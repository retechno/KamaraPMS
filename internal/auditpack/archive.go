package auditpack

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The archive of the packs (docs/architecture/38-reports-night-audit-pack.md, 8.3): the list, the manifest of a day, the JSON of a section, the sections for the PDF, and the rebuild.

// MaxPackAgeDays is how old a day can be for a pack to be built for it later (the range limit of the other reports).
const MaxPackAgeDays = 400

// PageSize is the number of days of a page of the archive.
const PageSize = 31

// PDFMaker draws a pack as a PDF (documents.Service through an adapter in internal/app).
type PDFMaker interface {
	NightAuditPackPDF(ctx context.Context, propertyID int64, date civil.Date, sections []string) (filename string, pdf []byte, err error)
}

// SetPDF wires the PDF of a pack.
func (s *Service) SetPDF(p PDFMaker) { s.pdf = p }

// access authorizes the caller: the permission is asked before anything of the request is read.
func (s *Service) access(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	if err := s.authz.Require(ctx, propertyID, perm); err != nil {
		return p, err
	}
	return p, nil
}

// Summary is a day of the archive.
type Summary struct {
	BusinessDate   civil.Date `json:"business_date"`
	Status         string     `json:"status"`
	FormatVersion  int16      `json:"format_version"`
	BuiltAt        time.Time  `json:"built_at"`
	BuildMS        int32      `json:"build_ms"`
	BuiltLate      bool       `json:"built_late"`
	SectionsOK     int        `json:"sections_ok"`
	SectionsFailed int        `json:"sections_failed"`
	// MailSent, MailFailed and MailQueued count the messages of the pack by their state (PR 3); the archive shows them beside the day.
	MailSent   int `json:"mail_sent"`
	MailFailed int `json:"mail_failed"`
	MailQueued int `json:"mail_queued"`
}

// List is a page of the archive, the newest day first.
type List struct {
	Items []Summary `json:"items"`
	// EmailEnabled says whether the server can send e-mail; without it the archive says "E-mail is off" and queues nothing.
	EmailEnabled bool `json:"email_enabled"`
	// NextCursor is the business date to ask the next page after; null on the last page.
	NextCursor *civil.Date `json:"next_cursor"`
}

// ListInput is the query of the archive. Without From and To the last 90 days up to the open business date are listed.
type ListInput struct {
	From, To *civil.Date
	Cursor   *civil.Date
}

// List answers the archive (nightaudit.pack_view).
func (s *Service) List(ctx context.Context, propertyID int64, in ListInput) (List, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackView)
	if err != nil {
		return List{}, err
	}
	to, from := civil.Date{}, civil.Date{}
	if in.To != nil {
		to = *in.To
	} else {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return List{}, err
		}
		to = day.BusinessDate
	}
	if in.From != nil {
		from = *in.From
	} else {
		from = to.AddDays(-89)
	}
	if to.Before(from) || from.DaysUntil(to)+1 > 366 {
		return List{}, apperr.Invalid("the range is invalid", apperr.FieldError{Field: "to", Code: "OUT_OF_RANGE", Message: "on or after from, at most 366 days"})
	}
	rows, err := s.q(ctx).ListPacks(ctx, auditpackdb.ListPacksParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to, Before: in.Cursor, PageSize: PageSize + 1})
	if err != nil {
		return List{}, err
	}
	out := List{Items: []Summary{}, EmailEnabled: s.mailEnabled()}
	for i, r := range rows {
		if i == PageSize {
			next := out.Items[PageSize-1].BusinessDate
			out.NextCursor = &next
			break
		}
		out.Items = append(out.Items, Summary{BusinessDate: r.BusinessDate, Status: r.Status, FormatVersion: r.FormatVersion, BuiltAt: r.BuiltAt, BuildMS: r.BuildMs, BuiltLate: r.BuiltLate,
			SectionsOK: int(r.SectionsOk), SectionsFailed: int(r.SectionsFailed), MailSent: int(r.MailSent), MailFailed: int(r.MailFailed), MailQueued: int(r.MailQueued)})
	}
	return out, nil
}

// SectionInfo is a section in the manifest of a pack.
type SectionInfo struct {
	Section       string `json:"section"`
	Status        string `json:"status"`
	FormatVersion int16  `json:"format_version"`
	Error         string `json:"error,omitempty"`
	BuiltLate     bool   `json:"built_late"`
	SizeBytes     int    `json:"size_bytes"`
	Rebuildable   bool   `json:"rebuildable"`
}

// Manifest is the pack of a day without the data of its sections.
type Manifest struct {
	BusinessDate  civil.Date    `json:"business_date"`
	Status        string        `json:"status"`
	FormatVersion int16         `json:"format_version"`
	BuiltAt       time.Time     `json:"built_at"`
	BuiltBy       string        `json:"built_by"`
	BuildMS       int32         `json:"build_ms"`
	BuiltLate     bool          `json:"built_late"`
	Sections      []SectionInfo `json:"sections"`
	// EmailEnabled and the counts of the messages of the pack by state: the result screen of the run says how many wait.
	EmailEnabled bool `json:"email_enabled"`
	MailQueued   int  `json:"mail_queued"`
	MailSent     int  `json:"mail_sent"`
	MailFailed   int  `json:"mail_failed"`
}

func packNotFound(date civil.Date) error {
	return apperr.NotFound("PACK_NOT_FOUND", "there is no night audit pack for this business date").WithContext("business_date", date.String())
}

// stored is a pack as the database holds it.
type stored struct {
	id       int64
	manifest Manifest
	sections []auditpackdb.ListPackSectionsRow
}

func (s *Service) load(ctx context.Context, p auth.Principal, propertyID int64, date civil.Date) (stored, error) {
	q := s.q(ctx)
	h, err := q.GetPackByDate(ctx, auditpackdb.GetPackByDateParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: date})
	if errors.Is(err, pgx.ErrNoRows) {
		return stored{}, packNotFound(date)
	}
	if err != nil {
		return stored{}, err
	}
	rows, err := q.ListPackSections(ctx, auditpackdb.ListPackSectionsParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: h.ID})
	if err != nil {
		return stored{}, err
	}
	m := Manifest{BusinessDate: date, Status: h.Status, FormatVersion: h.FormatVersion, BuiltAt: h.BuiltAt, BuiltBy: h.BuiltByName, BuildMS: h.BuildMs, BuiltLate: h.BuiltLate, Sections: []SectionInfo{}}
	byCode := map[string]auditpackdb.ListPackSectionsRow{}
	for _, r := range rows {
		byCode[r.Section] = r
	}
	for _, def := range Catalog { // the order of the catalog, whatever the order of the rows
		r, ok := byCode[def.Code]
		if !ok {
			continue
		}
		info := SectionInfo{Section: r.Section, Status: r.Status, FormatVersion: r.FormatVersion, BuiltLate: r.BuiltLate, SizeBytes: int(r.SizeBytes), Rebuildable: def.Rebuildable}
		if r.Error != nil {
			info.Error = *r.Error
		}
		m.Sections = append(m.Sections, info)
	}
	return stored{id: h.ID, manifest: m, sections: rows}, nil
}

// Manifest answers the pack of a day (nightaudit.pack_view).
func (s *Service) Manifest(ctx context.Context, propertyID int64, date civil.Date) (Manifest, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackView)
	if err != nil {
		return Manifest{}, err
	}
	st, err := s.load(ctx, p, propertyID, date)
	if err != nil {
		return Manifest{}, err
	}
	c, err := s.q(ctx).CountPackMail(ctx, auditpackdb.CountPackMailParams{PropertyID: propertyID, PackID: &st.id})
	if err != nil {
		return Manifest{}, err
	}
	st.manifest.EmailEnabled, st.manifest.MailQueued, st.manifest.MailSent, st.manifest.MailFailed = s.mailEnabled(), int(c.Queued), int(c.Sent), int(c.Failed)
	return st.manifest, nil
}

// SectionData is the stored JSON of a section (nightaudit.pack_view). A section that holds no data (failed, off, not available) is a 404: it has nothing to download.
func (s *Service) SectionData(ctx context.Context, propertyID int64, date civil.Date, code string) ([]byte, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackView)
	if err != nil {
		return nil, err
	}
	if !Known(code) {
		return nil, invalidSection(code)
	}
	st, err := s.load(ctx, p, propertyID, date)
	if err != nil {
		return nil, err
	}
	for _, r := range st.sections {
		if r.Section == code {
			if r.Status != StatusOK {
				return nil, apperr.NotFound("PACK_NOT_FOUND", "this section holds no data").WithContext("section", code).WithContext("status", r.Status)
			}
			return r.Data, nil
		}
	}
	return nil, packNotFound(date)
}

func invalidSection(code string) error {
	return apperr.New(apperr.KindInvalid, "PACK_SECTION_INVALID", "this is not a section of the night audit pack").WithContext("section", code)
}

// ForPDF is a pack with the data of the sections to print.
type ForPDF struct {
	Manifest Manifest
	Sections []Section
}

// ForPDF reads the sections to print (nightaudit.pack_view): the codes asked for, or every section that holds data. The order is the catalog's.
func (s *Service) ForPDF(ctx context.Context, propertyID int64, date civil.Date, codes []string) (ForPDF, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackView)
	if err != nil {
		return ForPDF{}, err
	}
	want := map[string]bool{}
	for _, c := range codes {
		if !Known(c) {
			return ForPDF{}, invalidSection(c)
		}
		want[c] = true
	}
	st, err := s.load(ctx, p, propertyID, date)
	if err != nil {
		return ForPDF{}, err
	}
	out := ForPDF{Manifest: st.manifest}
	byCode := map[string]auditpackdb.ListPackSectionsRow{}
	for _, r := range st.sections {
		byCode[r.Section] = r
	}
	for _, def := range Catalog {
		r, ok := byCode[def.Code]
		if !ok || r.Status != StatusOK || (len(want) > 0 && !want[def.Code]) {
			continue
		}
		out.Sections = append(out.Sections, Section{Code: r.Section, Status: r.Status, FormatVersion: r.FormatVersion, Data: r.Data, BuiltLate: r.BuiltLate})
	}
	if len(out.Sections) == 0 {
		return ForPDF{}, apperr.New(apperr.KindInvalid, "PACK_SECTION_INVALID", "no section of the list holds data, so there is nothing to print")
	}
	return out, nil
}

// PDF draws the pack of a day (nightaudit.pack_view); sections empty means every section that holds data.
func (s *Service) PDF(ctx context.Context, propertyID int64, date civil.Date, codes []string) (string, []byte, error) {
	if _, err := s.access(ctx, propertyID, auth.PermNightAuditPackView); err != nil {
		return "", nil, err
	}
	if s.pdf == nil {
		return "", nil, errors.New("auditpack: the PDF of a pack is not wired")
	}
	return s.pdf.NightAuditPackPDF(ctx, propertyID, date, codes)
}

// Rebuild makes the pack of a closed day that has none (from the ledger: the four sections that only the close of the night saw are NOT_AVAILABLE), or builds again the sections of a pack that
// failed and can be made again (nightaudit.pack_manage). It runs in its own transaction and queues no e-mail.
func (s *Service) Rebuild(ctx context.Context, propertyID int64, date civil.Date) (Manifest, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackManage)
	if err != nil {
		return Manifest{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		day, err := q.GetDay(ctx, auditpackdb.GetDayParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: date})
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "this date was never a business day of the property").WithContext("business_date", date.String())
		}
		if err != nil {
			return err
		}
		if day.Status != "CLOSED" {
			return apperr.Conflict("DATE_NOT_CLOSED", "the business day is not closed yet, so its books are not complete").WithContext("business_date", date.String())
		}
		open, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if date.DaysUntil(open.BusinessDate) > MaxPackAgeDays {
			return apperr.Invalid("the business date is too old", apperr.FieldError{Field: "date", Code: "OUT_OF_RANGE", Message: "at most 400 days back"})
		}
		if _, err := q.GetPackByDate(ctx, auditpackdb.GetPackByDateParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: date}); errors.Is(err, pgx.ErrNoRows) {
			_, err := s.Build(ctx, p, propertyID, date, true, nil)
			return err
		} else if err != nil {
			return err
		}
		return s.rebuildFailed(ctx, p, propertyID, date)
	})
	if err != nil {
		return Manifest{}, err
	}
	st, err := s.load(ctx, p, propertyID, date)
	return st.manifest, err
}

// rebuildFailed builds again the failed sections that can be made from the ledger.
func (s *Service) rebuildFailed(ctx context.Context, p auth.Principal, propertyID int64, date civil.Date) error {
	st, err := s.load(ctx, p, propertyID, date)
	if err != nil {
		return err
	}
	var todo, stuck []string
	for _, info := range st.manifest.Sections {
		switch {
		case info.Status != StatusFailed:
		case info.Rebuildable:
			todo = append(todo, info.Section)
		default:
			stuck = append(stuck, info.Section)
		}
	}
	if len(todo) == 0 {
		if len(stuck) > 0 {
			return apperr.Conflict("SECTION_NOT_REBUILDABLE", "the sections that failed are what only the close of the night saw: they cannot be built again").WithContext("sections", stuck)
		}
		return nil // nothing failed: nothing to do
	}
	j, err := s.newJob(ctx, p, propertyID, date, true, nil)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	var done, failed []string
	for _, code := range todo {
		sec, err := s.buildSection(ctx, j, code)
		if err != nil {
			return err
		}
		var msg *string
		if sec.Status == StatusFailed {
			msg = &sec.Error
			failed = append(failed, code)
		} else {
			done = append(done, code)
		}
		n, err := q.RebuildSection(ctx, auditpackdb.RebuildSectionParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: st.id, Section: code, Status: sec.Status, Data: sec.Data, Error: msg})
		if err != nil {
			return err
		}
		if n != 1 { // another rebuild got there first
			return apperr.Conflict("DUPLICATE_REQUEST", "the pack was built again at the same time")
		}
	}
	status := PackComplete
	if len(failed) > 0 || len(stuck) > 0 {
		status = PackPartial
	}
	if status != st.manifest.Status {
		if err := q.SetPackStatus(ctx, auditpackdb.SetPackStatusParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: st.id, Status: status}); err != nil {
			return err
		}
	}
	return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &date, UserID: p.ActorID(), Action: "nap_pack.rebuilt", EntityType: "nap_pack", EntityID: st.id,
		EntityLabel: date.String(), Old: map[string]any{"status": st.manifest.Status}, New: map[string]any{"status": status, "rebuilt": done, "still_failed": failed}})
}
