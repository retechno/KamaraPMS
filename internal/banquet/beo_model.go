package banquet

import (
	"slices"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// The BEO, its revisions and its documents (36 3.4, 4.6): the pure rules, with no database.

// The sections of the BEO of a session. The six that belong to a department are told when they change (Q8); GENERAL is the set-up, the menu and the notes everybody reads, and has no department.
const (
	SectionKitchen      = "KITCHEN"
	SectionFBService    = "FB_SERVICE"
	SectionHousekeeping = "HOUSEKEEPING"
	SectionEngineering  = "ENGINEERING"
	SectionSecurity     = "SECURITY"
	SectionFrontOffice  = "FRONT_OFFICE"
	SectionGeneral      = "GENERAL"
)

// Sections is every section of a BEO in the order it is printed.
var Sections = []string{SectionGeneral, SectionKitchen, SectionFBService, SectionHousekeeping, SectionEngineering, SectionSecurity, SectionFrontOffice}

// DepartmentSections are the sections that a department of the hotel reads, and so the ones that have a recipient.
var DepartmentSections = []string{SectionKitchen, SectionFBService, SectionHousekeeping, SectionEngineering, SectionSecurity, SectionFrontOffice}

// IsSection tells whether s is a section of the BEO.
func IsSection(s string) bool { return slices.Contains(Sections, s) }

// IsDepartmentSection tells whether s is a section that has a department.
func IsDepartmentSection(s string) bool { return slices.Contains(DepartmentSections, s) }

// The kinds of a notice added by PR 3, and of the documents.
const (
	NoticeBeoRevised  = "BEO_REVISED"
	NoticeFeeProposed = "FEE_PROPOSED"

	DocProposal = "PROPOSAL"
	DocContract = "CONTRACT"
)

// The kinds of a change in the history of a BEO.
const (
	ChangeBaseline         = "baseline"
	ChangeConfirmed        = "confirmed"
	ChangeSessionAdded     = "session_added"
	ChangeSessionCancelled = "session_cancelled"
	ChangeSessionField     = "session_field"
	ChangeSection          = "section"
	ChangeRunOfShow        = "run_of_show"
	ChangeLineAdded        = "line_added"
	ChangeLineRemoved      = "line_removed"
	ChangeLineChanged      = "line_changed"
	ChangeDeposits         = "deposit_schedule"
	ChangePolicy           = "policy"
)

// RunLine is a line of the programme of a session.
type RunLine struct {
	Seq  int             `json:"seq"`
	At   civil.TimeOfDay `json:"at_time"`
	Text string          `json:"text"`
}

// SnapEvent is the header of an event as a document shows it.
type SnapEvent struct {
	Number       string `json:"number"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	EventType    string `json:"event_type"`
	Booker       string `json:"booker"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	ContactEmail string `json:"contact_email"`
	SalesName    string `json:"sales_name"`
	GroupName    string `json:"group_name"`
	Notes        string `json:"notes"`
	OptionUntil  string `json:"option_until"`
}

// SnapSession is a session with its BEO.
type SnapSession struct {
	ID            int64             `json:"id"`
	No            int               `json:"no"`
	Name          string            `json:"name"`
	Date          civil.Date        `json:"date"`
	Start         *civil.TimeOfDay  `json:"start"`
	End           *civil.TimeOfDay  `json:"end"`
	EndsNextDay   bool              `json:"ends_next_day"`
	Room          string            `json:"room"`
	Setup         string            `json:"setup"`
	PaxEstimated  int               `json:"pax_estimated"`
	PaxGuaranteed *int              `json:"pax_guaranteed"`
	Status        string            `json:"status"`
	Notes         string            `json:"notes"`
	Sections      map[string]string `json:"sections"`
	RunOfShow     []RunLine         `json:"run_of_show"`
}

// SnapDeposit is a row of the deposit schedule.
type SnapDeposit struct {
	Label   string           `json:"label"`
	DueDate civil.Date       `json:"due_date"`
	Percent *decimal.Decimal `json:"percent"`
	Amount  decimal.Decimal  `json:"amount"`
}

// SnapPolicy is the cancellation policy of an event.
type SnapPolicy struct {
	Base  string `json:"base"`
	Tiers []Tier `json:"tiers"`
}

// Snapshot is the whole document of an event at one moment: what a revision keeps, what a proposal that was sent keeps, and what the PDFs are rendered from. It holds no clock and nothing that
// differs between two reads of the same data, so two snapshots of the same state are equal.
type Snapshot struct {
	Version  int           `json:"version"`
	Event    SnapEvent     `json:"event"`
	Sessions []SnapSession `json:"sessions"`
	Lines    []Line        `json:"lines"`
	Estimate Estimate      `json:"estimate"`
	Deposits []SnapDeposit `json:"deposits"`
	Policy   SnapPolicy    `json:"policy"`
}

// SnapshotVersion is the shape of the snapshot; a reader that meets a newer one refuses it.
const SnapshotVersion = 1

// Change is one thing a save changed, kept in the history of the revisions. The screen writes the sentence from the kind and the values, in the language of the reader.
type Change struct {
	Kind      string `json:"kind"`
	SessionID int64  `json:"session_id,omitempty"`
	SessionNo int    `json:"session_no,omitempty"`
	Field     string `json:"field,omitempty"`
	Section   string `json:"section,omitempty"`
	Old       string `json:"old,omitempty"`
	New       string `json:"new,omitempty"`
	Text      string `json:"text,omitempty"`
}

// The data of a session that every department shares: when the section of a department is "changed since" a revision, a change of one of these counts even if its own text did not move (36 4.6).
// The set-up is one of them because the room is laid out for it.
var sharedFields = []string{"date", "start", "end", "room", "setup", "pax_estimated", "pax_guaranteed"}

func timeText(t *civil.TimeOfDay) string {
	if t == nil {
		return ""
	}
	return t.String()
}

func intText(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

// sharedValue is the text of a shared field of a session, the way it is compared and shown.
func sharedValue(sn SnapSession, field string) string {
	switch field {
	case "date":
		return sn.Date.String()
	case "start":
		return timeText(sn.Start)
	case "end":
		if sn.EndsNextDay {
			return timeText(sn.End) + "+1"
		}
		return timeText(sn.End)
	case "room":
		return sn.Room
	case "setup":
		return sn.Setup
	case "pax_estimated":
		return strconv.Itoa(sn.PaxEstimated)
	case "pax_guaranteed":
		return intText(sn.PaxGuaranteed)
	}
	return ""
}

func runText(rs []RunLine) string {
	var b []byte
	for _, r := range rs {
		b = append(b, r.At.String()...)
		b = append(b, ' ')
		b = append(b, r.Text...)
		b = append(b, '\n')
	}
	return string(b)
}

func sessionByID(s Snapshot, id int64) (SnapSession, bool) {
	for _, x := range s.Sessions {
		if x.ID == id {
			return x, true
		}
	}
	return SnapSession{}, false
}

// SectionChangedSince tells whether the section of a session is not what it was in the earlier snapshot: its own text, or a datum the departments share. A session that did not exist then counts as
// changed.
func SectionChangedSince(then, now Snapshot, sessionID int64, section string) bool {
	a, okA := sessionByID(then, sessionID)
	b, okB := sessionByID(now, sessionID)
	if !okA || !okB {
		return okA != okB
	}
	if a.Status != b.Status || a.Sections[section] != b.Sections[section] {
		return true
	}
	for _, f := range sharedFields {
		if sharedValue(a, f) != sharedValue(b, f) {
			return true
		}
	}
	return false
}

func lineSame(a, b Line) bool {
	eq := func(x, y *decimal.Decimal) bool { return (x == nil) == (y == nil) && (x == nil || x.Equal(*y)) }
	return a.Description == b.Description && a.Quantity.Equal(b.Quantity) && a.UnitPrice.Equal(b.UnitPrice) && a.PriceMode == b.PriceMode && a.SessionID == b.SessionID &&
		eq(a.DiscountPct, b.DiscountPct) && eq(a.DiscountAmount, b.DiscountAmount)
}

func depositsSame(a, b []SnapDeposit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Label != y.Label || !x.DueDate.Equal(y.DueDate) || !x.Amount.Equal(y.Amount) || (x.Percent == nil) != (y.Percent == nil) || (x.Percent != nil && !x.Percent.Equal(*y.Percent)) {
			return false
		}
	}
	return true
}

func policySame(a, b SnapPolicy) bool {
	if a.Base != b.Base || len(a.Tiers) != len(b.Tiers) {
		return false
	}
	for i := range a.Tiers {
		x, y := a.Tiers[i], b.Tiers[i]
		if x.DaysFrom != y.DaysFrom || (x.DaysTo == nil) != (y.DaysTo == nil) || (x.DaysTo != nil && *x.DaysTo != *y.DaysTo) || !x.Percent.Equal(y.Percent) || x.Action != y.Action {
			return false
		}
	}
	return true
}

// Flags are the sections of the sessions that a revision tells somebody about: the session, then the department sections whose text or shared data changed.
type Flags map[int64]map[string]bool

func (f Flags) add(sessionID int64, section string) {
	if !IsDepartmentSection(section) {
		return
	}
	if f[sessionID] == nil {
		f[sessionID] = map[string]bool{}
	}
	f[sessionID][section] = true
}

func (f Flags) all(sessionID int64) {
	for _, sec := range DepartmentSections {
		f.add(sessionID, sec)
	}
}

// Sections lists the department sections flagged in any session, in the printed order.
func (f Flags) Sections() []string {
	var out []string
	for _, sec := range DepartmentSections {
		for _, m := range f {
			if m[sec] {
				out = append(out, sec)
				break
			}
		}
	}
	return out
}

// DiffSnapshots says what a save changed against the revision before it (nil: there is none, the BEO starts here) and which department sections have to be told. No change at all means no revision
// (Q10: a save that changes nothing is not a revision).
func DiffSnapshots(old *Snapshot, now Snapshot) ([]Change, Flags) {
	flags := Flags{}
	if old == nil {
		for _, sn := range now.Sessions {
			if sn.Status == "ACTIVE" {
				flags.all(sn.ID)
			}
		}
		return []Change{{Kind: ChangeBaseline}}, flags
	}
	var out []Change
	for _, sn := range now.Sessions {
		was, ok := sessionByID(*old, sn.ID)
		switch {
		case !ok:
			if sn.Status == "ACTIVE" {
				out = append(out, Change{Kind: ChangeSessionAdded, SessionID: sn.ID, SessionNo: sn.No, Text: sn.Name})
				flags.all(sn.ID)
			}
			continue
		case was.Status == "ACTIVE" && sn.Status != "ACTIVE":
			out = append(out, Change{Kind: ChangeSessionCancelled, SessionID: sn.ID, SessionNo: sn.No, Text: sn.Name})
			flags.all(sn.ID)
			continue
		case sn.Status != "ACTIVE":
			continue
		}
		if was.Name != sn.Name {
			out = append(out, Change{Kind: ChangeSessionField, SessionID: sn.ID, SessionNo: sn.No, Field: "name", Old: was.Name, New: sn.Name})
		}
		for _, f := range sharedFields {
			if a, b := sharedValue(was, f), sharedValue(sn, f); a != b {
				out = append(out, Change{Kind: ChangeSessionField, SessionID: sn.ID, SessionNo: sn.No, Field: f, Old: a, New: b})
				flags.all(sn.ID)
			}
		}
		for _, sec := range Sections {
			if was.Sections[sec] != sn.Sections[sec] {
				out = append(out, Change{Kind: ChangeSection, SessionID: sn.ID, SessionNo: sn.No, Section: sec})
				flags.add(sn.ID, sec)
			}
		}
		if runText(was.RunOfShow) != runText(sn.RunOfShow) {
			out = append(out, Change{Kind: ChangeRunOfShow, SessionID: sn.ID, SessionNo: sn.No})
		}
	}
	for _, l := range now.Lines {
		var was *Line
		for i := range old.Lines {
			if old.Lines[i].ID == l.ID {
				was = &old.Lines[i]
			}
		}
		switch {
		case was == nil:
			out = append(out, Change{Kind: ChangeLineAdded, SessionID: l.SessionID, Text: l.Description})
		case !lineSame(*was, l):
			out = append(out, Change{Kind: ChangeLineChanged, SessionID: l.SessionID, Text: l.Description})
		}
	}
	for _, l := range old.Lines {
		found := false
		for _, n := range now.Lines {
			found = found || n.ID == l.ID
		}
		if !found {
			out = append(out, Change{Kind: ChangeLineRemoved, SessionID: l.SessionID, Text: l.Description})
		}
	}
	if !depositsSame(old.Deposits, now.Deposits) {
		out = append(out, Change{Kind: ChangeDeposits})
	}
	if !policySame(old.Policy, now.Policy) {
		out = append(out, Change{Kind: ChangePolicy})
	}
	return out, flags
}

// SectionState is where a section stands for a reader: not read yet, read and unchanged since, or changed since the revision that was read.
const (
	StateUnread  = "UNREAD"
	StateRead    = "READ"
	StateChanged = "CHANGED"
	StateDraft   = "DRAFT"
)
