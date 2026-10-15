package folios_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The lock of a folio for outlet charges (docs/architecture/34-pos.md section 6): closed by default, opened with a limit, a validity and the outlets allowed, checked under the folio's lock.

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// outlets makes the restaurant and the bar of the property (the masters of stage 1, by SQL: this package tests the lock, not the setup screens).
func (p *payerFx) outlets(t *testing.T) (resto, bar int64) {
	t.Helper()
	ctx := context.Background()
	var dept, profile, list int64
	must(t, p.Pool.QueryRow(ctx, `SELECT id FROM departments WHERE property_id = $1 AND code = 'FB'`, p.propID).Scan(&dept))
	must(t, p.Pool.QueryRow(ctx, `INSERT INTO pos_rule_profiles (tenant_id, property_id, code, name) VALUES ($1, $2, 'FB', 'F&B') RETURNING id`, p.tenantID, p.propID).Scan(&profile))
	must(t, p.Pool.QueryRow(ctx, `INSERT INTO pos_price_lists (tenant_id, property_id, code, name) VALUES ($1, $2, 'MAIN', 'Main') RETURNING id`, p.tenantID, p.propID).Scan(&list))
	for code, dst := range map[string]*int64{"RESTO": &resto, "BAR": &bar} {
		must(t, p.Pool.QueryRow(ctx, `INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
			VALUES ($1, $2, $3, $4, 'RESTAURANT', 'PRINT', $5, $6, $7) RETURNING id`, p.tenantID, p.propID, code, code+" outlet", dept, profile, list).Scan(dst))
	}
	return resto, bar
}

// opener is a person who may open folios for outlets.
func (p *payerFx) opener(t *testing.T) context.Context {
	return p.User(t, p.tenantID, p.propID, auth.PermFolioOutletChargeOpen)
}

func (p *payerFx) openLock(t *testing.T, folio int64, limit string, mode string, outlets ...int64) folios.OutletLock {
	t.Helper()
	l, err := p.Folios.SetOutletLock(p.opener(t), p.propID, folio, folios.OutletLockInput{LimitAmount: dec(limit), ValidMode: mode, OutletIDs: outlets})
	must(t, err)
	return l
}

// outletCharge asks the gate for amount at outlet, in its own transaction with the folio held, and records the charge when it is accepted. It returns the refusal.
func (p *payerFx) outletCharge(t *testing.T, ctx context.Context, folio, outlet int64, amount string) *apperr.Error {
	t.Helper()
	pr, err := auth.Require(ctx)
	must(t, err)
	var refusal *apperr.Error
	err = p.TxM.WithinTx(ctx, func(ctx context.Context) error {
		day, err := p.Tenancy.RequireOpenBusinessDay(ctx, p.propID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, p.propID, []int64{folio}); err != nil {
			return err
		}
		c := folios.OutletChargeCheck{PropertyID: p.propID, BusinessDate: day.BusinessDate, TargetFolioID: folio, OutletID: outlet, Amount: dec(amount), Source: folios.ChargeSourceCharge, BillNumber: "R-0001"}
		d, err := p.Folios.CheckOutletCharge(ctx, pr, c)
		if err != nil {
			return err
		}
		if d.Refusal != nil {
			refusal = d.Refusal // the attempt row is the only write: the transaction commits
			return nil
		}
		_, err = p.Folios.RecordOutletCharge(ctx, pr, c, d, nil, nil)
		return err
	})
	must(t, err)
	return refusal
}

func contextOf(t *testing.T, e *apperr.Error, key string) any {
	t.Helper()
	if e == nil {
		t.Fatal("no refusal")
	}
	return e.Context[key]
}

func TestAFolioIsLockedUntilTheFrontDeskOpensIt(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	l, err := p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
	must(t, err)
	if l.Status != "LOCKED" || l.Reason == nil || *l.Reason != "NEVER_OPENED" || !l.CanOpen || l.Limit != nil || l.PeriodNo != 0 {
		t.Fatalf("never opened: %+v", l)
	}
	ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "50000")
	if ref == nil || ref.Code != "OUTLET_FOLIO_LOCKED" || contextOf(t, ref, "reason") != "NEVER_OPENED" || contextOf(t, ref, "target") != "GUEST" {
		t.Fatalf("a folio that was never opened refuses: %+v", ref)
	}
	if n := p.Count(t, `SELECT count(*) FROM pos_room_charge_attempts WHERE result = 'REJECTED' AND code = 'OUTLET_FOLIO_LOCKED'`); n != 1 {
		t.Fatalf("the refused attempt is a row: %d", n)
	}
	if p.Count(t, `SELECT count(*) FROM pos_room_charges`) != 0 || p.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = `+itoa64(p.guestFolio)) != 0 {
		t.Fatal("a refusal writes no charge and no ledger row")
	}
}

func itoa64(n int64) string { return decimal.NewFromInt(n).String() }

func TestOpeningNeedsALimitAndOutletsAndIsAudited(t *testing.T) {
	p := setupPayer(t)
	resto, bar := p.outlets(t)
	in := func(limit string, mode string, ids ...int64) folios.OutletLockInput {
		return folios.OutletLockInput{LimitAmount: dec(limit), ValidMode: mode, OutletIDs: ids}
	}
	opener := p.opener(t)
	for name, c := range map[string]struct {
		in    folios.OutletLockInput
		field string
	}{
		"no limit":        {in("0", "UNTIL_CLOSE", resto), "limit_amount"},
		"a negative one":  {in("-5", "UNTIL_CLOSE", resto), "limit_amount"},
		"a bad validity":  {in("1000", "FOREVER", resto), "valid_mode"},
		"no outlet":       {in("1000", "TODAY"), "outlet_ids"},
		"too many places": {in("1000.5", "TODAY", resto), "limit_amount"},
	} {
		_, err := p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, c.in)
		if e, ok := apperr.As(err); !ok || e.Code != "VALIDATION_FAILED" || len(e.Fields) == 0 || e.Fields[0].Field != c.field {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, in("1000", "TODAY", 999999))
	wantCode(t, err, "VALIDATION_FAILED")

	l := p.openLock(t, p.guestFolio, "500000", "UNTIL_CLOSE", resto, bar, resto)
	if l.Status != "OPEN" || l.Limit == nil || !l.Limit.Equal(dec("500000")) || l.Used == nil || !l.Used.IsZero() || l.Remaining == nil || !l.Remaining.Equal(dec("500000")) || l.PeriodNo != 1 || len(l.Outlets) != 2 || l.Version != 1 {
		t.Fatalf("opened: %+v", l)
	}
	if len(l.Events) != 1 || l.Events[0].Kind != "OPENED" {
		t.Fatalf("one event: %+v", l.Events)
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.outlet_lock_opened'`) != 1 {
		t.Fatal("opening is audited")
	}
	// who may: the right to open; who may read: the right or the right to read folios; nobody else
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioPostCharge)
	_, err = p.Folios.SetOutletLock(clerk, p.propID, p.guestFolio, in("1000", "TODAY", resto))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = p.Folios.GetOutletLock(clerk, p.propID, p.guestFolio)
	wantCode(t, err, "PERMISSION_DENIED")
	reader := p.User(t, p.tenantID, p.propID, auth.PermFolioRead)
	if _, err := p.Folios.GetOutletLock(reader, p.propID, p.guestFolio); err != nil {
		t.Fatalf("whoever reads folios sees the lock: %v", err)
	}
	_, err = p.Folios.SetOutletLock(opener, p.propID, 999999, in("1000", "TODAY", resto))
	wantCode(t, err, "FOLIO_NOT_FOUND")
}

func TestOnlyAFolioThatCanBeChargedIsOpened(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	must(t, p.Exec(t, `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, p.stay))
	_, err := p.Folios.SetOutletLock(p.opener(t), p.propID, p.guestFolio, folios.OutletLockInput{LimitAmount: dec("1000"), ValidMode: "TODAY", OutletIDs: []int64{resto}})
	wantCode(t, err, "OUTLET_LOCK_NOT_ALLOWED")
	l, err := p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
	must(t, err)
	if l.CanOpen || l.WhyNot == nil || *l.WhyNot != "STAY_NOT_IN_HOUSE" {
		t.Fatalf("a stay that is not in house: %+v", l)
	}
}

func TestTheGateRefusesAnOutletThatIsNotAllowedAndAnAmountOverTheLimitButKeepsTheFolioOpen(t *testing.T) {
	p := setupPayer(t)
	resto, bar := p.outlets(t)
	p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto) // only the restaurant
	if ref := p.outletCharge(t, p.admin, p.guestFolio, bar, "10000"); ref == nil || ref.Code != "OUTLET_NOT_ALLOWED" {
		t.Fatalf("the bar is not on the list: %+v", ref)
	}
	if ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "58730"); ref != nil {
		t.Fatalf("accepted: %+v", ref)
	}
	ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "60000") // 58,730 + 60,000 > 100,000
	if ref == nil || ref.Code != "OUTLET_LIMIT_EXCEEDED" || contextOf(t, ref, "remaining") != "41270" || contextOf(t, ref, "used") != "58730" || contextOf(t, ref, "limit") != "100000" || contextOf(t, ref, "requested") != "60000" {
		t.Fatalf("over the limit: %+v", ref)
	}
	l, err := p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
	must(t, err)
	if l.Status != "OPEN" || !l.Used.Equal(dec("58730")) || !l.Remaining.Equal(dec("41270")) {
		t.Fatalf("an excess does not lock the folio: %+v", l)
	}
	// a smaller part may still pass, and reaching the limit exactly locks the folio by itself
	if ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "41270"); ref != nil {
		t.Fatalf("the rest of the limit: %+v", ref)
	}
	l, err = p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
	must(t, err)
	if l.Status != "LOCKED" || l.Reason == nil || *l.Reason != "LIMIT_REACHED" || !l.Used.Equal(dec("100000")) {
		t.Fatalf("the limit was reached: %+v", l)
	}
	if ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "1"); ref == nil || ref.Code != "OUTLET_FOLIO_LOCKED" || contextOf(t, ref, "reason") != "LIMIT_REACHED" {
		t.Fatalf("after the limit: %+v", ref)
	}
	if p.Count(t, `SELECT count(*) FROM folio_outlet_lock_events WHERE kind = 'LOCKED' AND reason = 'LIMIT_REACHED'`) != 1 ||
		p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.outlet_lock_locked'`) != 1 {
		t.Fatal("the automatic lock is an event and an audit entry")
	}
}

func TestTheLimitAndTheOutletsCanBeChangedButNotBelowWhatWasUsed(t *testing.T) {
	p := setupPayer(t)
	resto, bar := p.outlets(t)
	l := p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto)
	_ = p.outletCharge(t, p.admin, p.guestFolio, resto, "70000")
	opener := p.opener(t)
	v := l.Version
	_, err := p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, folios.OutletLockInput{LimitAmount: dec("50000"), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{resto}, Version: &v})
	wantCode(t, err, "LIMIT_BELOW_USED")
	_, err = p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, folios.OutletLockInput{LimitAmount: dec("200000"), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{resto}})
	wantCode(t, err, "VALIDATION_FAILED") // changing an open lock needs its version
	stale := v + 5
	_, err = p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, folios.OutletLockInput{LimitAmount: dec("200000"), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{resto}, Version: &stale})
	wantCode(t, err, "VERSION_CONFLICT")
	got, err := p.Folios.SetOutletLock(opener, p.propID, p.guestFolio, folios.OutletLockInput{LimitAmount: dec("200000"), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{resto, bar}, Version: &v})
	must(t, err)
	if !got.Limit.Equal(dec("200000")) || !got.Used.Equal(dec("70000")) || got.PeriodNo != 1 || len(got.Outlets) != 2 || got.Version != v+1 {
		t.Fatalf("changed, same period, usage kept: %+v", got)
	}
	if ref := p.outletCharge(t, p.admin, p.guestFolio, bar, "100000"); ref != nil {
		t.Fatalf("the bar may now charge: %+v", ref)
	}
	if p.Count(t, `SELECT count(*) FROM folio_outlet_lock_events WHERE kind = 'CHANGED'`) != 1 || p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.outlet_lock_changed'`) != 1 {
		t.Fatal("a change is an event and an audit entry")
	}
}

func TestOpeningAgainStartsANewPeriodAndLockingByHandWorks(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	l := p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto)
	_ = p.outletCharge(t, p.admin, p.guestFolio, resto, "100000") // reaches the limit: locked
	locked, err := p.Folios.LockOutletFolio(p.opener(t), p.propID, p.guestFolio, folios.OutletLockPatch{})
	must(t, err)
	if locked.Status != "LOCKED" || *locked.Reason != "LIMIT_REACHED" {
		t.Fatalf("locking a locked folio answers it as it is: %+v", locked)
	}
	again := p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto)
	if again.PeriodNo != l.PeriodNo+1 || !again.Used.IsZero() || again.Status != "OPEN" {
		t.Fatalf("a new period: %+v", again)
	}
	byHand, err := p.Folios.LockOutletFolio(p.opener(t), p.propID, p.guestFolio, folios.OutletLockPatch{})
	must(t, err)
	if byHand.Status != "LOCKED" || *byHand.Reason != "MANUAL" {
		t.Fatalf("locked by hand: %+v", byHand)
	}
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioRead)
	_, err = p.Folios.LockOutletFolio(clerk, p.propID, p.guestFolio, folios.OutletLockPatch{})
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestALockOfOneBusinessDayEndsWithTheDay(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	p.openLock(t, p.guestFolio, "100000", "TODAY", resto)
	if ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "1000"); ref != nil {
		t.Fatalf("today: %+v", ref)
	}
	// the business day moved on: the lock was for the day it was opened
	must(t, p.Exec(t, `UPDATE folio_outlet_locks SET valid_on = valid_on - 1 WHERE folio_id = $1`, p.guestFolio))
	ref := p.outletCharge(t, p.admin, p.guestFolio, resto, "1000")
	if ref == nil || ref.Code != "OUTLET_FOLIO_LOCKED" || contextOf(t, ref, "reason") != "EXPIRED" {
		t.Fatalf("expired: %+v", ref)
	}
	l, err := p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
	must(t, err)
	if l.Status != "LOCKED" || *l.Reason != "EXPIRED" {
		t.Fatalf("the lock says so: %+v", l)
	}
	// and the close of the day writes the same down for the locks nobody asked about
	p.openLock(t, p.guestFolio, "100000", "TODAY", resto)
	must(t, p.Exec(t, `UPDATE folio_outlet_locks SET valid_on = valid_on - 1 WHERE folio_id = $1`, p.guestFolio))
	pr, _ := auth.Require(p.admin)
	var n int
	must(t, p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		day, err := p.Tenancy.RequireOpenBusinessDay(ctx, p.propID, db.ForShare, nil)
		if err != nil {
			return err
		}
		n, err = p.Folios.ExpireOutletLocks(ctx, pr, p.propID, day.BusinessDate)
		return err
	}))
	if n != 1 || p.Count(t, `SELECT count(*) FROM folio_outlet_locks WHERE status = 'LOCKED' AND locked_reason = 'EXPIRED'`) != 1 {
		t.Fatalf("expired by the close: %d", n)
	}
}

func TestCheckOutLocksTheFoliosOfTheStay(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	company := p.companyFolio(t, "FC2")
	p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto)
	p.openLock(t, company, "100000", "UNTIL_CLOSE", resto)
	must(t, p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		_, err := p.Folios.CloseStayFolios(ctx, p.principal, p.propID, p.stay)
		return err
	}))
	for _, f := range []int64{p.guestFolio, company} {
		l, err := p.Folios.GetOutletLock(p.admin, p.propID, f)
		must(t, err)
		if l.Status != "LOCKED" || l.Reason == nil || *l.Reason != "CHECKOUT" || l.FolioStatus != "CLOSED" {
			t.Fatalf("folio %d after the check-out: %+v", f, l)
		}
	}
	if p.Count(t, `SELECT count(*) FROM folio_outlet_lock_events WHERE kind = 'LOCKED' AND reason = 'CHECKOUT'`) != 2 {
		t.Fatal("one event for each folio")
	}
}

func TestTwoOutletsChargingTheSameFolioCannotPassTheLimit(t *testing.T) {
	p := setupPayer(t)
	resto, bar := p.outlets(t)
	for round := 0; round < 6; round++ {
		p.openLock(t, p.guestFolio, "100000", "UNTIL_CLOSE", resto, bar) // a new period each round: nothing used
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make([]*apperr.Error, 2)
		for i, outlet := range []int64{resto, bar} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				pr, _ := auth.Require(p.admin)
				_ = pr
				results[i] = p.chargeNoFatal(p.admin, p.guestFolio, outlet, "60000")
			}()
		}
		close(start)
		wg.Wait()
		accepted, refused := 0, 0
		for _, r := range results {
			if r == nil {
				accepted++
			} else if r.Code == "OUTLET_LIMIT_EXCEEDED" || r.Code == "OUTLET_FOLIO_LOCKED" {
				refused++
			} else {
				t.Fatalf("round %d: unexpected %+v", round, r)
			}
		}
		if accepted != 1 || refused != 1 {
			t.Fatalf("round %d: %d accepted and %d refused, want one each", round, accepted, refused)
		}
		l, err := p.Folios.GetOutletLock(p.admin, p.propID, p.guestFolio)
		must(t, err)
		if l.Used.GreaterThan(dec("100000")) {
			t.Fatalf("round %d: used %s is over the limit", round, l.Used)
		}
		must(t, p.Exec(t, `UPDATE folio_outlet_locks SET status = 'LOCKED', locked_reason = 'MANUAL' WHERE folio_id = $1`, p.guestFolio))
	}
}

// chargeNoFatal is outletCharge for a goroutine: it returns the refusal, or a refusal-shaped error for anything else.
func (p *payerFx) chargeNoFatal(ctx context.Context, folio, outlet int64, amount string) *apperr.Error {
	pr, err := auth.Require(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	var refusal *apperr.Error
	err = p.TxM.WithinTx(ctx, func(ctx context.Context) error {
		day, err := p.Tenancy.RequireOpenBusinessDay(ctx, p.propID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, p.propID, []int64{folio}); err != nil {
			return err
		}
		c := folios.OutletChargeCheck{PropertyID: p.propID, BusinessDate: day.BusinessDate, TargetFolioID: folio, OutletID: outlet, Amount: dec(amount), Source: folios.ChargeSourceCharge}
		d, err := p.Folios.CheckOutletCharge(ctx, pr, c)
		if err != nil {
			return err
		}
		if d.Refusal != nil {
			refusal = d.Refusal
			return nil
		}
		_, err = p.Folios.RecordOutletCharge(ctx, pr, c, d, nil, nil)
		return err
	})
	if err != nil {
		if e, ok := apperr.As(err); ok {
			return e
		}
		return apperr.Internal(err)
	}
	return refusal
}

func TestTheHolderOfTransferAnyIsCheckedLikeEveryone(t *testing.T) {
	p := setupPayer(t)
	resto, _ := p.outlets(t)
	ctx := p.User(t, p.tenantID, p.propID, auth.PermFolioTransferAny, auth.PermFolioRead)
	if ref := p.outletCharge(t, ctx, p.guestFolio, resto, "1000"); ref == nil || ref.Code != "OUTLET_FOLIO_LOCKED" {
		t.Fatalf("folio.transfer_any widens where a front office transfer may go, never this: %+v", ref)
	}
}

func TestTheListOfIncomingOutletCharges(t *testing.T) {
	p := setupPayer(t)
	resto, bar := p.outlets(t)
	_ = p.outletCharge(t, p.admin, p.guestFolio, bar, "110000") // refused: never opened
	p.openLock(t, p.guestFolio, "300000", "UNTIL_CLOSE", resto)
	_ = p.outletCharge(t, p.admin, p.guestFolio, resto, "253750")
	list, err := p.Folios.ListOutletCharges(p.admin, p.propID, nil, "")
	must(t, err)
	if list.Accepted != 1 || list.Rejected != 1 || len(list.Data) != 2 {
		t.Fatalf("list: %+v", list)
	}
	first := list.Data[0] // newest first: the accepted one
	if first.Result != "ACCEPTED" || first.OutletCode != "RESTO" || first.RoomNumber != "101" || !first.Amount.Equal(dec("253750")) || first.BillNumber != "R-0001" {
		t.Fatalf("accepted row: %+v", first)
	}
	second := list.Data[1]
	var ctxv map[string]any
	_ = json.Unmarshal(second.Context, &ctxv)
	if second.Result != "REJECTED" || second.Code == nil || *second.Code != "OUTLET_FOLIO_LOCKED" || second.OutletCode != "BAR" || ctxv["reason"] != "NEVER_OPENED" {
		t.Fatalf("refused row: %+v", second)
	}
	only, err := p.Folios.ListOutletCharges(p.admin, p.propID, nil, "REJECTED")
	must(t, err)
	if len(only.Data) != 1 || only.Accepted != 0 {
		t.Fatalf("filter: %+v", only)
	}
	other := civil.MustParseDate("2020-01-01")
	none, err := p.Folios.ListOutletCharges(p.admin, p.propID, &other, "")
	must(t, err)
	if len(none.Data) != 0 {
		t.Fatalf("another day: %+v", none)
	}
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioPostCharge)
	_, err = p.Folios.ListOutletCharges(clerk, p.propID, nil, "")
	wantCode(t, err, "PERMISSION_DENIED")
}
