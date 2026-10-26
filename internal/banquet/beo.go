package banquet

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/tenancy"
)

// The BEO of the sessions, its revisions, the people told, and the documents (36 4.6, B11, B12, Q8, Q10).

const (
	maxSectionText = 4000
	maxRunLines    = 60
	maxRunText     = 300
	maxNote        = 300
)

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------------------------------------------------------------- requests and answers

// RunLineInput is a line of the programme in a request.
type RunLineInput struct {
	At   civil.TimeOfDay `json:"at_time"`
	Text string          `json:"text"`
}

// BeoInput saves the BEO of a session: the sections it names (the others stay), the programme when it is given (an empty list clears it), and why, for the history.
type BeoInput struct {
	Sections  map[string]string `json:"sections"`
	RunOfShow []RunLineInput    `json:"run_of_show"`
	Note      *string           `json:"note"`
}

// AckInput marks a section of a session as read at the last revision; with no section, every section of the session.
type AckInput struct {
	SessionID int64  `json:"session_id"`
	Section   string `json:"section"`
}

// Reader is a person who read a section, and the revision they read.
type Reader struct {
	UserName string    `json:"user_name"`
	Revision int       `json:"revision"`
	At       time.Time `json:"at"`
}

// BeoSection is a section of the BEO of a session with where it stands for the departments: who is told, who read it and whether it changed since.
type BeoSection struct {
	Section        string  `json:"section"`
	Body           string  `json:"body"`
	DepartmentID   *int64  `json:"department_id"`
	DepartmentName *string `json:"department_name"`
	Head           *string `json:"head"`
	Deputy         *string `json:"deputy"`
	// NoDepartment is true for a section of a department that the property did not map: nobody is told when it changes, and the screen says so.
	NoDepartment bool `json:"no_department"`
	// State is DRAFT (before Definite there are no revisions), UNREAD, READ (read, and nothing changed since) or CHANGED (changed since ChangedSince, the last revision that was read).
	State        string   `json:"state"`
	ReadRevision *int     `json:"read_revision"`
	ChangedSince *int     `json:"changed_since"`
	ReadBy       []Reader `json:"read_by"`
}

// BeoSession is the BEO of one session.
type BeoSession struct {
	ID            int64            `json:"id"`
	No            int              `json:"no"`
	Name          string           `json:"name"`
	Date          civil.Date       `json:"date"`
	Start         *civil.TimeOfDay `json:"start"`
	End           *civil.TimeOfDay `json:"end"`
	EndsNextDay   bool             `json:"ends_next_day"`
	Room          string           `json:"room"`
	Setup         string           `json:"setup"`
	PaxEstimated  int              `json:"pax_estimated"`
	PaxGuaranteed *int             `json:"pax_guaranteed"`
	Status        string           `json:"status"`
	Sections      []BeoSection     `json:"sections"`
	RunOfShow     []RunLine        `json:"run_of_show"`
}

// Revision is a line of the history of the BEO.
type Revision struct {
	No            int       `json:"revision_no"`
	Note          *string   `json:"note"`
	Changes       []Change  `json:"changes"`
	Notified      int       `json:"notified"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
}

// SectionRecipient is the department that reads a section, with the two people told when it changes.
type SectionRecipient struct {
	Section        string  `json:"section"`
	DepartmentID   *int64  `json:"department_id"`
	DepartmentCode *string `json:"department_code"`
	DepartmentName *string `json:"department_name"`
	Head           *string `json:"head"`
	Deputy         *string `json:"deputy"`
}

// BeoView is the BEO of an event as its tab shows it.
type BeoView struct {
	EventID    int64              `json:"event_id"`
	Number     string             `json:"number"`
	Name       string             `json:"name"`
	Status     string             `json:"status"`
	Revision   int                `json:"revision"`
	Draft      bool               `json:"draft"`
	Sessions   []BeoSession       `json:"sessions"`
	Revisions  []Revision         `json:"revisions"`
	Recipients []SectionRecipient `json:"recipients"`
}

// RevisionView is the BEO of an event as it was at a revision.
type RevisionView struct {
	No        int       `json:"revision_no"`
	CreatedAt time.Time `json:"created_at"`
	Snapshot  Snapshot  `json:"snapshot"`
}

// RecipientItem sets the department of a section; no department takes the section off.
type RecipientItem struct {
	Section      string `json:"section"`
	DepartmentID *int64 `json:"department_id"`
}

// RecipientsInput sets the departments of the sections of the BEO.
type RecipientsInput struct {
	Items []RecipientItem `json:"items"`
}

// DocumentRow is a proposal that was sent or a contract that was issued.
type DocumentRow struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	Number        *string   `json:"number"`
	RevisionNo    *int      `json:"revision_no"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedByName string    `json:"created_by_name"`
}

// DocumentFile is what a recorded document is rendered from: its snapshot (the one of the revision for a contract, its own for a proposal).
type DocumentFile struct {
	ID         int64      `json:"id"`
	EventID    int64      `json:"event_id"`
	Kind       string     `json:"kind"`
	Number     *string    `json:"number"`
	RevisionNo *int       `json:"revision_no"`
	CreatedAt  time.Time  `json:"created_at"`
	Snapshot   Snapshot   `json:"snapshot"`
	IssuedAt   *time.Time `json:"issued_at"`
}

// ---------------------------------------------------------------- the snapshot

// snapshot reads the whole document of an event as it is now, inside the transaction of the change that asks.
func (s *Service) snapshot(ctx context.Context, tenantID, propertyID, eventID int64) (Snapshot, error) {
	ev, err := s.event(ctx, tenantID, propertyID, eventID)
	if err != nil {
		return Snapshot{}, err
	}
	q := s.q(ctx)
	ids := make([]int64, len(ev.Sessions))
	for i, sn := range ev.Sessions {
		ids[i] = sn.ID
	}
	secRows, err := q.ListBeoSections(ctx, banquetdb.ListBeoSectionsParams{PropertyID: propertyID, SessionIds: ids})
	if err != nil {
		return Snapshot{}, err
	}
	runRows, err := q.ListRunOfShow(ctx, banquetdb.ListRunOfShowParams{PropertyID: propertyID, SessionIds: ids})
	if err != nil {
		return Snapshot{}, err
	}
	lines, est, err := s.eventEstimate(ctx, tenantID, propertyID, eventID)
	if err != nil {
		return Snapshot{}, err
	}
	sched, err := s.schedule(ctx, propertyID, eventID)
	if err != nil {
		return Snapshot{}, err
	}
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: tenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return Snapshot{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	tiers, err := s.tiers(ctx, tenantID, propertyID, nil, &eventID)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Version: SnapshotVersion, Estimate: est, Lines: lines, Deposits: []SnapDeposit{}, Policy: SnapPolicy{Base: derefStr(hdr.PolicyBase), Tiers: tiers}}
	if snap.Lines == nil {
		snap.Lines = []Line{}
	}
	if snap.Policy.Tiers == nil {
		snap.Policy.Tiers = []Tier{}
	}
	snap.Event = SnapEvent{Number: ev.Number, Name: ev.Name, Status: ev.Status, EventType: derefStr(ev.EventTypeName), Booker: ev.BookerName, ContactName: derefStr(ev.ContactName),
		ContactPhone: derefStr(ev.ContactPhone), ContactEmail: derefStr(ev.ContactEmail), SalesName: ev.SalesName, GroupName: derefStr(ev.BookingGroupName), Notes: derefStr(ev.Notes)}
	if ev.OptionUntil != nil {
		snap.Event.OptionUntil = ev.OptionUntil.String()
	}
	for _, d := range sched {
		snap.Deposits = append(snap.Deposits, SnapDeposit{Label: d.Label, DueDate: d.DueDate, Percent: d.Percent, Amount: d.Amount})
	}
	texts := map[int64]map[string]string{}
	for _, r := range secRows {
		if texts[r.SessionID] == nil {
			texts[r.SessionID] = map[string]string{}
		}
		texts[r.SessionID][r.Section] = r.Body
	}
	runs := map[int64][]RunLine{}
	for _, r := range runRows {
		runs[r.SessionID] = append(runs[r.SessionID], RunLine{Seq: int(r.Seq), At: r.AtTime, Text: r.Body})
	}
	for _, sn := range ev.Sessions {
		ss := SnapSession{ID: sn.ID, No: sn.SessionNo, Name: sn.Name, Date: sn.SessionDate, Start: sn.StartTime, End: sn.EndTime, EndsNextDay: sn.EndsNextDay, Room: derefStr(sn.RoomName),
			Setup: derefStr(sn.SetupStyle), PaxEstimated: sn.PaxEstimated, PaxGuaranteed: sn.PaxGuaranteed, Status: sn.Status, Notes: derefStr(sn.Notes), Sections: map[string]string{}, RunOfShow: runs[sn.ID]}
		if ss.RunOfShow == nil {
			ss.RunOfShow = []RunLine{}
		}
		for _, sec := range Sections {
			ss.Sections[sec] = texts[sn.ID][sec]
		}
		snap.Sessions = append(snap.Sessions, ss)
	}
	return snap, nil
}

func decodeSnapshot(b []byte) (Snapshot, error) {
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		return Snapshot{}, err
	}
	if out.Version > SnapshotVersion {
		return Snapshot{}, apperr.Internal(errors.New("the snapshot of the document is of a newer version"))
	}
	return out, nil
}

// ---------------------------------------------------------------- the revision of a save

type reviseOpts struct {
	note      *string
	confirmed bool
}

// revise makes the revision that a save of a definite event is (Q10): the snapshot after the save, what changed against the revision before, and the notices to the head and the deputy of the
// department of every section that changed. It runs in the transaction of the save, which holds the event, so a rollback leaves no revision and no notice. A save that changes nothing makes none.
// It answers the number of the revision in force (0 when the event is not definite and the BEO is a draft).
func (s *Service) revise(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, eventID int64, o reviseOpts) (int, error) {
	q := s.q(ctx)
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return 0, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	if hdr.Status != StatusDefinite {
		return 0, nil
	}
	snap, err := s.snapshot(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return 0, err
	}
	var old *Snapshot
	lastNo := 0
	last, err := q.LastRevision(ctx, banquetdb.LastRevisionParams{PropertyID: propertyID, EventID: eventID})
	switch {
	case err == nil:
		prev, err := decodeSnapshot(last.Snapshot)
		if err != nil {
			return 0, err
		}
		old, lastNo = &prev, int(last.RevisionNo)
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, err
	}
	changes, flags := DiffSnapshots(old, snap)
	if o.confirmed && old == nil {
		changes = []Change{{Kind: ChangeConfirmed}}
	}
	if len(changes) == 0 {
		return lastNo, nil
	}
	users, err := s.peopleOf(ctx, p, propertyID, flags)
	if err != nil {
		return 0, err
	}
	cj, err := json.Marshal(changes)
	if err != nil {
		return 0, err
	}
	sj, err := json.Marshal(snap)
	if err != nil {
		return 0, err
	}
	var note *string
	if o.note != nil && strings.TrimSpace(*o.note) != "" {
		note = ptr(strings.TrimSpace(*o.note))
	}
	row, err := q.InsertRevision(ctx, banquetdb.InsertRevisionParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, Note: note, Changes: cj, Snapshot: sj, Notified: int32(len(users)), ActorID: p.ActorID()}) //nolint:gosec // G115: a few people
	if err != nil {
		return 0, err
	}
	var first *int64
	ids := make([]int64, 0, len(flags))
	for id := range flags {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) > 0 {
		first = &ids[0]
	}
	rn := row.RevisionNo
	for _, u := range users {
		if err := q.UpsertRevisionNotice(ctx, banquetdb.UpsertRevisionNoticeParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: u, EventID: eventID, SessionID: first, NoticeDate: bd, RevisionNo: &rn}); err != nil {
			return 0, err
		}
	}
	data := map[string]any{"revision_no": row.RevisionNo, "changes": len(changes), "notified": len(users), "sections": flags.Sections()}
	if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.beo_revised", "banquet_event", eventID, hdr.Number, nil, data)); err != nil {
		return 0, err
	}
	return int(row.RevisionNo), nil
}

// peopleOf finds who is told: the head and the deputy of the department of every section that changed, once each, not the person who made the change. A section with no department tells nobody.
func (s *Service) peopleOf(ctx context.Context, p auth.Principal, propertyID int64, flags Flags) ([]int64, error) {
	secs := flags.Sections()
	if len(secs) == 0 {
		return nil, nil
	}
	rows, err := s.q(ctx).SectionPeople(ctx, banquetdb.SectionPeopleParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, r := range rows {
		if !slices.Contains(secs, r.Section) {
			continue
		}
		for _, id := range []int64{r.HeadUserID, r.DeputyUserID} {
			if id != 0 && id != p.UserID && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- saving the BEO of a session

func checkBeo(in BeoInput) []apperrField {
	var f []apperrField
	for sec, body := range in.Sections {
		if !IsSection(sec) {
			f = append(f, fieldErr("sections", "INVALID_VALUE", "an unknown section: "+sec))
		} else if len([]rune(body)) > maxSectionText {
			f = append(f, fieldErr("sections."+sec, "INVALID_VALUE", "at most 4000 characters"))
		}
	}
	if len(in.RunOfShow) > maxRunLines {
		f = append(f, fieldErr("run_of_show", "INVALID_VALUE", "at most 60 lines"))
	}
	for _, r := range in.RunOfShow {
		if n := len([]rune(strings.TrimSpace(r.Text))); n < 1 || n > maxRunText {
			f = append(f, fieldErr("run_of_show", "INVALID_VALUE", "1 to 300 characters in each line"))
			break
		}
	}
	if in.Note != nil && len([]rune(*in.Note)) > maxNote {
		f = append(f, fieldErr("note", "INVALID_VALUE", "at most 300 characters"))
	}
	return f
}

type apperrField = apperr.FieldError

// SaveBeo saves the texts and the programme of a session (banquet.manage). On a definite event the save is one revision, whatever number of sections it touches; before Definite the BEO is a draft
// and has none. A save that changes nothing writes nothing.
func (s *Service) SaveBeo(ctx context.Context, propertyID, eventID, sessionID int64, in BeoInput) (BeoView, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return BeoView{}, err
	}
	if f := checkBeo(in); len(f) > 0 {
		return BeoView{}, invalid("BEO", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		sn, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, sessionID)
		if err != nil {
			return err
		}
		if sn.Status != "ACTIVE" {
			return apperr.Conflict("SESSION_CANCELLED", "a cancelled session has no BEO to change")
		}
		q := s.q(ctx)
		cur, err := q.ListBeoSections(ctx, banquetdb.ListBeoSectionsParams{PropertyID: propertyID, SessionIds: []int64{sessionID}})
		if err != nil {
			return err
		}
		have := map[string]string{}
		for _, r := range cur {
			have[r.Section] = r.Body
		}
		var changed []string
		secs := make([]string, 0, len(in.Sections))
		for sec := range in.Sections {
			secs = append(secs, sec)
		}
		sort.Strings(secs)
		for _, sec := range secs {
			body := strings.TrimSpace(in.Sections[sec])
			if have[sec] == body {
				continue
			}
			if err := q.UpsertBeoSection(ctx, banquetdb.UpsertBeoSectionParams{TenantID: p.TenantID, PropertyID: propertyID, SessionID: sessionID, Section: sec, Body: body, ActorID: p.ActorID()}); err != nil {
				return err
			}
			changed = append(changed, sec)
		}
		runChanged := false
		if in.RunOfShow != nil {
			rows, err := q.ListRunOfShow(ctx, banquetdb.ListRunOfShowParams{PropertyID: propertyID, SessionIds: []int64{sessionID}})
			if err != nil {
				return err
			}
			before := make([]RunLine, len(rows))
			for i, r := range rows {
				before[i] = RunLine{Seq: int(r.Seq), At: r.AtTime, Text: r.Body}
			}
			next := append([]RunLineInput(nil), in.RunOfShow...)
			sort.SliceStable(next, func(i, j int) bool { return next[i].At.Compare(next[j].At) < 0 })
			after := make([]RunLine, len(next))
			for i, r := range next {
				after[i] = RunLine{Seq: i + 1, At: r.At, Text: strings.TrimSpace(r.Text)}
			}
			if runText(before) != runText(after) {
				if err := q.DeleteRunOfShow(ctx, banquetdb.DeleteRunOfShowParams{PropertyID: propertyID, SessionID: sessionID}); err != nil {
					return err
				}
				for _, r := range after {
					if err := q.InsertRunOfShow(ctx, banquetdb.InsertRunOfShowParams{TenantID: p.TenantID, PropertyID: propertyID, SessionID: sessionID, Seq: int32(r.Seq), AtTime: r.At, Body: r.Text}); err != nil { //nolint:gosec // G115: at most 60
						return err
					}
				}
				runChanged = true
			}
		}
		if len(changed) == 0 && !runChanged {
			return nil
		}
		rev, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{note: in.Note})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.beo_saved", "banquet_session", sessionID, hdr.Number+" / "+sn.Name, nil, map[string]any{"sections": changed, "run_of_show": runChanged, "revision_no": rev}))
	})
	if err != nil {
		return BeoView{}, err
	}
	return s.Beo(ctx, propertyID, eventID)
}

// ---------------------------------------------------------------- reading the BEO

// Beo answers the BEO of an event: the sections of every active session, where each stands for the departments, the history of the revisions and the department of each section.
func (s *Service) Beo(ctx context.Context, propertyID, eventID int64) (BeoView, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return BeoView{}, err
	}
	snap, err := s.snapshot(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return BeoView{}, err
	}
	q := s.q(ctx)
	ev, err := s.event(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return BeoView{}, err
	}
	revs, err := q.ListRevisions(ctx, banquetdb.ListRevisionsParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return BeoView{}, err
	}
	out := BeoView{EventID: eventID, Number: ev.Number, Name: ev.Name, Status: ev.Status, Sessions: []BeoSession{}, Revisions: make([]Revision, 0, len(revs))}
	for _, r := range revs {
		rv := Revision{No: int(r.RevisionNo), Note: r.Note, Notified: int(r.Notified), CreatedByName: r.CreatedByName, CreatedAt: r.CreatedAt, Changes: []Change{}}
		if err := json.Unmarshal(r.Changes, &rv.Changes); err != nil {
			return BeoView{}, err
		}
		out.Revisions = append(out.Revisions, rv)
	}
	if len(revs) > 0 {
		out.Revision = int(revs[0].RevisionNo)
	}
	out.Draft = out.Revision == 0
	out.Recipients, err = s.recipients(ctx, p.TenantID, propertyID)
	if err != nil {
		return BeoView{}, err
	}
	people := map[string]SectionRecipient{}
	for _, r := range out.Recipients {
		people[r.Section] = r
	}
	acks, err := q.ListAcks(ctx, banquetdb.ListAcksParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return BeoView{}, err
	}
	// the snapshots of the revisions that were read, each loaded once
	thens := map[int]Snapshot{}
	then := func(no int) (Snapshot, error) {
		if v, ok := thens[no]; ok {
			return v, nil
		}
		r, err := q.GetRevision(ctx, banquetdb.GetRevisionParams{PropertyID: propertyID, EventID: eventID, RevisionNo: int32(no)}) //nolint:gosec // G115: a revision number
		if err != nil {
			return Snapshot{}, err
		}
		v, err := decodeSnapshot(r.Snapshot)
		if err == nil {
			thens[no] = v
		}
		return v, err
	}
	for _, ss := range snap.Sessions {
		if ss.Status != "ACTIVE" {
			continue
		}
		bs := BeoSession{ID: ss.ID, No: ss.No, Name: ss.Name, Date: ss.Date, Start: ss.Start, End: ss.End, EndsNextDay: ss.EndsNextDay, Room: ss.Room, Setup: ss.Setup, PaxEstimated: ss.PaxEstimated,
			PaxGuaranteed: ss.PaxGuaranteed, Status: ss.Status, RunOfShow: ss.RunOfShow, Sections: make([]BeoSection, 0, len(Sections))}
		for _, sec := range Sections {
			x := BeoSection{Section: sec, Body: ss.Sections[sec], State: StateDraft, ReadBy: []Reader{}}
			if rc, ok := people[sec]; ok && IsDepartmentSection(sec) {
				x.DepartmentID, x.DepartmentName, x.Head, x.Deputy = rc.DepartmentID, rc.DepartmentName, rc.Head, rc.Deputy
				x.NoDepartment = rc.DepartmentID == nil
			}
			var readMax int
			for _, a := range acks {
				if a.SessionID != ss.ID || a.Section != sec {
					continue
				}
				x.ReadBy = append(x.ReadBy, Reader{UserName: a.UserName, Revision: int(a.RevisionNo), At: a.ReadAt})
				readMax = max(readMax, int(a.RevisionNo))
			}
			if !out.Draft {
				if readMax == 0 {
					x.State = StateUnread
				} else {
					x.ReadRevision = ptr(readMax)
					x.State = StateRead
					if readMax < out.Revision {
						was, err := then(readMax)
						if err != nil {
							return BeoView{}, err
						}
						if SectionChangedSince(was, snap, ss.ID, sec) {
							x.State, x.ChangedSince = StateChanged, ptr(readMax)
						}
					}
				}
			}
			bs.Sections = append(bs.Sections, x)
		}
		out.Sessions = append(out.Sessions, bs)
	}
	return out, nil
}

// RevisionOf answers the BEO of an event as it was at a revision, from its snapshot.
func (s *Service) RevisionOf(ctx context.Context, propertyID, eventID int64, no int) (RevisionView, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return RevisionView{}, err
	}
	snap, at, err := s.revisionSnapshot(ctx, p.TenantID, propertyID, eventID, no)
	return RevisionView{No: no, CreatedAt: at, Snapshot: snap}, err
}

func (s *Service) revisionSnapshot(ctx context.Context, tenantID, propertyID, eventID int64, no int) (Snapshot, time.Time, error) {
	q := s.q(ctx)
	if _, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: tenantID, PropertyID: propertyID, ID: eventID}); err != nil {
		return Snapshot{}, time.Time{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	r, err := q.GetRevision(ctx, banquetdb.GetRevisionParams{PropertyID: propertyID, EventID: eventID, RevisionNo: int32(no)}) //nolint:gosec // G115: a revision number
	if err != nil {
		return Snapshot{}, time.Time{}, orNotFound(err, notFound("REVISION_NOT_FOUND", "revision"))
	}
	snap, err := decodeSnapshot(r.Snapshot)
	return snap, r.CreatedAt, err
}

// AckBeo marks a section of a session (or every section of it) as read by the caller at the last revision, so that the next change shows "changed since" (36 4.6). A draft has no revision to read.
func (s *Service) AckBeo(ctx context.Context, propertyID, eventID int64, in AckInput) (BeoView, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return BeoView{}, err
	}
	if in.Section != "" && !IsSection(in.Section) {
		return BeoView{}, invalid("acknowledgement", fieldErr("section", "INVALID_VALUE", "an unknown section"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		last, err := q.LastRevision(ctx, banquetdb.LastRevisionParams{PropertyID: propertyID, EventID: eventID})
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.Conflict("BEO_IS_DRAFT", "the BEO has no revision yet: it is a draft until the event is definite")
		}
		if err != nil {
			return err
		}
		sn, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, in.SessionID)
		if err != nil {
			return err
		}
		secs := Sections
		if in.Section != "" {
			secs = []string{in.Section}
		}
		for _, sec := range secs {
			if err := q.UpsertAck(ctx, banquetdb.UpsertAckParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, SessionID: in.SessionID, Section: sec, UserID: p.UserID, RevisionNo: last.RevisionNo}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.beo_read", "banquet_session", in.SessionID, hdr.Number+" / "+sn.Name, nil, map[string]any{"sections": secs, "revision_no": last.RevisionNo}))
	})
	if err != nil {
		return BeoView{}, err
	}
	return s.Beo(ctx, propertyID, eventID)
}

// ---------------------------------------------------------------- who is told

func (s *Service) recipients(ctx context.Context, tenantID, propertyID int64) ([]SectionRecipient, error) {
	rows, err := s.q(ctx).SectionPeople(ctx, banquetdb.SectionPeopleParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	by := map[string]banquetdb.SectionPeopleRow{}
	for _, r := range rows {
		by[r.Section] = r
	}
	out := make([]SectionRecipient, 0, len(DepartmentSections))
	for _, sec := range DepartmentSections {
		r, ok := by[sec]
		x := SectionRecipient{Section: sec}
		if ok {
			x.DepartmentID, x.DepartmentCode, x.DepartmentName, x.Head, x.Deputy = ptr(r.DepartmentID), ptr(r.DepartmentCode), ptr(r.DepartmentName), r.HeadName, r.DeputyName
		}
		out = append(out, x)
	}
	return out, nil
}

// ListRecipients answers the department of each section of the BEO, with the people told (36 Q8).
func (s *Service) ListRecipients(ctx context.Context, propertyID int64) ([]SectionRecipient, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	return s.recipients(ctx, p.TenantID, propertyID)
}

// PutRecipients sets the department of the sections it names (banquet.setup); no department takes a section off, and nobody is told of it.
func (s *Service) PutRecipients(ctx context.Context, propertyID int64, in RecipientsInput) ([]SectionRecipient, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, it := range in.Items {
		if !IsDepartmentSection(it.Section) || seen[it.Section] {
			return nil, invalid("recipients", fieldErr("items", "INVALID_VALUE", "each of the six sections of a department, once"))
		}
		seen[it.Section] = true
	}
	var out []SectionRecipient
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		before, err := s.recipients(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		for _, it := range in.Items {
			if it.DepartmentID == nil {
				err = q.DeleteRecipient(ctx, banquetdb.DeleteRecipientParams{PropertyID: propertyID, Section: it.Section})
			} else {
				err = q.UpsertRecipient(ctx, banquetdb.UpsertRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, Section: it.Section, DepartmentID: *it.DepartmentID, ActorID: p.ActorID()})
			}
			if err != nil {
				return err
			}
		}
		out, err = s.recipients(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.beo_recipients_changed", "banquet_beo_recipients", propertyID, "", before, out))
	})
	return out, err
}

// ---------------------------------------------------------------- the documents

// LiveSnapshot is the document of an event as it is now: what the PDF of a proposal and of a draft BEO is rendered from (banquet.read).
func (s *Service) LiveSnapshot(ctx context.Context, propertyID, eventID int64) (Snapshot, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, p.TenantID, propertyID, eventID)
}

// BeoSnapshot is the BEO to print: the one of a revision when it is asked for, else the last revision of a definite event, else the draft as it is now. It answers the revision number (0 for a draft).
func (s *Service) BeoSnapshot(ctx context.Context, propertyID, eventID int64, revision *int) (Snapshot, int, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return Snapshot{}, 0, err
	}
	if revision != nil {
		snap, _, err := s.revisionSnapshot(ctx, p.TenantID, propertyID, eventID, *revision)
		return snap, *revision, err
	}
	snap, err := s.snapshot(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return Snapshot{}, 0, err
	}
	last, err := s.q(ctx).LastRevision(ctx, banquetdb.LastRevisionParams{PropertyID: propertyID, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return snap, 0, nil
	}
	return snap, int(last.RevisionNo), err
}

func documentRow(id int64, kind string, number *string, rev *int32, at time.Time, by string) DocumentRow {
	d := DocumentRow{ID: id, Kind: kind, Number: number, CreatedAt: at, CreatedByName: by}
	if rev != nil {
		d.RevisionNo = ptr(int(*rev))
	}
	return d
}

// ListDocuments answers the proposals that were sent and the contracts that were issued for an event.
func (s *Service) ListDocuments(ctx context.Context, propertyID, eventID int64) ([]DocumentRow, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return nil, err
	}
	if _, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID}); err != nil {
		return nil, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	rows, err := s.q(ctx).ListDocuments(ctx, banquetdb.ListDocumentsParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	out := make([]DocumentRow, len(rows))
	for i, r := range rows {
		out[i] = documentRow(r.ID, r.Kind, r.Number, r.RevisionNo, r.CreatedAt, r.CreatedByName)
	}
	return out, nil
}

// IssueContract numbers the contract of the revision in force (banquet.confirm): the contract is final for the revision it carries, so asking again for the same revision gives the same number, and a
// later revision needs a new contract. Only a definite event has one.
func (s *Service) IssueContract(ctx context.Context, propertyID, eventID int64) (DocumentRow, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetConfirm)
	if err != nil {
		return DocumentRow{}, err
	}
	var out DocumentRow
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "only a definite event has a contract").WithContext("status", hdr.Status)
		}
		rev, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{})
		if err != nil {
			return err
		}
		q := s.q(ctx)
		rn := int32(rev) //nolint:gosec // G115: a revision number
		if ex, err := q.ContractOfRevision(ctx, banquetdb.ContractOfRevisionParams{PropertyID: propertyID, EventID: eventID, RevisionNo: &rn}); err == nil {
			out = documentRow(ex.ID, DocContract, ex.Number, ex.RevisionNo, ex.CreatedAt, "")
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqBanquetContract)
		if err != nil {
			return err
		}
		row, err := q.InsertDocument(ctx, banquetdb.InsertDocumentParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, Kind: DocContract, Number: &number, RevisionNo: &rn, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = documentRow(row.ID, DocContract, &number, &rn, row.CreatedAt, "")
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.contract_issued", "banquet_document", row.ID, hdr.Number+" / "+number, nil, map[string]any{"number": number, "revision_no": rev}))
	})
	return out, err
}

// RecordProposal keeps the proposal as it is now, with its snapshot, so that the e-mail that carries it sends what the person saw (banquet.manage). A closed event has none.
func (s *Service) RecordProposal(ctx context.Context, propertyID, eventID int64) (DocumentRow, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return DocumentRow{}, err
	}
	var out DocumentRow
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event has no proposal").WithContext("status", hdr.Status)
		}
		snap, err := s.snapshot(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		sj, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).InsertDocument(ctx, banquetdb.InsertDocumentParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, Kind: DocProposal, Snapshot: sj, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = documentRow(row.ID, DocProposal, nil, nil, row.CreatedAt, "")
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.proposal_made", "banquet_document", row.ID, hdr.Number, nil, map[string]any{"status": hdr.Status}))
	})
	return out, err
}

// DocumentFileOf answers what a recorded document is rendered from (banquet.read): the snapshot of the revision of a contract, or the one a proposal kept.
func (s *Service) DocumentFileOf(ctx context.Context, propertyID, id int64) (DocumentFile, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return DocumentFile{}, err
	}
	q := s.q(ctx)
	d, err := q.GetDocument(ctx, banquetdb.GetDocumentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return DocumentFile{}, orNotFound(err, notFound("DOCUMENT_NOT_FOUND", "document"))
	}
	out := DocumentFile{ID: d.ID, EventID: d.EventID, Kind: d.Kind, Number: d.Number, CreatedAt: d.CreatedAt}
	raw := d.Snapshot
	if d.RevisionNo != nil {
		out.RevisionNo = ptr(int(*d.RevisionNo))
		r, err := q.GetRevision(ctx, banquetdb.GetRevisionParams{PropertyID: propertyID, EventID: d.EventID, RevisionNo: *d.RevisionNo})
		if err != nil {
			return DocumentFile{}, err
		}
		raw = r.Snapshot
	}
	out.Snapshot, err = decodeSnapshot(raw)
	return out, err
}

// LatestContract answers the contract of the revision in force of an event, the one a person prints, or 404 when none was issued for it.
func (s *Service) LatestContract(ctx context.Context, propertyID, eventID int64) (DocumentRow, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return DocumentRow{}, err
	}
	q := s.q(ctx)
	last, err := q.LastRevision(ctx, banquetdb.LastRevisionParams{PropertyID: propertyID, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return DocumentRow{}, notFound("CONTRACT_NOT_ISSUED", "contract")
	}
	if err != nil {
		return DocumentRow{}, err
	}
	rn := last.RevisionNo
	ex, err := q.ContractOfRevision(ctx, banquetdb.ContractOfRevisionParams{PropertyID: propertyID, EventID: eventID, RevisionNo: &rn})
	if err != nil {
		return DocumentRow{}, orNotFound(err, notFound("CONTRACT_NOT_ISSUED", "contract"))
	}
	_ = p
	return documentRow(ex.ID, DocContract, ex.Number, ex.RevisionNo, ex.CreatedAt, ""), nil
}

// ---------------------------------------------------------------- completing an event

// CompleteEvent closes a definite event whose sessions are all posted or cancelled (banquet.manage, 36 4.5): the status becomes COMPLETED. It does not close the folio: that is the close that exists,
// with the balance at zero.
func (s *Service) CompleteEvent(ctx context.Context, propertyID, eventID int64) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "only a definite event can be completed").WithContext("status", hdr.Status)
		}
		q := s.q(ctx)
		n, err := q.UnpostedActiveSessions(ctx, banquetdb.UnpostedActiveSessionsParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("SESSIONS_NOT_POSTED", "every active session must be posted or cancelled first").WithContext("sessions", n)
		}
		now := s.clock.Now()
		if err := q.SetEventStatus(ctx, banquetdb.SetEventStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, Status: StatusCompleted, ClosedAt: &now, ActorID: p.ActorID()}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_completed", "banquet_event", eventID, out.Number, map[string]any{"status": StatusDefinite}, map[string]any{"status": StatusCompleted}))
	})
	return out, err
}
