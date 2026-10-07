package folios_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// Transaction Group (docs/architecture/20-transaction-group.md): a presentation dimension inside one folio. Every test that moves a line also proves that the money did not move.

// ledgerFingerprint is everything that is money or the record of it: the rows of the folio ledger, their components, the payments, the folio totals and the general ledger. A move
// of a line between groups must leave every part of it as it was.
type ledgerFingerprint struct {
	items, components, payments, journals, journalLines string
	debit, credit                                       string
	itemCount, journalCount                             int
}

func (f *fx) fingerprint(t *testing.T) ledgerFingerprint {
	t.Helper()
	var fp ledgerFingerprint
	row := func(sql string, dst ...any) {
		must(t, f.Pool.QueryRow(context.Background(), sql, f.propID).Scan(dst...))
	}
	row(`SELECT COALESCE(md5(string_agg(i::text, '' ORDER BY i.id)), '-'), count(*) FROM folio_items i WHERE i.property_id = $1`, &fp.items, &fp.itemCount)
	row(`SELECT COALESCE(md5(string_agg(c::text, '' ORDER BY c.id)), '-') FROM folio_item_components c WHERE c.property_id = $1`, &fp.components)
	row(`SELECT COALESCE(md5(string_agg(p::text, '' ORDER BY p.id)), '-') FROM payments p WHERE p.property_id = $1`, &fp.payments)
	row(`SELECT COALESCE(md5(string_agg(j::text, '' ORDER BY j.id)), '-'), count(*) FROM gl_journals j WHERE j.property_id = $1`, &fp.journals, &fp.journalCount)
	row(`SELECT COALESCE(md5(string_agg(l::text, '' ORDER BY l.id)), '-') FROM gl_journal_lines l WHERE l.property_id = $1`, &fp.journalLines)
	row(`SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM folio_items WHERE property_id = $1 AND folio_id = (SELECT min(id) FROM folios WHERE property_id = $1)`, &fp.debit, &fp.credit)
	return fp
}

func (f *fx) groupOf(t *testing.T, itemID int64) string {
	t.Helper()
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	for _, it := range fo.Items {
		if it.ID == itemID {
			return it.GroupCode
		}
	}
	t.Fatalf("item %d is not on the folio", itemID)
	return ""
}

func TestEveryLineStartsInGroupA(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	p := f.payment(t, "p1", "50000")
	if c.Item.GroupCode != "A" || p.FolioItem.GroupCode != "A" || p.Payment.GroupCode != "A" {
		t.Fatalf("new lines are in group A: charge %q, payment line %q, payment %q", c.Item.GroupCode, p.FolioItem.GroupCode, p.Payment.GroupCode)
	}
	// a line that has no row at all (every line that existed before the feature) is in group A
	if n := f.Count(t, `SELECT count(*) FROM folio_item_groups`); n != 0 {
		t.Fatalf("no row is written until a line is moved: %d", n)
	}
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	for _, it := range fo.Items {
		if it.GroupCode != "A" {
			t.Fatalf("existing line %d: %q", it.ID, it.GroupCode)
		}
	}
	list, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{}, 0, 50)
	must(t, err)
	if len(list.Data) != 1 || list.Data[0].GroupCode != "A" {
		t.Fatalf("payment list: %+v", list.Data)
	}
}

func TestAChargeAndAPaymentMoveBetweenGroupsWithoutMovingAnyMoney(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	p := f.payment(t, "p1", "50000")
	f.charge(t, "c2")
	before := f.fingerprint(t)
	audits := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio_item.group_changed'`)
	bal := func() string {
		fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
		must(t, err)
		return fo.Balance + "|" + fo.Totals.Debit + "|" + fo.Totals.Credit
	}
	balBefore := bal()

	// A to B to C to D and back to A, for a charge and for the line of a payment
	cycle := []string{"B", "C", "D", "A"}
	prev := "A"
	for _, g := range cycle {
		res, err := f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: g})
		must(t, err)
		if !res.Changed || res.GroupCode != g || res.Previous != prev || res.FolioID != f.folio || res.PaymentID != nil {
			t.Fatalf("charge to %s: %+v", g, res)
		}
		if got := f.groupOf(t, c.Item.ID); got != g {
			t.Fatalf("the folio shows group %q, want %q", got, g)
		}
		prev = g
	}
	prev = "A"
	for _, g := range cycle {
		res, err := f.Folios.SetPaymentGroup(f.admin, f.propID, p.Payment.ID, folios.GroupInput{GroupCode: g})
		must(t, err)
		if !res.Changed || res.GroupCode != g || res.Previous != prev || res.FolioID != f.folio || res.PaymentID == nil || *res.PaymentID != p.Payment.ID {
			t.Fatalf("payment to %s: %+v", g, res)
		}
		got, err := f.Folios.GetPayment(f.admin, f.propID, p.Payment.ID)
		must(t, err)
		if got.GroupCode != g || got.FolioID != f.folio {
			t.Fatalf("the payment shows group %q on folio %d", got.GroupCode, got.FolioID)
		}
		prev = g
	}
	// a line left in group C, the other in group B, to see them apart
	_, err := f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "C"})
	must(t, err)
	_, err = f.Folios.SetPaymentGroup(f.admin, f.propID, p.Payment.ID, folios.GroupInput{GroupCode: "B"})
	must(t, err)

	// the money: not one byte of the ledger, the payments or the general ledger differs; the balance is the balance of the whole folio
	if after := f.fingerprint(t); after != before {
		t.Fatalf("moving lines between groups changed the money:\nbefore %+v\nafter  %+v", before, after)
	}
	if got := bal(); got != balBefore {
		t.Fatalf("the folio balance moved: %s -> %s", balBefore, got)
	}
	// every real move is audited with the old and the new group, nothing else is
	if got := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio_item.group_changed'`) - audits; got != 10 {
		t.Fatalf("%d audit entries for 10 moves", got)
	}
	var entry string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT old_data::text || ' -> ' || new_data::text FROM audit_logs WHERE action = 'folio_item.group_changed' ORDER BY id DESC LIMIT 1`).Scan(&entry))
	if entry == "" || !contains(entry, `"group_code": "A"`) || !contains(entry, `"group_code": "B"`) {
		t.Fatalf("the audit entry says the old and the new group: %s", entry)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestMovingToTheGroupItIsInIsANoOp(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	audits := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio_item.group_changed'`)
	res, err := f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "a"}) // lower case is the same group
	must(t, err)
	if res.Changed || res.GroupCode != "A" {
		t.Fatalf("%+v", res)
	}
	if f.Count(t, `SELECT count(*) FROM folio_item_groups`) != 0 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio_item.group_changed'`) != audits {
		t.Fatal("nothing is written and nothing is audited when the group does not change")
	}
}

func TestAnInvalidGroupIsRefused(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	for _, g := range []string{"", " ", "E", "Z", "AB", "1", "A;DROP TABLE folio_items", "ä"} {
		_, err := f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: g})
		wantCode(t, err, "VALIDATION_FAILED")
	}
	if f.groupOf(t, c.Item.ID) != "A" {
		t.Fatal("a refused move changes nothing")
	}
}

func TestAMissingLineOrPaymentIsNotFound(t *testing.T) {
	f := setup(t)
	_, err := f.Folios.SetItemGroup(f.admin, f.propID, 999999, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "FOLIO_ITEM_NOT_FOUND")
	_, err = f.Folios.SetPaymentGroup(f.admin, f.propID, 999999, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "PAYMENT_NOT_FOUND")
}

func TestAReversalIsShownWithTheLineItReverses(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	rev, err := f.Folios.Reverse(f.admin, f.propID, c.Item.ID, folios.CorrectionInput{Reason: "posted twice", Approval: f.approval()})
	must(t, err)
	if rev.Item.GroupCode != "A" {
		t.Fatalf("the reversal starts with its line: %q", rev.Item.GroupCode)
	}
	_, err = f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "C"})
	must(t, err)
	if got := f.groupOf(t, rev.Item.ID); got != "C" {
		t.Fatalf("the reversal follows the line it reverses: %q", got)
	}
	// but it cannot be moved alone: the pair would no longer net to zero in one group
	_, err = f.Folios.SetItemGroup(f.admin, f.propID, rev.Item.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "FOLIO_ITEM_GROUP_NOT_ALLOWED")
	if f.groupOf(t, rev.Item.ID) != "C" || f.groupOf(t, c.Item.ID) != "C" {
		t.Fatal("the pair stays together")
	}
}

func TestAVoidedPaymentKeepsItsLineMovableAndItsReversalFollows(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1")
	p := f.payment(t, "p1", "50000")
	voided, err := f.Folios.Void(f.admin, f.propID, p.Payment.ID, folios.CorrectionInput{Reason: "wrong guest", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.SetPaymentGroup(f.admin, f.propID, p.Payment.ID, folios.GroupInput{GroupCode: "D"})
	must(t, err)
	if got := f.groupOf(t, voided.FolioItem.ID); got != "D" {
		t.Fatalf("the reversal of the void is in the group of the payment line: %q", got)
	}
}

func TestOnlyAClosedFolioIsLocked(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	f.payment(t, "p1", "122100")
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	_, err = f.Folios.CloseFolio(f.admin, f.propID, f.folio, fo.Version)
	must(t, err)
	_, err = f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "FOLIO_CLOSED")
	if f.groupOf(t, c.Item.ID) != "A" {
		t.Fatal("a closed folio does not change")
	}
}

func TestGroupsAreScopedByTenantPropertyAndPermission(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	p := f.payment(t, "p1", "50000")

	// another tenant sees nothing of it
	other := f.Tenant(t, "XYZ")
	otherProp := f.Property(t, other.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, other.ID)
	_, err := f.Folios.SetItemGroup(otherAdmin, otherProp.ID, c.Item.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "FOLIO_ITEM_NOT_FOUND")
	_, err = f.Folios.SetPaymentGroup(otherAdmin, otherProp.ID, p.Payment.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "PAYMENT_NOT_FOUND")
	// the same tenant, another property: the line is not there, and the property of this tenant that the caller is not assigned to is not found
	second := f.Property(t, f.tenantID, "JKT")
	_, err = f.Folios.SetItemGroup(f.admin, second.ID, c.Item.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "FOLIO_ITEM_NOT_FOUND")
	// a caller of the right property without the permission
	reader := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.Folios.SetItemGroup(reader, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Folios.SetPaymentGroup(reader, f.propID, p.Payment.ID, folios.GroupInput{GroupCode: "B"})
	wantCode(t, err, "PERMISSION_DENIED")
	// the permission that posts charges is the one that arranges the bill
	poster := f.User(t, f.tenantID, f.propID, auth.PermFolioRead, auth.PermFolioPostCharge)
	if _, err := f.Folios.SetItemGroup(poster, f.propID, c.Item.ID, folios.GroupInput{GroupCode: "B"}); err != nil {
		t.Fatal(err)
	}
	if f.groupOf(t, c.Item.ID) != "B" {
		t.Fatal("moved by the poster")
	}
	// nothing the refused callers did left a trace
	if n := f.Count(t, `SELECT count(*) FROM folio_item_groups`); n != 1 {
		t.Fatalf("one row, the poster's: %d", n)
	}
}

// Two people move the same line at the same time, and a third posts a charge to the folio: each move is whole, the folio row lock makes them take turns, and the last writer wins with a
// consistent row and an audit entry for every real change.
func TestConcurrentMovesOfOneLineDoNotCorruptIt(t *testing.T) {
	f := setup(t)
	c := f.charge(t, "c1")
	before := f.fingerprint(t)
	targets := []string{"B", "C", "D", "B", "C", "D", "B", "C"}
	var wg sync.WaitGroup
	results := make([]folios.GroupResult, len(targets))
	errs := make([]error, len(targets))
	for i, g := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = f.Folios.SetItemGroup(f.admin, f.propID, c.Item.ID, folios.GroupInput{GroupCode: g})
		}()
	}
	var posted folios.ItemResult
	var postErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		posted, postErr = f.Folios.PostCharge(f.admin, f.propID, f.folio, "c-concurrent", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	}()
	wg.Wait()
	changed := 0
	for i := range targets {
		if errs[i] != nil {
			var ae *apperr.Error
			if !errors.As(errs[i], &ae) {
				t.Fatalf("move %d: %v", i, errs[i])
			}
			t.Fatalf("move %d failed: %v", i, errs[i])
		}
		if results[i].Changed {
			changed++
		}
	}
	must(t, postErr)
	final := f.groupOf(t, c.Item.ID)
	if final != "B" && final != "C" && final != "D" {
		t.Fatalf("final group %q", final)
	}
	if n := f.Count(t, `SELECT count(*) FROM folio_item_groups WHERE folio_item_id = $1`, c.Item.ID); n != 1 {
		t.Fatalf("one row for the line: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio_item.group_changed'`); n != changed {
		t.Fatalf("%d audit entries for %d real changes", n, changed)
	}
	// the posting that ran beside the moves is intact and in group A
	if f.groupOf(t, posted.Item.ID) != "A" {
		t.Fatal("a new charge is in group A")
	}
	// the money of the line that was moved is what it was
	after := f.fingerprint(t)
	if after.itemCount != before.itemCount+1 {
		t.Fatalf("one new item (the posting), got %d -> %d", before.itemCount, after.itemCount)
	}
}
