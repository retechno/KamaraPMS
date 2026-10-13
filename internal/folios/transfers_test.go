package folios_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// Transfers between folios (docs/architecture/30-group-folio.md, part B).

// otherFolio is the guest folio of a second stay of another reservation (room 102): a folio that has nothing to do with the first.
func (p *payerFx) otherFolio(t *testing.T) int64 {
	t.Helper()
	var typeID int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT room_type_id FROM rooms WHERE property_id = $1 AND room_number = '101'`, p.propID).Scan(&typeID))
	roomID := p.Room(t, p.admin, p.propID, typeID, "102").ID
	stay := p.Stay(t, p.tenantID, p.propID, typeID, roomID, "2026-10-01", "2026-10-03")
	var res, folio int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&res))
	must(t, p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		sf, err := p.Folios.AttachStayFolio(ctx, p.principal, p.propID, res, stay, 0)
		folio = sf.ID
		return err
	}))
	return folio
}

func (p *payerFx) codeID(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, p.propID, code).Scan(&id))
	return id
}

func (p *payerFx) items(ids ...int64) folios.TransferItemsInput {
	return folios.TransferItemsInput{ItemIDs: ids, Reason: "moved", Approval: p.approval()}
}

func (p *payerFx) moveItems(from, to int64, in folios.TransferItemsInput) (folios.TransferResult, error) {
	in.FolioID = to
	return p.Folios.TransferItems(p.admin, p.propID, from, in)
}

func (p *payerFx) closeFolio(t *testing.T, id int64) {
	t.Helper()
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, id))
}

type logRow struct {
	from, to, origin, previous int64
	original                   *int64
	returns                    *int64
	amount                     string
}

func (p *payerFx) log(t *testing.T, transferID int64) logRow {
	t.Helper()
	var r logRow
	must(t, p.Pool.QueryRow(context.Background(), `SELECT from_folio_id, to_folio_id, origin_folio_id, previous_folio_id, original_item_id, returns_transfer_id, trim_scale(amount)::text FROM folio_transfers WHERE id = $1`,
		transferID).Scan(&r.from, &r.to, &r.origin, &r.previous, &r.original, &r.returns, &r.amount))
	return r
}

func TestTransferItemsByCodeAndByItems(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	a, b, c := p.charge(t, p.guestFolio, "a"), p.charge(t, p.guestFolio, "b"), p.charge(t, p.guestFolio, "c")
	laundry := p.codeID(t, "LAUNDRY")
	l, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "l", folios.ChargeInput{ChargeCodeID: laundry, Quantity: "1", UnitPrice: ptr("50000")})
	must(t, err)

	// every minibar charge in one go: one batch, one approval, one transaction, the laundry stays
	res, err := p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{ChargeCodeID: &p.minibar, Reason: "company pays the minibar", Approval: p.approval()})
	must(t, err)
	if res.Scope != "RESERVATION" || res.Kind != "ITEM" || len(res.Moved) != 3 || res.Replayed {
		t.Fatalf("result: %+v", res)
	}
	if p.balance(t, p.guestFolio) != "50000" || p.balance(t, cf) != "366300" {
		t.Fatalf("balances: guest %s company %s", p.balance(t, p.guestFolio), p.balance(t, cf))
	}
	for _, m := range res.Moved {
		r := p.log(t, m.TransferID)
		if r.from != p.guestFolio || r.to != cf || r.origin != p.guestFolio || r.previous != p.guestFolio || r.original == nil || r.returns != nil || r.amount != "122100" {
			t.Fatalf("log row: %+v", r)
		}
	}
	if p.Count(t, `SELECT count(DISTINCT batch_id) FROM folio_transfers`) != 1 || p.Count(t, `SELECT count(*) FROM folio_transfer_batches WHERE reason = 'company pays the minibar' AND approved_by IS NOT NULL`) != 1 {
		t.Fatal("one batch, with its reason and approver")
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.item_transferred'`) != 3 || p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.items_transferred'`) != 1 {
		t.Fatal("audit: one entry for each charge and one for the batch")
	}

	// the same code again has nothing left to move
	_, err = p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{ChargeCodeID: &p.minibar, Reason: "again", Approval: p.approval()})
	wantCode(t, err, "TRANSFER_SELECTION_EMPTY")

	// named charges: one that nothing can move makes the whole request fail, and nothing is written
	before := p.Count(t, `SELECT count(*) FROM folio_items`)
	_, err = p.moveItems(p.guestFolio, cf, p.items(l.Item.ID, a)) // a was moved already
	e := code(t, err, "FOLIO_TRANSFER_INVALID")
	if fmt.Sprint(e.Context["item_ids"]) != fmt.Sprint([]int64{a}) {
		t.Fatalf("context: %v", e.Context)
	}
	_, err = p.moveItems(p.guestFolio, cf, p.items(l.Item.ID, 999999))
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	if p.Count(t, `SELECT count(*) FROM folio_items`) != before {
		t.Fatal("a refused request writes nothing")
	}
	_, err = p.moveItems(p.guestFolio, cf, p.items(b)) // a single item that was moved: the old answer
	wantCode(t, err, "ALREADY_REVERSED")
	_ = c
	// by items
	ok, err := p.moveItems(p.guestFolio, cf, p.items(l.Item.ID))
	must(t, err)
	if len(ok.Moved) != 1 || p.balance(t, p.guestFolio) != "0" {
		t.Fatalf("by items: %+v / %s", ok, p.balance(t, p.guestFolio))
	}

	// the shape of the request
	_, err = p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{Reason: "x", Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{ItemIDs: []int64{1}, ChargeCodeID: &p.minibar, Reason: "x", Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{ItemIDs: []int64{1}, Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED") // no reason
	_, err = p.moveItems(p.guestFolio, p.guestFolio, p.items(1))
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
}

func TestTransferItemsMoreThan200Charges(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	ids := make([]int64, 201)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	_, err := p.moveItems(p.guestFolio, cf, p.items(ids...))
	wantCode(t, err, "TOO_MANY_ITEMS")
	for i := 0; i < 201; i++ {
		_, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, fmt.Sprintf("m%d", i), folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("1000")})
		must(t, err)
	}
	before := p.Count(t, `SELECT count(*) FROM folio_items`)
	_, err = p.moveItems(p.guestFolio, cf, folios.TransferItemsInput{ChargeCodeID: &p.minibar, Reason: "all", Approval: p.approval()})
	wantCode(t, err, "TOO_MANY_ITEMS")
	if p.Count(t, `SELECT count(*) FROM folio_items`) != before || p.Count(t, `SELECT count(*) FROM folio_transfers`) != 0 {
		t.Fatal("nothing is moved when the code has more than 200")
	}
}

func TestChainedTransferAndTheReturnToThePreviousFolioAndToTheOrigin(t *testing.T) {
	p := setupPayer(t)
	b := p.companyFolio(t, "FB")
	c := p.otherFolio(t)
	a := p.guestFolio

	// A to B, then B to C (a folio that has nothing to do with the stay)
	i1, i2 := p.charge(t, a, "x1"), p.charge(t, a, "x2")
	t1, err := p.moveItems(a, b, p.items(i1, i2))
	must(t, err)
	t2, err := p.moveItems(b, c, folios.TransferItemsInput{ItemIDs: []int64{t1.Moved[0].InItemID, t1.Moved[1].InItemID}, Reason: "to the other guest", Approval: p.approval()})
	must(t, err)
	if t2.Scope != "ANY" {
		t.Fatalf("scope: %s", t2.Scope)
	}
	for _, m := range t2.Moved {
		r := p.log(t, m.TransferID)
		if r.origin != a || r.previous != b || r.from != b || r.to != c {
			t.Fatalf("a chain A, B, C always names A as the origin: %+v", r)
		}
	}
	if p.balance(t, a) != "0" || p.balance(t, b) != "0" || p.balance(t, c) != "244200" {
		t.Fatalf("balances: %s %s %s", p.balance(t, a), p.balance(t, b), p.balance(t, c))
	}
	// the first link has moved on: it cannot be returned from B
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, b, folios.ReturnInput{TransferIDs: []int64{t1.Moved[0].TransferID}, To: "ORIGIN", Reason: "back", Approval: p.approval()})
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")

	// from C, one charge goes back to the previous folio (B), the other straight to the first origin (A)
	r1, err := p.Folios.ReturnTransfers(p.admin, p.propID, c, folios.ReturnInput{TransferIDs: []int64{t2.Moved[0].TransferID}, To: "PREVIOUS", Reason: "wrong guest", Approval: p.approval()})
	must(t, err)
	r2, err := p.Folios.ReturnTransfers(p.admin, p.propID, c, folios.ReturnInput{TransferIDs: []int64{t2.Moved[1].TransferID}, To: "ORIGIN", Reason: "wrong guest", Approval: p.approval()})
	must(t, err)
	if p.balance(t, a) != "122100" || p.balance(t, b) != "122100" || p.balance(t, c) != "0" {
		t.Fatalf("after the returns: %s %s %s", p.balance(t, a), p.balance(t, b), p.balance(t, c))
	}
	back := p.log(t, r1.Moved[0].TransferID)
	if back.to != b || back.origin != a || back.previous != c || back.returns == nil || *back.returns != t2.Moved[0].TransferID {
		t.Fatalf("the return to the previous folio: %+v", back)
	}
	home := p.log(t, r2.Moved[0].TransferID)
	if home.to != a || home.origin != a || home.previous != c {
		t.Fatalf("the return to the origin: %+v", home)
	}
	// a transfer is returned once
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, c, folios.ReturnInput{TransferIDs: []int64{t2.Moved[0].TransferID}, To: "PREVIOUS", Reason: "again", Approval: p.approval()})
	wantCode(t, err, "ALREADY_RETURNED")
	// what came back to B goes on to A, from B, to the origin
	r3, err := p.Folios.ReturnTransfers(p.admin, p.propID, b, folios.ReturnInput{TransferIDs: []int64{r1.Moved[0].TransferID}, To: "ORIGIN", Reason: "home", Approval: p.approval()})
	must(t, err)
	if p.balance(t, a) != "244200" || p.balance(t, b) != "0" || p.log(t, r3.Moved[0].TransferID).to != a {
		t.Fatalf("home: %s %s", p.balance(t, a), p.balance(t, b))
	}
	// many at once, to different folios: one approval, one transaction
	i3, i4 := p.charge(t, a, "x3"), p.charge(t, a, "x4")
	m1, err := p.moveItems(a, b, p.items(i3))
	must(t, err)
	m2, err := p.moveItems(a, c, p.items(i4))
	must(t, err)
	_ = m2
	both, err := p.Folios.ReturnTransfers(p.admin, p.propID, c, folios.ReturnInput{TransferIDs: []int64{m2.Moved[0].TransferID}, To: "PREVIOUS", Reason: "both", Approval: p.approval()})
	must(t, err)
	_ = m1
	if len(both.Moved) != 1 || p.balance(t, c) != "0" {
		t.Fatalf("return: %+v %s", both, p.balance(t, c))
	}
	// the log is append only
	if err := p.Exec(t, `UPDATE folio_transfers SET amount = 1`); err == nil {
		t.Fatal("the log must refuse an update")
	}
	// the folio view marks what moved, and hides the corrections that sum to zero
	fo, err := p.Folios.GetFolio(p.admin, p.propID, a)
	must(t, err)
	hidden, shown := 0, 0
	for _, it := range fo.Items {
		if it.Transfer != nil && it.Transfer.Hidden {
			hidden++
		} else if it.Transfer != nil {
			shown++
		}
	}
	if hidden == 0 || shown == 0 {
		t.Fatalf("hidden %d shown %d", hidden, shown)
	}
}

func TestAClosedFolioIsNeitherSourceNorTargetOfATransferOrAReturn(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	item := p.charge(t, p.guestFolio, "k")
	p.closeFolio(t, cf)
	_, err := p.moveItems(p.guestFolio, cf, p.items(item))
	if e := code(t, err, "FOLIO_CLOSED"); e.Context["folio_id"] != cf || e.Context["folio_number"] != "FC1" {
		t.Fatalf("the closed folio is named: %v", e.Context)
	}
	_, err = p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "kb", folios.TransferBalanceInput{FolioID: cf, Amount: "1000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "FOLIO_CLOSED")
	_, err = p.Folios.TransferBalance(p.admin, p.propID, cf, "kc", folios.TransferBalanceInput{FolioID: p.guestFolio, Amount: "1000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "FOLIO_CLOSED")
}

func TestReturnToAClosedPreviousFolioIsRefused(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	item := p.charge(t, p.guestFolio, "k")
	m, err := p.moveItems(p.guestFolio, cf, p.items(item))
	must(t, err)
	p.closeFolio(t, p.guestFolio)
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, cf, folios.ReturnInput{TransferIDs: []int64{m.Moved[0].TransferID}, To: "PREVIOUS", Reason: "back", Approval: p.approval()})
	if e := code(t, err, "FOLIO_CLOSED"); e.Context["folio_id"] != p.guestFolio {
		t.Fatalf("context: %v", e.Context)
	}
	must(t, p.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, p.guestFolio))
	p.closeFolio(t, cf)
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, cf, folios.ReturnInput{TransferIDs: []int64{m.Moved[0].TransferID}, To: "PREVIOUS", Reason: "back", Approval: p.approval()})
	wantCode(t, err, "FOLIO_CLOSED")                                                            // the holder is closed too
	must(t, p.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, cf)) // the fixture checks that a closed folio is at zero
}

func TestATransferToAFolioOfNoRelationNeedsTransferAnyOnThePersonAndTheApprover(t *testing.T) {
	p := setupPayer(t)
	c := p.otherFolio(t)
	item := p.charge(t, p.guestFolio, "k")
	reverse := []auth.Permission{auth.PermFolioRead, auth.PermFolioReverse}
	clerk := p.User(t, p.tenantID, p.propID, reverse...)
	clerkAny := p.User(t, p.tenantID, p.propID, append(reverse, auth.PermFolioTransferAny)...)
	_, supervisor := p.Account(t, p.tenantID, p.propID, auth.PermCorrectionApprove)
	_, boss := p.Account(t, p.tenantID, p.propID, auth.PermCorrectionApprove, auth.PermFolioTransferAny)
	ap := func(email string) *iam.ApprovalInput {
		return &iam.ApprovalInput{Email: email, Password: roomstest.Password}
	}
	do := func(ctx context.Context, email string) error {
		_, err := p.Folios.TransferItems(ctx, p.propID, p.guestFolio, folios.TransferItemsInput{FolioID: c, ItemIDs: []int64{item}, Reason: "x", Approval: ap(email)})
		return err
	}
	wantCode(t, do(clerk, boss), "PERMISSION_DENIED")               // the person lacks it
	wantCode(t, do(clerkAny, supervisor), "APPROVAL_NOT_PERMITTED") // the approver lacks it
	if p.Count(t, `SELECT count(*) FROM folio_transfers`) != 0 {
		t.Fatal("nothing is moved by a refused request")
	}
	must(t, do(clerkAny, boss))
	if p.Count(t, `SELECT count(*) FROM folio_transfer_batches WHERE scope = 'ANY'`) != 1 {
		t.Fatal("the batch says the scope")
	}
	// a related folio needs neither: the person with the ordinary right and an ordinary approver
	cf := p.companyFolio(t, "FC1")
	item2 := p.charge(t, p.guestFolio, "k2")
	_, err := p.Folios.TransferItems(clerk, p.propID, p.guestFolio, folios.TransferItemsInput{FolioID: cf, ItemIDs: []int64{item2}, Reason: "x", Approval: ap(supervisor)})
	must(t, err)
}

func TestTransferTargets(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	c := p.otherFolio(t)
	reverse := []auth.Permission{auth.PermFolioRead, auth.PermFolioReverse}
	clerk := p.User(t, p.tenantID, p.propID, reverse...)
	list, err := p.Folios.TransferTargets(clerk, p.propID, p.guestFolio, "", 25)
	must(t, err)
	if len(list) != 1 || list[0].ID != cf || list[0].Scope != "RESERVATION" {
		t.Fatalf("a person without folio.transfer_any sees only the related folios: %+v", list)
	}
	all, err := p.Folios.TransferTargets(p.admin, p.propID, p.guestFolio, "", 25)
	must(t, err)
	if len(all) != 2 || all[0].ID != cf || all[1].ID != c || all[1].Scope != "ANY" {
		t.Fatalf("related first, then the others: %+v", all)
	}
	byRoom, err := p.Folios.TransferTargets(p.admin, p.propID, p.guestFolio, "102", 25)
	must(t, err)
	if len(byRoom) != 1 || byRoom[0].ID != c || byRoom[0].Room != "102" {
		t.Fatalf("by room: %+v", byRoom)
	}
	byNumber, err := p.Folios.TransferTargets(p.admin, p.propID, p.guestFolio, "FC", 25)
	must(t, err)
	if len(byNumber) != 1 || byNumber[0].ID != cf {
		t.Fatalf("by number: %+v", byNumber)
	}
	p.closeFolio(t, cf)
	list, err = p.Folios.TransferTargets(p.admin, p.propID, p.guestFolio, "", 25)
	must(t, err)
	if len(list) != 1 || list[0].ID != c {
		t.Fatalf("a closed folio is not offered: %+v", list)
	}
}

func TestTheBalanceTransferIsAnOrdinaryLineOfTheCodeTRFWithoutTaxOrService(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	p.charge(t, p.guestFolio, "k") // 122,100 owed
	trf := p.codeID(t, "TRF")
	// a tax mapped to it would be a component: nothing can be mapped
	_, err := p.Billing.ReplaceRules(p.admin, p.propID, trf, billingconfig.RulesInput{})
	wantCode(t, err, "SYSTEM_CHARGE_CODE_LOCKED")
	_, err = p.Billing.UpdateChargeCode(p.admin, p.propID, trf, billingconfig.ChargeCodePatch{IsActive: new(bool)})
	wantCode(t, err, "SYSTEM_CHARGE_CODE_LOCKED")
	_, err = p.Billing.UpdateChargeCode(p.admin, p.propID, trf, billingconfig.ChargeCodePatch{GLAccountCode: str("4230")})
	wantCode(t, err, "SYSTEM_CHARGE_CODE_LOCKED")
	// no manual posting: not a charge, not an adjustment
	_, err = p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "m1", folios.ChargeInput{ChargeCodeID: trf, Quantity: "1", UnitPrice: ptr("1000")})
	wantCode(t, err, "TRANSFER_CODE_NOT_MANUAL")
	_, err = p.Folios.PostAdjustment(p.admin, p.propID, p.guestFolio, "m2", folios.AdjustmentInput{ChargeCodeID: trf, Amount: "-1000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "TRANSFER_CODE_NOT_MANUAL")

	res, err := p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb1", folios.TransferBalanceInput{FolioID: cf, Amount: "100000", Reason: "the company pays 100,000 of the bill", Approval: p.approval()})
	must(t, err)
	if res.Kind != "BALANCE" || res.Scope != "RESERVATION" || len(res.Moved) != 1 || p.balance(t, p.guestFolio) != "22100" || p.balance(t, cf) != "100000" {
		t.Fatalf("result %+v, balances %s %s", res, p.balance(t, p.guestFolio), p.balance(t, cf))
	}
	m := res.Moved[0]
	type line struct{ typ, source, code, account, ref, refID, debit, credit, tax, svc, desc string }
	read := func(id int64) line {
		var l line
		var ref, refID, account *string
		must(t, p.Pool.QueryRow(context.Background(), `SELECT i.transaction_type, i.source, c.code, i.revenue_account_code, i.reference_type, i.reference_id, i.debit::text, i.credit::text, i.tax_total::text, i.service_charge_total::text, i.description
			FROM folio_items i JOIN charge_codes c ON c.id = i.charge_code_id WHERE i.id = $1`, id).Scan(&l.typ, &l.source, &l.code, &account, &ref, &refID, &l.debit, &l.credit, &l.tax, &l.svc, &l.desc))
		if account != nil {
			l.account = *account
		}
		if ref != nil {
			l.ref, l.refID = *ref, *refID
		}
		return l
	}
	out, in := read(m.OutItemID), read(m.InItemID)
	batch := fmt.Sprint(res.BatchID)
	if out.typ != "ADJUSTMENT" || out.credit != "100000.000" || out.code != "TRF" || out.account != "1215" || out.ref != "FOLIO_BALANCE_TRANSFER" || out.refID != batch || out.tax != "0.000" || out.svc != "0.000" || out.desc != "Transfer to FC1" {
		t.Fatalf("out line: %+v", out)
	}
	if in.typ != "CHARGE" || in.debit != "100000.000" || in.code != "TRF" || in.account != "1215" || in.ref != "FOLIO_BALANCE_TRANSFER" || in.refID != batch || in.source != "TRANSFER" || in.desc != "Transfer from "+p.folioNumber(t, p.guestFolio) {
		t.Fatalf("in line: %+v", in)
	}
	if p.Count(t, `SELECT count(*) FROM folio_item_components WHERE folio_item_id IN ($1, $2)`, m.OutItemID, m.InItemID) != 0 {
		t.Fatal("the engine finds no tax or service charge for the code TRF")
	}
	r := p.log(t, m.TransferID)
	if r.origin != p.guestFolio || r.previous != p.guestFolio || r.original != nil || r.amount != "100000" {
		t.Fatalf("log: %+v", r)
	}
	// a line of the code TRF is not reversed or moved as a charge: it goes back with a return
	_, err = p.Folios.Reverse(p.admin, p.propID, m.InItemID, folios.CorrectionInput{Reason: "x", Approval: p.approval()})
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	_, err = p.moveItems(cf, p.guestFolio, p.items(m.InItemID))
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")

	// more than the balance, and the balance that is left
	_, err = p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb2", folios.TransferBalanceInput{FolioID: cf, Amount: "30000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "TRANSFER_EXCEEDS_BALANCE")
	_, err = p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb3", folios.TransferBalanceInput{FolioID: cf, Amount: "0", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	// the same key and request again is the same transfer; another request under it is refused
	again, err := p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb1", folios.TransferBalanceInput{FolioID: cf, Amount: "100000", Reason: "the company pays 100,000 of the bill", Approval: p.approval()})
	must(t, err)
	if !again.Replayed || again.Moved[0].TransferID != m.TransferID || p.Count(t, `SELECT count(*) FROM folio_transfers`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	_, err = p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb1", folios.TransferBalanceInput{FolioID: cf, Amount: "5000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
	// the account of the key is needed
	must(t, p.Exec(t, `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'FOLIO_TRANSFER'`, p.propID))
	_, err = p.Folios.TransferBalance(p.admin, p.propID, p.guestFolio, "tb4", folios.TransferBalanceInput{FolioID: cf, Amount: "1000", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "TRANSFER_ACCOUNT_NOT_MAPPED")

	// the return of a balance goes back to the folio it left, never to a first origin
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, cf, folios.ReturnInput{TransferIDs: []int64{m.TransferID}, To: "ORIGIN", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	must(t, p.Exec(t, `INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) SELECT tenant_id, property_id, 'FOLIO_TRANSFER', id FROM gl_accounts WHERE property_id = $1 AND code = '1215'`, p.propID))
	back, err := p.Folios.ReturnTransfers(p.admin, p.propID, cf, folios.ReturnInput{TransferIDs: []int64{m.TransferID}, To: "PREVIOUS", Reason: "the company will not pay", Approval: p.approval()})
	must(t, err)
	if p.balance(t, p.guestFolio) != "122100" || p.balance(t, cf) != "0" || *p.log(t, back.Moved[0].TransferID).returns != m.TransferID {
		t.Fatalf("return: %s %s", p.balance(t, p.guestFolio), p.balance(t, cf))
	}
	// the account of the transfers is zero
	if p.Count(t, `SELECT count(*) FROM (SELECT sum(debit - credit) AS n FROM folio_items i JOIN charge_codes c ON c.id = i.charge_code_id WHERE c.charge_type = 'TRANSFER' GROUP BY i.business_date HAVING sum(debit - credit) <> 0) x`) != 0 {
		t.Fatal("the lines of the code TRF come to zero on every day")
	}
}

func (p *payerFx) folioNumber(t *testing.T, id int64) string {
	t.Helper()
	var n string
	must(t, p.Pool.QueryRow(context.Background(), `SELECT folio_number FROM folios WHERE id = $1`, id).Scan(&n))
	return n
}

func TestTwoTransfersAtOnce(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	c := p.otherFolio(t)
	item := p.charge(t, p.guestFolio, "k")

	// the same charge to two folios: one wins, the other finds it reversed
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, to := range []int64{cf, c} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.moveItems(p.guestFolio, to, p.items(item))
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			wantCode(t, err, "ALREADY_REVERSED")
		}
	}
	if wins != 1 || p.Count(t, `SELECT count(*) FROM folio_transfers`) != 1 || p.balance(t, p.guestFolio) != "0" {
		t.Fatalf("exactly one transfer wins: %v, %d rows, balance %s", errs, p.Count(t, `SELECT count(*) FROM folio_transfers`), p.balance(t, p.guestFolio))
	}

	// two balances of 70,000 from a folio that owes 122,100: one succeeds, the other finds 52,100
	owner, other := cf, c
	if p.balance(t, cf) == "0" {
		owner, other = c, cf
	}
	errs = make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.Folios.TransferBalance(p.admin, p.propID, owner, fmt.Sprintf("race-%d", i), folios.TransferBalanceInput{FolioID: other, Amount: "70000", Reason: "x", Approval: p.approval()})
		}()
	}
	wg.Wait()
	wins = 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			wantCode(t, err, "TRANSFER_EXCEEDS_BALANCE")
		}
	}
	if wins != 1 {
		t.Fatalf("two transfers of 70,000 from 122,100: one wins: %v", errs)
	}

	// A to B and B to A at once: ascending locks, no deadlock
	i1 := p.charge(t, p.guestFolio, "d1")
	i2 := p.charge(t, c, "d2")
	errs = make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = p.moveItems(p.guestFolio, c, p.items(i1)) }()
	go func() { defer wg.Done(); _, errs[1] = p.moveItems(c, p.guestFolio, p.items(i2)) }()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("both directions at once: %v", errs)
	}

	// two returns of one transfer: the database lets one through
	m, err := p.moveItems(p.guestFolio, cf, p.items(p.charge(t, p.guestFolio, "r1")))
	must(t, err)
	errs = make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.Folios.ReturnTransfers(p.admin, p.propID, cf, folios.ReturnInput{TransferIDs: []int64{m.Moved[0].TransferID}, To: "PREVIOUS", Reason: "back", Approval: p.approval()})
		}()
	}
	wg.Wait()
	wins = 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !strings.Contains(err.Error(), "ALREADY_RETURNED") && !strings.Contains(err.Error(), "FOLIO_TRANSFER_INVALID") && !strings.Contains(err.Error(), "ALREADY_REVERSED") {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("a transfer is returned once: %v", errs)
	}
}

func TestTheLogOfAFolioSaysWhereEachChargeIsNow(t *testing.T) {
	p := setupPayer(t)
	b := p.companyFolio(t, "FB")
	c := p.otherFolio(t)
	i1 := p.charge(t, p.guestFolio, "k")
	t1, err := p.moveItems(p.guestFolio, b, p.items(i1))
	must(t, err)
	_, err = p.moveItems(b, c, p.items(t1.Moved[0].InItemID))
	must(t, err)
	out, err := p.Folios.ListTransfers(p.admin, p.propID, p.guestFolio, "out", 50)
	must(t, err)
	if len(out) != 1 || out[0].To.ID != b || out[0].Current == nil || out[0].Current.ID != c || out[0].Origin.ID != p.guestFolio || !out[0].Returnable || out[0].Description == "" || out[0].By == "" || out[0].ApprovedBy == "" {
		t.Fatalf("out of the first folio: %+v", out)
	}
	in, err := p.Folios.ListTransfers(p.admin, p.propID, c, "in", 50)
	must(t, err)
	if len(in) != 1 || in[0].From.ID != b || in[0].Origin.ID != p.guestFolio || in[0].Previous.ID != b {
		t.Fatalf("into the last folio: %+v", in)
	}
	fo, err := p.Folios.GetFolio(p.admin, p.propID, c)
	must(t, err)
	var mark *folios.ItemTransfer
	for _, it := range fo.Items {
		if it.Transfer != nil && it.Transfer.Role == "IN" {
			mark = it.Transfer
		}
	}
	if mark == nil || mark.From.Number != p.folioNumber(t, b) || mark.Origin.Number != p.folioNumber(t, p.guestFolio) || mark.From.Room != "" && mark.From.Room != "101" {
		t.Fatalf("the incoming line says where it came from: %+v", mark)
	}
	_, err = p.Folios.ListTransfers(p.admin, p.propID, c, "sideways", 50)
	wantCode(t, err, "VALIDATION_FAILED")
}
