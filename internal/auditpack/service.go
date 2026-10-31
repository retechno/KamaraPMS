package auditpack

import (
	"context"
	"math"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

// Outlets is the point of sale as the pack reads it (pos.Service). Without it, or without the module in the plan, the section of the outlets is off.
type Outlets interface {
	ListOutlets(ctx context.Context, propertyID int64, activeOnly *bool) ([]pos.Outlet, error)
	Report(ctx context.Context, propertyID, outletID int64, from, to civil.Date) (pos.OutletReport, error)
}

// Modules says whether the plan of the tenant includes the point of sale (an adapter over subscription.Service in internal/app).
type Modules interface {
	HasPOS(ctx context.Context, tenantID int64) (bool, error)
}

// Service builds and reads the packs.
type Service struct {
	txm     *db.TxManager
	clock   clock.Clock
	audit   *audit.Writer
	days    *tenancy.Service
	reports *reports.Service
	outlets Outlets
	modules Modules
	// override replaces the builder of a section: the tests make a section fail with it. It is never set by the application.
	override map[string]func(ctx context.Context) (any, error)
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, days *tenancy.Service, r *reports.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, days: days, reports: r}
}

// SetOutlets wires the point of sale and the plan that says whether the tenant has it.
func (s *Service) SetOutlets(o Outlets, m Modules) { s.outlets, s.modules = o, m }

func (s *Service) q(ctx context.Context) *auditpackdb.Queries { return auditpackdb.New(s.txm.DB(ctx)) }

// BuildPack is the step of the night audit (nightaudit.PackBuilder): it runs in the transaction of the close, after the day is closed.
func (s *Service) BuildPack(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, notes nightaudit.PackNotes) error {
	_, err := s.Build(ctx, p, propertyID, bd, false, &notes)
	return err
}

// Build makes the pack of a closed business day and stores it. Each section is built in a savepoint: a section that fails is stored as FAILED, the pack is PARTIAL, and everything else goes
// on (the day is closed whatever happens to a report). late is a pack made after the close: the sections that only the close of that night saw are NOT_AVAILABLE.
// It runs in the transaction of the caller, which must be open.
func (s *Service) Build(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, late bool, notes *nightaudit.PackNotes) (Pack, error) {
	start := s.clock.Now()
	j, err := s.newJob(ctx, p, propertyID, bd, late, notes)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{BusinessDate: bd.String(), FormatVersion: FormatVersion, Status: PackComplete, BuiltAt: start, BuiltLate: late, Sections: make([]Section, 0, len(Catalog))}
	for _, def := range Catalog {
		var sec Section
		if late && !def.Rebuildable {
			sec = Section{Code: def.Code, Status: StatusNotAvailable, FormatVersion: FormatVersion, BuiltLate: true}
		} else if sec, err = s.buildSection(ctx, j, def.Code); err != nil {
			return Pack{}, err
		}
		if sec.Status == StatusFailed {
			pack.Status = PackPartial
		}
		pack.Sections = append(pack.Sections, sec)
	}
	ms := min(s.clock.Now().Sub(start).Milliseconds(), math.MaxInt32)
	pack.BuildMS = int32(ms) //nolint:gosec // clamped to the range of the column above
	q := s.q(ctx)
	pack.ID, err = q.InsertPack(ctx, auditpackdb.InsertPackParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd, FormatVersion: FormatVersion, Status: pack.Status, BuiltAt: start,
		BuiltBy: p.ActorID(), BuildMs: pack.BuildMS, BuiltLate: late})
	if err != nil {
		return Pack{}, err
	}
	failed := []string{}
	for _, sec := range pack.Sections {
		var msg *string
		if sec.Status == StatusFailed {
			m := sec.Error
			msg = &m
			failed = append(failed, sec.Code)
		}
		if err := q.InsertSection(ctx, auditpackdb.InsertSectionParams{PackID: pack.ID, TenantID: p.TenantID, PropertyID: propertyID, Section: sec.Code, Status: sec.Status,
			FormatVersion: sec.FormatVersion, Data: sec.Data, Error: msg, BuiltLate: sec.BuiltLate}); err != nil {
			return Pack{}, err
		}
	}
	if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: "nap_pack.built", EntityType: "nap_pack",
		EntityID: pack.ID, EntityLabel: bd.String(), New: map[string]any{"business_date": bd, "status": pack.Status, "sections_failed": failed, "built_late": late, "recipients_queued": 0,
			"build_ms": pack.BuildMS}}); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

// buildSection builds one section in a savepoint. A failure of the section is its status; a failure of the savepoint itself is an error of the transaction.
func (s *Service) buildSection(ctx context.Context, j *job, code string) (Section, error) {
	sec := Section{Code: code, FormatVersion: FormatVersion, BuiltLate: j.late}
	tx, err := db.Tx(ctx)
	if err != nil {
		return sec, err
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT nap_section"); err != nil {
		return sec, err
	}
	data, off, berr := s.run(j, code)
	if berr != nil {
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT nap_section"); err != nil {
			return sec, err
		}
		sec.Status, sec.Error = StatusFailed, truncate(berr.Error(), MaxErrorLength)
	} else {
		sec.Data = data
		sec.Status = StatusOK
		if off {
			sec.Status, sec.Data = StatusOff, nil
		}
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT nap_section"); err != nil {
		return sec, err
	}
	return sec, nil
}

// truncate cuts a text at a character, to fit the column.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
