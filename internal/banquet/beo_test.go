package banquet_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kamarapms/internal/banquet"
	"kamarapms/internal/platform/auth"
)

// The BEO, its revisions, who is told, the contract and the completion of an event (docs/architecture/36-banquet.md 4.6, 4.5, PR 3).

func (f *fx) userID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	must(t, err)
	return p.UserID
}

// department makes a department with a head and a deputy (0: none).
func (f *fx) department(t *testing.T, code string, head, deputy int64) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(),
		`INSERT INTO departments (tenant_id, property_id, code, name, head_user_id, deputy_user_id) VALUES ($1, $2, $3, $3, NULLIF($4, 0), NULLIF($5, 0)) RETURNING id`,
		f.tenantID, f.propID, code, head, deputy).Scan(&id))
	return id
}

func (f *fx) mapSection(t *testing.T, section string, departmentID int64) {
	t.Helper()
	var dep *int64
	if departmentID != 0 {
		dep = &departmentID
	}
	_, err := f.svc.PutRecipients(f.mgr, f.propID, banquet.RecipientsInput{Items: []banquet.RecipientItem{{Section: section, DepartmentID: dep}}})
	must(t, err)
}

func (f *fx) saveText(t *testing.T, ctx context.Context, eventID, sessionID int64, section, text string) banquet.BeoView {
	t.Helper()
	v, err := f.svc.SaveBeo(ctx, f.propID, eventID, sessionID, banquet.BeoInput{Sections: map[string]string{section: text}})
	must(t, err)
	return v
}

func (f *fx) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	must(t, f.Pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

// unseen counts the BEO notices of the caller that they have not seen.
func (f *fx) unseen(t *testing.T, ctx context.Context) int {
	t.Helper()
	list, err := f.svc.ListNotices(ctx, f.propID, true)
	must(t, err)
	n := 0
	for _, x := range list {
		if x.Kind == banquet.NoticeBeoRevised {
			n++
		}
	}
	return n
}

func (f *fx) seen(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := f.svc.MarkNoticesSeen(ctx, f.propID, nil)
	must(t, err)
}

func sectionOf(t *testing.T, v banquet.BeoView, sessionID int64, name string) banquet.BeoSection {
	t.Helper()
	for _, s := range v.Sessions {
		if s.ID != sessionID {
			continue
		}
		for _, x := range s.Sections {
			if x.Section == name {
				return x
			}
		}
	}
	t.Fatalf("no section %s in session %d", name, sessionID)
	return banquet.BeoSection{}
}

func (f *fx) moveSession(t *testing.T, eventID, sessionID int64, pax int) {
	t.Helper()
	in := f.session(f.a.ID, "2026-11-07", "12:00", "16:00", false)
	in.PaxEstimated = pax
	_, err := f.svc.UpdateSession(f.office, f.propID, eventID, sessionID, in)
	must(t, err)
}

func TestTheBeoIsADraftBeforeDefiniteAndKeepsNoRevision(t *testing.T) {
	f := setup(t)
	ev := f.tentative(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	v := f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "No nuts")
	if !v.Draft || v.Revision != 0 || len(v.Revisions) != 0 {
		t.Fatalf("a tentative event has a draft: %+v", v)
	}
	if got := sectionOf(t, v, sid, banquet.SectionKitchen); got.Body != "No nuts" || got.State != banquet.StateDraft {
		t.Fatalf("section: %+v", got)
	}
	if n := f.count(t, `SELECT count(*) FROM banquet_beo_revisions WHERE property_id = $1`, f.propID); n != 0 {
		t.Fatalf("revisions of a draft: %d", n)
	}
	// nobody reads a draft: there is no revision to mark
	_, err := f.svc.AckBeo(f.reader, f.propID, ev.ID, banquet.AckInput{SessionID: sid, Section: banquet.SectionKitchen})
	_ = code(t, err, "BEO_IS_DRAFT")
}

func TestEverySaveAfterDefiniteIsOneRevisionAndTheSnapshotReproducesIt(t *testing.T) {
	f := setup(t)
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	v, err := f.svc.Beo(f.reader, f.propID, ev.ID)
	must(t, err)
	if v.Draft || v.Revision != 1 || len(v.Revisions) != 1 {
		t.Fatalf("confirming the event is revision 1: %+v", v)
	}

	v = f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")
	if v.Revision != 2 {
		t.Fatalf("a save is a revision: %d", v.Revision)
	}
	// saving what is already there is no revision
	v = f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")
	if v.Revision != 2 || len(v.Revisions) != 2 {
		t.Fatalf("a save with no change made a revision: %+v", v.Revisions)
	}
	f.moveSession(t, ev.ID, sid, 120)
	v, err = f.svc.Beo(f.reader, f.propID, ev.ID)
	must(t, err)
	if v.Revision != 3 {
		t.Fatalf("a change of the guests is a revision: %d", v.Revision)
	}

	r1, err := f.svc.RevisionOf(f.reader, f.propID, ev.ID, 1)
	must(t, err)
	r2, err := f.svc.RevisionOf(f.reader, f.propID, ev.ID, 2)
	must(t, err)
	r3, err := f.svc.RevisionOf(f.reader, f.propID, ev.ID, 3)
	must(t, err)
	if r1.Snapshot.Sessions[0].Sections[banquet.SectionKitchen] != "" || r2.Snapshot.Sessions[0].Sections[banquet.SectionKitchen] != "Halal only" {
		t.Fatalf("the texts of the revisions: %q, %q", r1.Snapshot.Sessions[0].Sections[banquet.SectionKitchen], r2.Snapshot.Sessions[0].Sections[banquet.SectionKitchen])
	}
	if r2.Snapshot.Sessions[0].PaxEstimated != 100 || r3.Snapshot.Sessions[0].PaxEstimated != 120 {
		t.Fatalf("the guests of the revisions: %d, %d", r2.Snapshot.Sessions[0].PaxEstimated, r3.Snapshot.Sessions[0].PaxEstimated)
	}
	// the BEO of a revision as a PDF is drawn from the same snapshot
	snap, rev, err := f.svc.BeoSnapshot(f.reader, f.propID, ev.ID, ptr(2))
	must(t, err)
	if rev != 2 || snap.Sessions[0].Sections[banquet.SectionKitchen] != "Halal only" {
		t.Fatalf("snapshot of revision 2: %d %+v", rev, snap.Sessions[0].Sections)
	}
	_, err = f.svc.RevisionOf(f.reader, f.propID, ev.ID, 99)
	_ = code(t, err, "REVISION_NOT_FOUND")
}

func TestASectionIsChangedSinceTheRevisionThatWasRead(t *testing.T) {
	f := setup(t)
	head := f.User(t, f.tenantID, f.propID, auth.PermBanquetRead)
	f.mapSection(t, banquet.SectionKitchen, f.department(t, "KIT", f.userID(t, head), 0))
	f.mapSection(t, banquet.SectionFBService, f.department(t, "FBS", f.userID(t, head), 0))
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")

	v, err := f.svc.AckBeo(head, f.propID, ev.ID, banquet.AckInput{SessionID: sid, Section: banquet.SectionKitchen})
	must(t, err)
	k := sectionOf(t, v, sid, banquet.SectionKitchen)
	if k.State != banquet.StateRead || k.ReadRevision == nil || *k.ReadRevision != 2 {
		t.Fatalf("kitchen after the mark: %+v", k)
	}
	if fb := sectionOf(t, v, sid, banquet.SectionFBService); fb.State != banquet.StateUnread {
		t.Fatalf("a section nobody marked is unread: %+v", fb)
	}

	// the text of another department does not touch the kitchen
	v = f.saveText(t, f.office, ev.ID, sid, banquet.SectionFBService, "Buffet")
	if k = sectionOf(t, v, sid, banquet.SectionKitchen); k.State != banquet.StateRead {
		t.Fatalf("a change of another section marked the kitchen: %+v", k)
	}
	// but the data every department shares does
	f.moveSession(t, ev.ID, sid, 120)
	v, err = f.svc.Beo(head, f.propID, ev.ID)
	must(t, err)
	k = sectionOf(t, v, sid, banquet.SectionKitchen)
	if k.State != banquet.StateChanged || k.ChangedSince == nil || *k.ChangedSince != 2 {
		t.Fatalf("kitchen after the change of the guests: %+v", k)
	}
	// reading it again at the last revision clears it
	v, err = f.svc.AckBeo(head, f.propID, ev.ID, banquet.AckInput{SessionID: sid, Section: banquet.SectionKitchen})
	must(t, err)
	if k = sectionOf(t, v, sid, banquet.SectionKitchen); k.State != banquet.StateRead || *k.ReadRevision != v.Revision {
		t.Fatalf("kitchen after the second mark: %+v", k)
	}
}

func TestTheHeadAndTheDeputyAreToldOnceWhileTheyHaveNotSeenTheLastNotice(t *testing.T) {
	f := setup(t)
	head := f.User(t, f.tenantID, f.propID, auth.PermBanquetRead, auth.PermBanquetManage)
	deputy := f.User(t, f.tenantID, f.propID, auth.PermBanquetRead)
	f.mapSection(t, banquet.SectionKitchen, f.department(t, "KIT", f.userID(t, head), f.userID(t, deputy)))
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	// the revision of the confirmation tells the departments that the BEO exists
	if f.unseen(t, head) != 1 || f.unseen(t, deputy) != 1 {
		t.Fatalf("the confirmation told the head and the deputy once")
	}
	f.seen(t, head)
	f.seen(t, deputy)

	v := f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")
	if f.unseen(t, head) != 1 || f.unseen(t, deputy) != 1 {
		t.Fatalf("the head and the deputy are told once")
	}
	if last := v.Revisions[0]; last.Notified != 2 {
		t.Fatalf("the revision says how many were told: %+v", last)
	}
	// a second revision while the notice is unseen brings the notice up to date
	v = f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only, no pork")
	list, err := f.svc.ListNotices(head, f.propID, true)
	must(t, err)
	if len(list) != 1 || list[0].RevisionNo == nil || *list[0].RevisionNo != v.Revision {
		t.Fatalf("the notice was not coalesced: %+v", list)
	}
	// once seen, the next revision tells again
	f.seen(t, head)
	f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only, no pork, no shellfish")
	if f.unseen(t, head) != 1 {
		t.Fatalf("a notice that was seen is followed by a new one")
	}
	f.seen(t, head)
	// the person who made the revision is not told of it
	f.saveText(t, head, ev.ID, sid, banquet.SectionKitchen, "Halal only, nothing else")
	if f.unseen(t, head) != 0 {
		t.Fatalf("the actor was told of their own revision")
	}
	if f.unseen(t, deputy) != 1 {
		t.Fatalf("the deputy never saw a notice, so one is waiting")
	}
}

func TestASectionWithNoDepartmentTellsNobodyAndSaysSo(t *testing.T) {
	f := setup(t)
	head := f.User(t, f.tenantID, f.propID, auth.PermBanquetRead)
	f.mapSection(t, banquet.SectionKitchen, f.department(t, "KIT", f.userID(t, head), 0))
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID

	f.seen(t, head) // the confirmation told the kitchen that the BEO exists
	v := f.saveText(t, f.office, ev.ID, sid, banquet.SectionSecurity, "Two guards at the door")
	if x := sectionOf(t, v, sid, banquet.SectionSecurity); !x.NoDepartment {
		t.Fatalf("the screen must say that nobody is told: %+v", x)
	}
	if v.Revisions[0].Notified != 0 || f.unseen(t, head) != 0 {
		t.Fatalf("a section with no department told someone: %+v", v.Revisions[0])
	}
	// a department with a head nobody named tells nobody either
	f.mapSection(t, banquet.SectionEngineering, f.department(t, "ENG", 0, 0))
	v = f.saveText(t, f.office, ev.ID, sid, banquet.SectionEngineering, "Extra power at the stage")
	if v.Revisions[0].Notified != 0 {
		t.Fatalf("a department with no head told someone: %+v", v.Revisions[0])
	}
	// taking the department off a section is a change of the settings, not of the event
	f.mapSection(t, banquet.SectionKitchen, 0)
	rec, err := f.svc.ListRecipients(f.reader, f.propID)
	must(t, err)
	for _, r := range rec {
		if r.Section == banquet.SectionKitchen && r.DepartmentID != nil {
			t.Fatalf("the department stayed: %+v", r)
		}
	}
}

func TestARolledBackSaveLeavesNoRevisionAndNoNotice(t *testing.T) {
	f := setup(t)
	head := f.User(t, f.tenantID, f.propID, auth.PermBanquetRead)
	f.mapSection(t, banquet.SectionKitchen, f.department(t, "KIT", f.userID(t, head), 0))
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID

	f.seen(t, head) // the confirmation told the kitchen that the BEO exists
	err := f.TxM.WithinTx(f.office, func(ctx context.Context) error {
		f.saveText(t, ctx, ev.ID, sid, banquet.SectionKitchen, "Halal only")
		return errors.New("something later in the same use case failed")
	})
	if err == nil {
		t.Fatal("the transaction should have failed")
	}
	if n := f.count(t, `SELECT count(*) FROM banquet_beo_revisions WHERE property_id = $1`, f.propID); n != 1 {
		t.Fatalf("revisions after the rollback: %d (only the one of the confirmation)", n)
	}
	if f.unseen(t, head) != 0 {
		t.Fatalf("a rollback left a notice")
	}
	if n := f.count(t, `SELECT count(*) FROM banquet_beo_sections WHERE property_id = $1`, f.propID); n != 0 {
		t.Fatalf("a rollback left a text: %d", n)
	}
}

func TestTheRunOfTheDayIsPartOfTheBeoAndOfItsRevision(t *testing.T) {
	f := setup(t)
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	v, err := f.svc.SaveBeo(f.office, f.propID, ev.ID, sid, banquet.BeoInput{RunOfShow: []banquet.RunLineInput{{At: *tod("12:00"), Text: "Guests arrive"}, {At: *tod("13:00"), Text: "Lunch"}}})
	must(t, err)
	if v.Revision != 2 || len(v.Sessions[0].RunOfShow) != 2 {
		t.Fatalf("run of the day: %+v", v.Sessions[0].RunOfShow)
	}
	r, err := f.svc.RevisionOf(f.reader, f.propID, ev.ID, 2)
	must(t, err)
	if len(r.Snapshot.Sessions[0].RunOfShow) != 2 || r.Snapshot.Sessions[0].RunOfShow[1].Text != "Lunch" {
		t.Fatalf("the snapshot lost the run of the day: %+v", r.Snapshot.Sessions[0].RunOfShow)
	}
	// an empty list clears it
	v, err = f.svc.SaveBeo(f.office, f.propID, ev.ID, sid, banquet.BeoInput{RunOfShow: []banquet.RunLineInput{}})
	must(t, err)
	if len(v.Sessions[0].RunOfShow) != 0 || v.Revision != 3 {
		t.Fatalf("clearing the run: %+v", v)
	}
}

func TestTheBeoAndItsRecipientsAreGuardedByPermission(t *testing.T) {
	f := setup(t)
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	_, err := f.svc.SaveBeo(f.reader, f.propID, ev.ID, sid, banquet.BeoInput{Sections: map[string]string{banquet.SectionKitchen: "x"}})
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.Beo(f.nobody, f.propID, ev.ID)
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.PutRecipients(f.office, f.propID, banquet.RecipientsInput{})
	_ = code(t, err, "PERMISSION_DENIED")
	if _, err = f.svc.Beo(f.reader, f.propID, ev.ID); err != nil {
		t.Fatal(err)
	}
	// an unknown section and a text that is too long are refused
	_, err = f.svc.SaveBeo(f.office, f.propID, ev.ID, sid, banquet.BeoInput{Sections: map[string]string{"BAR": "x"}})
	if err == nil {
		t.Fatal("an unknown section was accepted")
	}
	_, err = f.svc.SaveBeo(f.office, f.propID, ev.ID, sid, banquet.BeoInput{Sections: map[string]string{banquet.SectionKitchen: strings.Repeat("x", 4001)}})
	if err == nil {
		t.Fatal("a text of more than 4000 characters was accepted")
	}
}

func TestTheContractIsNumberedOncePerRevision(t *testing.T) {
	f := setup(t)
	tent := f.tentative(t, "Early", f.b.ID, "2026-11-08", "12:00", "16:00")
	_, err := f.svc.IssueContract(f.mgr, f.propID, tent.ID)
	if err == nil {
		t.Fatal("a tentative event has no contract")
	}
	ev := f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	_, err = f.svc.IssueContract(f.office, f.propID, ev.ID)
	_ = code(t, err, "PERMISSION_DENIED") // banquet.confirm

	c1, err := f.svc.IssueContract(f.mgr, f.propID, ev.ID)
	must(t, err)
	again, err := f.svc.IssueContract(f.mgr, f.propID, ev.ID)
	must(t, err)
	if c1.Number == nil || !strings.HasPrefix(*c1.Number, "BQC-") || again.ID != c1.ID || *again.Number != *c1.Number {
		t.Fatalf("the same revision gives the same contract: %+v %+v", c1, again)
	}
	if c1.RevisionNo == nil || *c1.RevisionNo != 1 {
		t.Fatalf("revision of the contract: %+v", c1)
	}
	// a later revision needs a new contract, and the first one still reads the revision it carried
	f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")
	c2, err := f.svc.IssueContract(f.mgr, f.propID, ev.ID)
	must(t, err)
	if c2.ID == c1.ID || *c2.Number == *c1.Number || *c2.RevisionNo != 2 {
		t.Fatalf("a new revision, a new contract: %+v", c2)
	}
	file, err := f.svc.DocumentFileOf(f.reader, f.propID, c1.ID)
	must(t, err)
	if file.Kind != banquet.DocContract || file.Snapshot.Sessions[0].Sections[banquet.SectionKitchen] != "" {
		t.Fatalf("the first contract must read its own revision: %+v", file.Snapshot.Sessions[0].Sections)
	}
	list, err := f.svc.ListDocuments(f.reader, f.propID, ev.ID)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("documents: %+v", list)
	}
	latest, err := f.svc.LatestContract(f.reader, f.propID, ev.ID)
	must(t, err)
	if latest.ID != c2.ID {
		t.Fatalf("the contract in force: %+v", latest)
	}
}

func TestAProposalKeepsWhatWasSent(t *testing.T) {
	f := setup(t)
	ev := f.tentative(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	sid := ev.Sessions[0].ID
	f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Halal only")
	p, err := f.svc.RecordProposal(f.office, f.propID, ev.ID)
	must(t, err)
	if p.Kind != banquet.DocProposal || p.Number != nil || p.RevisionNo != nil {
		t.Fatalf("a proposal has no number and no revision: %+v", p)
	}
	f.saveText(t, f.office, ev.ID, sid, banquet.SectionKitchen, "Vegetarian")
	file, err := f.svc.DocumentFileOf(f.reader, f.propID, p.ID)
	must(t, err)
	if file.Snapshot.Sessions[0].Sections[banquet.SectionKitchen] != "Halal only" {
		t.Fatalf("the proposal must read what it was when it was recorded: %+v", file.Snapshot.Sessions[0].Sections)
	}
	_, err = f.svc.RecordProposal(f.reader, f.propID, ev.ID)
	_ = code(t, err, "PERMISSION_DENIED")
}

func TestAnEventIsCompletedOnlyWhenEverySessionIsPostedOrCancelled(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Fullday", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	_, err := m.svc.CompleteEvent(m.office, m.propID, ev.ID)
	_ = code(t, err, "EVENT_STATUS_INVALID") // still tentative
	m.confirm(t, ev.ID)

	_, err = m.svc.CompleteEvent(m.office, m.propID, ev.ID)
	e := code(t, err, "SESSIONS_NOT_POSTED")
	if e.Context["sessions"] == nil {
		t.Fatalf("the error says how many sessions are not posted: %+v", e.Context)
	}
	_, err = m.svc.PostSession(m.office, m.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	done, err := m.svc.CompleteEvent(m.office, m.propID, ev.ID)
	must(t, err)
	if done.Status != "COMPLETED" {
		t.Fatalf("status: %s", done.Status)
	}
	// completing does not close the bill
	if fo := m.folioOf(t, ev.ID); fo.Status != "OPEN" {
		t.Fatalf("the folio after completing: %+v", fo)
	}
	_, err = m.svc.CompleteEvent(m.office, m.propID, ev.ID)
	_ = code(t, err, "EVENT_STATUS_INVALID")
	_, err = m.svc.CompleteEvent(m.reader, m.propID, ev.ID)
	_ = code(t, err, "PERMISSION_DENIED")
}
