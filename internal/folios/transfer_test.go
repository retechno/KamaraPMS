package folios_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/migrate"
)

// charge posts a minibar charge (10% service, 11% VAT on it: 100,000 becomes 122,100) to a folio.
func (p *payerFx) charge(t *testing.T, folio int64, key string) int64 {
	t.Helper()
	res, err := p.Folios.PostCharge(p.admin, p.propID, folio, key, folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	return res.Item.ID
}

func (p *payerFx) move(item, to int64) (folios.TransferItemResult, error) {
	return p.Folios.TransferItem(p.admin, p.propID, item, folios.TransferItemInput{FolioID: to, Reason: "company pays the minibar", Approval: p.approval()})
}

func (p *payerFx) balance(t *testing.T, folio int64) string {
	t.Helper()
	var b string
	must(t, p.Pool.QueryRow(context.Background(), `SELECT trim_scale(COALESCE(sum(debit - credit), 0))::text FROM folio_items WHERE folio_id = $1`, folio).Scan(&b))
	return b
}

func TestTransferMovesAChargeAsACopy(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	orig := p.charge(t, p.guestFolio, "k1")
	// the tax rate changes after the posting: a copy keeps what was posted, a recomputation would not
	must(t, p.Exec(t, `UPDATE taxes SET rate = 20 WHERE property_id = $1`, p.propID))

	res, err := p.move(orig, cf)
	must(t, err)
	if res.Reversal.FolioBalance != "0" || res.Charge.FolioBalance != "122100" {
		t.Fatalf("balances: %+v", res)
	}
	if p.balance(t, p.guestFolio) != "0" || p.balance(t, cf) != "122100" {
		t.Fatalf("folios: guest %s company %s", p.balance(t, p.guestFolio), p.balance(t, cf))
	}

	type row struct {
		typ, source, ref, refID, account                   string
		debit, credit, net, service, tax, qty, price, desc string
		serviceDate                                        string
		approved                                           bool
		dept                                               *int64
	}
	read := func(id int64) row {
		var r row
		var typ, source string
		var ref, refID, account *string
		var approved *int64
		must(t, p.Pool.QueryRow(context.Background(), `SELECT transaction_type, source, reference_type, reference_id, revenue_account_code, debit::text, credit::text, net_amount::text,
			service_charge_total::text, tax_total::text, quantity::text, unit_price::text, description, service_date::text, approved_by, department_id FROM folio_items WHERE id = $1`, id).
			Scan(&typ, &source, &ref, &refID, &account, &r.debit, &r.credit, &r.net, &r.service, &r.tax, &r.qty, &r.price, &r.desc, &r.serviceDate, &approved, &r.dept))
		r.typ, r.source, r.approved = typ, source, approved != nil
		if ref != nil {
			r.ref = *ref
		}
		if refID != nil {
			r.refID = *refID
		}
		if account != nil {
			r.account = *account
		}
		return r
	}
	o, rev, moved := read(orig), read(res.Reversal.Item.ID), read(res.Charge.Item.ID)
	want := strconv.FormatInt(orig, 10)
	if rev.typ != "REVERSAL" || !rev.approved || rev.ref != "FOLIO_TRANSFER" || rev.refID != want {
		t.Fatalf("reversal: %+v", rev)
	}
	if moved.typ != "CHARGE" || moved.source != "TRANSFER" || moved.approved || moved.ref != "FOLIO_TRANSFER" || moved.refID != want {
		t.Fatalf("new charge: %+v", moved)
	}
	// the copy is the original, field by field
	if moved.debit != o.debit || moved.credit != o.credit || moved.net != o.net || moved.service != o.service || moved.tax != o.tax || moved.qty != o.qty ||
		moved.price != o.price || moved.desc != o.desc || moved.serviceDate != o.serviceDate || moved.account != o.account || (moved.dept == nil) != (o.dept == nil) {
		t.Fatalf("copy %+v differs from the original %+v", moved, o)
	}
	if moved.tax != "12100.000" {
		t.Fatalf("the tax is the one posted (11%%), not recomputed: %s", moved.tax)
	}
	// the components are copied, and the item's totals equal their sum (the deferred ledger check passed at commit)
	if n := p.Count(t, `SELECT count(*) FROM folio_item_components WHERE folio_item_id = $1`, res.Charge.Item.ID); n != 2 {
		t.Fatalf("components of the copy: %d", n)
	}
	// net zero in every account: the reversal and the copy together change nothing
	if n := p.Count(t, `SELECT count(*) FROM (SELECT revenue_account_code FROM folio_items WHERE folio_id IN ($1, $2) GROUP BY revenue_account_code
		HAVING sum(net_amount) <> (SELECT net_amount FROM folio_items WHERE id = $3)) x`, p.guestFolio, cf, orig); n != 0 {
		t.Fatalf("an account changed: %d", n)
	}
	if n := p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.item_transferred' AND entity_id = $1`, orig); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	// it moves once, and it can move on
	_, err = p.move(orig, cf)
	wantCode(t, err, "ALREADY_REVERSED")
	_, err = p.move(res.Charge.Item.ID, p.guestFolio)
	must(t, err)
	if p.balance(t, p.guestFolio) != "122100" || p.balance(t, cf) != "0" {
		t.Fatalf("after moving back: guest %s company %s", p.balance(t, p.guestFolio), p.balance(t, cf))
	}
}

func TestTransferIsRefusedWhenItIsNotAChargeBetweenFoliosOfOneReservation(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	item := p.charge(t, p.guestFolio, "k1")

	_, err := p.move(item, p.guestFolio)
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	// another reservation
	other := p.newFolio(t, "OTHER1")
	_, err = p.move(item, other)
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	// the deposit folio of the same reservation has no stay
	var deposit int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'DEP1', $3) RETURNING id`, p.tenantID, p.propID, p.reservation).Scan(&deposit))
	_, err = p.move(item, deposit)
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	// a payment is voided, not moved
	pay, err := p.Folios.PostPayment(p.admin, p.propID, p.guestFolio, "pay1", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = p.move(pay.FolioItem.ID, cf)
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	// a closed target
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, cf))
	_, err = p.move(item, cf)
	wantCode(t, err, "FOLIO_CLOSED")
	must(t, p.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, cf))
	// nothing moved
	if got := p.balance(t, p.guestFolio); got != "121100" {
		t.Fatalf("guest folio: %s", got)
	}
	// a reversal is not a charge
	rev, err := p.Folios.Reverse(p.admin, p.propID, item, folios.CorrectionInput{Reason: "x", Approval: p.approval()})
	must(t, err)
	_, err = p.move(rev.Item.ID, cf)
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	_, err = p.move(999999, cf)
	wantCode(t, err, "FOLIO_ITEM_NOT_FOUND")
}

func TestTransferNeedsAReasonAnApprovalAndThePermission(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	item := p.charge(t, p.guestFolio, "k1")
	_, err := p.Folios.TransferItem(p.admin, p.propID, item, folios.TransferItemInput{FolioID: cf, Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.Folios.TransferItem(p.admin, p.propID, item, folios.TransferItemInput{Reason: "x", Approval: p.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.Folios.TransferItem(p.admin, p.propID, item, folios.TransferItemInput{FolioID: cf, Reason: "x"})
	if err == nil {
		t.Fatal("a transfer without an approval must be refused")
	}
	poster := p.User(t, p.tenantID, p.propID, auth.PermFolioPostCharge, auth.PermFolioRead)
	_, err = p.Folios.TransferItem(poster, p.propID, item, folios.TransferItemInput{FolioID: cf, Reason: "x", Approval: p.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	if p.balance(t, cf) != "0" {
		t.Fatal("nothing may have moved")
	}
}

// Two people move the same charge at once to two folios: it moves once.
func TestConcurrentTransfersMoveTheChargeOnce(t *testing.T) {
	p := setupPayer(t)
	a := p.companyFolio(t, "FC1")
	other := p.otherCompany(t)
	var b int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES ($1, $2, 'FC2', $3, $4, 'COMPANY', $5) RETURNING id`,
		p.tenantID, p.propID, p.reservation, p.stay, other).Scan(&b))
	item := p.charge(t, p.guestFolio, "k1")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, to := range []int64{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.move(item, to)
		}()
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one transfer must win: %v", errs)
	}
	if n := p.Count(t, `SELECT count(*) FROM folio_items WHERE source = 'TRANSFER'`); n != 1 {
		t.Fatalf("copies: %d", n)
	}
	if p.balance(t, p.guestFolio) != "0" {
		t.Fatalf("guest folio: %s", p.balance(t, p.guestFolio))
	}
}

// 00062: the way down is refused while a transferred item exists.
func TestMigration00062Down(t *testing.T) {
	p := setupPayer(t)
	ctx := context.Background()
	defer func() {
		if _, err := migrate.Up(ctx, p.Pool); err != nil {
			t.Fatalf("restoring the schema: %v", err)
		}
	}()
	cf := p.companyFolio(t, "FC1")
	_, err := p.move(p.charge(t, p.guestFolio, "k1"), cf)
	must(t, err)
	if _, err := migrate.DownTo(ctx, p.Pool, 61); err == nil || !strings.Contains(err.Error(), "transferred folio items exist") {
		t.Fatalf("the way down must be refused: %v", err)
	}
}
