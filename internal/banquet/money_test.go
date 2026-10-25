package banquet_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

// The money of the banquet (docs/architecture/36-banquet.md sections 10.1 to 10.5, tests 5 to 8 of section 11). The property is the fixture of the other modules: IDR, no decimals, PB1 10% on the
// service charge and SVC 10% on the codes of the banquet, so the factor of a price with both is 1.21.

// outlets is the point of sale and the banquet at the close of the day, as the application wires it.
type outlets struct {
	svc *banquet.Service
	fo  *folios.Service
}

func (o outlets) NightAuditOutlets(ctx context.Context, tenantID, propertyID int64) (blockers, warnings []nightaudit.OutletIssue, err error) {
	bb, bw, err := o.svc.NightAuditChecks(ctx, tenantID, propertyID)
	for _, i := range bb {
		blockers = append(blockers, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	for _, i := range bw {
		warnings = append(warnings, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	return blockers, warnings, err
}

func (o outlets) ExpireOutletLocks(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	if _, err := o.svc.PostDueSessions(ctx, p, propertyID, bd); err != nil {
		return err
	}
	if _, err := o.fo.ExpireOutletLocks(ctx, p, propertyID, bd); err != nil {
		return err
	}
	_, err := o.svc.WriteDueNotices(ctx, p.TenantID, propertyID, bd)
	return err
}

type mfx struct {
	*fx
	tax, service                   int64
	fb, roomCode, equipCode        int64
	lunch, coffee, roomItem, equip banquet.Item
	pkg                            banquet.Package
	approver                       context.Context
	approverEmail                  string
}

func (f *fx) codeID(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func (f *fx) money(t *testing.T) *mfx {
	t.Helper()
	m := &mfx{fx: f}
	ctx := context.Background()
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO taxes (tenant_id, property_id, code, name, rate, tax_on_service) VALUES ($1, $2, 'PB1', 'PB1', 10, true) RETURNING id`, f.tenantID, f.propID).Scan(&m.tax))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO service_charges (tenant_id, property_id, code, name, rate) VALUES ($1, $2, 'SVC', 'Service', 10) RETURNING id`, f.tenantID, f.propID).Scan(&m.service))
	m.fb, m.roomCode, m.equipCode = f.codeID(t, "BQT_FB"), f.codeID(t, "BQT_ROOM"), f.codeID(t, "BQT_EQUIPMENT")
	for _, id := range []int64{m.fb, m.roomCode, m.equipCode} {
		_, err := f.Billing.ReplaceRules(f.admin, f.propID, id, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: m.tax, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: m.service, Sequence: 1}}})
		must(t, err)
	}
	item := func(code, name, kind string, charge int64, price string, class *string) banquet.Item {
		it, err := f.svc.CreateItem(f.mgr, f.propID, banquet.ItemInput{Code: code, Name: name, Kind: kind, UnitBasis: "PER_PAX", ChargeCodeID: charge, ListPrice: dec(price), FbClass: class})
		must(t, err)
		return it
	}
	m.lunch = item("LUNCH", "Lunch", "MENU", m.fb, "150000", ptr("FOOD"))
	m.coffee = item("COFFEE", "Coffee break", "MENU", m.fb, "40000", ptr("FOOD"))
	m.roomItem = item("ROOMR", "Room rental", "ROOM_RENTAL", m.roomCode, "50000", nil)
	m.equip = item("EQUIP", "Equipment", "EQUIPMENT", m.equipCode, "20000", nil)
	pk, err := f.svc.CreatePackage(f.mgr, f.propID, banquet.PackageInput{Code: "FULLDAY", Name: "Fullday Meeting", PricePerPax: dec("363000"), Components: []banquet.ComponentInput{
		{ItemID: m.lunch.ID, QtyPerPax: dec("1")}, {ItemID: m.coffee.ID, QtyPerPax: dec("2")}, {ItemID: m.roomItem.ID, QtyPerPax: dec("1")}, {ItemID: m.equip.ID, QtyPerPax: dec("1")}}})
	must(t, err)
	m.pkg = pk
	m.approver, m.approverEmail = f.Account(t, f.tenantID, f.propID, auth.PermBanquetConfirm, auth.PermBanquetManage)
	f.Audit.SetOutletChecker(outlets{svc: f.svc, fo: f.Folios})
	return m
}

func (m *mfx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: m.approverEmail, Password: roomstest.Password}
}

// eventOn makes a tentative event with one session on the date, with guaranteed guests and the hours 12:00 to 16:00 in room A.
func (m *mfx) eventOn(t *testing.T, name, date string, guaranteed int) (banquet.Event, int64) {
	t.Helper()
	ev := m.event(t, name)
	in := m.session(m.a.ID, date, "12:00", "16:00", false)
	if guaranteed > 0 {
		in.PaxGuaranteed = ptr(guaranteed)
	}
	ev, err := m.svc.AddSession(m.office, m.propID, ev.ID, in)
	must(t, err)
	ev, err = m.svc.MakeTentative(m.office, m.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-25")})
	must(t, err)
	return ev, ev.Sessions[0].ID
}

func (m *mfx) packageLine(t *testing.T, eventID, sessionID int64, mode string) banquet.LinesView {
	t.Helper()
	v, err := m.svc.AddLine(m.office, m.propID, eventID, banquet.LineInput{SessionID: sessionID, PackageID: &m.pkg.ID, PriceMode: mode})
	must(t, err)
	return v
}

func (m *mfx) confirm(t *testing.T, eventID int64) banquet.Event {
	t.Helper()
	ev, err := m.svc.Confirm(m.mgr, m.propID, eventID, banquet.ConfirmInput{})
	must(t, err)
	return ev
}

func (m *mfx) folioOf(t *testing.T, eventID int64) banquet.FolioRef {
	t.Helper()
	v, err := m.svc.GetFolio(m.mgr, m.propID, eventID)
	must(t, err)
	return v
}

func (m *mfx) pay(t *testing.T, folioID int64, amount, key string) folios.PaymentResult {
	t.Helper()
	r, err := m.Folios.PostPayment(m.admin, m.propID, folioID, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
	must(t, err)
	return r
}

func (m *mfx) gl(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, m.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE l.property_id = $1 AND a.code = $2`, m.propID, code).Scan(&s))
	return dec(s)
}

func (m *mfx) closeDay(t *testing.T) {
	t.Helper()
	d, err := m.Tenancy.CurrentBusinessDay(m.admin, m.propID)
	must(t, err)
	m.Clock.Set(time.Date(d.BusinessDate.Year(), d.BusinessDate.Month(), d.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1)) // 00:30 local of the next day
	_, err = m.Audit.Run(m.admin, m.propID, d.BusinessDate)
	must(t, err)
}

func (m *mfx) requireBalanced(t *testing.T) {
	t.Helper()
	var dr, cr string
	must(t, m.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM gl_journal_lines WHERE property_id = $1`, m.propID).Scan(&dr, &cr))
	if dr != cr || dr == "0" {
		t.Fatalf("the books do not balance, or are empty: debit %s credit %s", dr, cr)
	}
}

// ---------------------------------------------------------------- 10.1: the package, nett, 100 guests

func TestThePackageOfSection10_1IsEstimatedPostedAndJournalled(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Fullday", "2026-09-30", 100)
	v := m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	est := v.Estimate
	if !est.Net.Equal(dec("30000000")) || !est.Service.Equal(dec("3000000")) || !est.Tax.Equal(dec("3300000")) || !est.Total.Equal(dec("36300000")) {
		t.Fatalf("estimate: %+v", est)
	}
	m.confirm(t, ev.ID)
	res, err := m.svc.PostSession(m.office, m.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	if res.Posting.PaxBilled != 100 || res.Posting.PaxBasis != banquet.BasisGuaranteed || !res.Total.Equal(dec("36300000")) || len(res.Charges) != 4 {
		t.Fatalf("posting: %+v", res)
	}
	nets := []string{"15000000", "8000000", "5000000", "2000000"}
	for i, c := range res.Charges {
		if !c.Net.Equal(dec(nets[i])) {
			t.Fatalf("component %d: net %s, want %s", i, c.Net, nets[i])
		}
	}
	// the posted money is what was estimated (one plan for both)
	after, err := m.svc.Estimate(m.mgr, m.propID, ev.ID)
	must(t, err)
	if !after.Total.Equal(est.Total) || !after.Net.Equal(est.Net) {
		t.Fatalf("estimate after the posting: %+v", after)
	}
	m.closeDay(t)
	m.requireBalanced(t)
	for code, want := range map[string]string{"1210": "36300000", "4240": "-23000000", "4410": "-7000000", "2430": "-3000000", "2410": "-3300000"} {
		if got := m.gl(t, code); !got.Equal(dec(want)) {
			t.Fatalf("account %s: %s, want %s", code, got, want)
		}
	}
}

func TestTheRoundingCaseOfSection10_1TotalsExactlyThePriceOfThePackage(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	item := func(code string) banquet.ComponentInput {
		it, err := f.svc.CreateItem(f.mgr, f.propID, banquet.ItemInput{Code: code, Name: code, Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: m.fb, ListPrice: dec("10")})
		must(t, err)
		return banquet.ComponentInput{ItemID: it.ID, QtyPerPax: dec("1")}
	}
	pk, err := f.svc.CreatePackage(f.mgr, f.propID, banquet.PackageInput{Code: "THIRDS", Name: "Thirds", PricePerPax: dec("100000"), Components: []banquet.ComponentInput{item("T1"), item("T2"), item("T3")}})
	must(t, err)
	ev, sid := m.eventOn(t, "One guest", "2026-09-30", 1)
	v, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, PackageID: &pk.ID, PriceMode: banquet.PriceInclusive})
	must(t, err)
	if !v.Estimate.Total.Equal(dec("100000")) {
		t.Fatalf("the package of 100,000 nett for one guest comes to %s", v.Estimate.Total)
	}
	m.confirm(t, ev.ID)
	res, err := f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	if !res.Total.Equal(dec("100000")) {
		t.Fatalf("posted %s", res.Total)
	}
}

// ---------------------------------------------------------------- price modes, frozen prices, discounts

func TestNettAndPlusPlusForTheSamePriceAndMixedModesInOneEvent(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Modes", "2026-10-05", 10)
	one := dec("1")
	price := dec("100000")
	add := func(mode string) banquet.LinesView {
		v, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID, Quantity: &one, UnitPrice: &price, PriceMode: mode})
		must(t, err)
		return v
	}
	nett := add(banquet.PriceInclusive)
	if !nett.Estimate.Total.Equal(dec("1000000")) || !nett.Estimate.Net.Equal(dec("826446")) && !nett.Estimate.Net.Equal(dec("826447")) {
		t.Fatalf("nett: %+v", nett.Estimate)
	}
	both := add(banquet.PriceExclusive)
	// 10 guests x 100,000 at ++ is 1,000,000 net and 1,210,000 with the service and the tax
	if got := both.Estimate.Total.Sub(nett.Estimate.Total); !got.Equal(dec("1210000")) {
		t.Fatalf("the ++ line adds %s", got)
	}
}

func TestTheModeAndThePriceOfALineAreFrozenWhenTheMasterChanges(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Frozen", "2026-10-05", 10)
	v, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID})
	must(t, err)
	line := v.Lines[0]
	if line.PriceMode != banquet.PriceExclusive || !line.UnitPrice.Equal(dec("150000")) {
		t.Fatalf("the new line starts from the setting and the master: %+v", line)
	}
	_, err = f.svc.PutSettings(f.mgr, f.propID, banquet.SettingsInput{TentativeConflictMode: banquet.ModeReject, DefaultPriceMode: banquet.PriceInclusive})
	must(t, err)
	_, err = f.svc.UpdateItem(f.mgr, f.propID, m.lunch.ID, banquet.ItemInput{Name: "Lunch", Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: m.fb, ListPrice: dec("999000")})
	must(t, err)
	v, err = f.svc.ListLines(f.mgr, f.propID, ev.ID)
	must(t, err)
	if v.Lines[0].PriceMode != banquet.PriceExclusive || !v.Lines[0].UnitPrice.Equal(dec("150000")) {
		t.Fatalf("a master and a setting changed, the line did not: %+v", v.Lines[0])
	}
	v2, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.coffee.ID})
	must(t, err)
	if v2.Lines[1].PriceMode != banquet.PriceInclusive {
		t.Fatalf("the next line takes the new setting: %+v", v2.Lines[1])
	}
	// a package freezes its components: a new list price changes no share
	pv, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, PackageID: &m.pkg.ID})
	must(t, err)
	_, err = f.svc.UpdateItem(f.mgr, f.propID, m.coffee.ID, banquet.ItemInput{Name: "Coffee break", Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: m.fb, ListPrice: dec("1")})
	must(t, err)
	again, err := f.svc.ListLines(f.mgr, f.propID, ev.ID)
	must(t, err)
	if !again.Estimate.Total.Equal(pv.Estimate.Total) {
		t.Fatalf("a master changed the estimate: %s then %s", pv.Estimate.Total, again.Estimate.Total)
	}
}

func TestADiscountAboveTheLimitNeedsTheApprovalOfTheRule(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	_, err := f.svc.PutSettings(f.mgr, f.propID, banquet.SettingsInput{TentativeConflictMode: banquet.ModeReject, MaxDiscountPctNoApproval: ptr(dec("5"))})
	must(t, err)
	ev, sid := m.eventOn(t, "Discounts", "2026-10-05", 10)
	in := banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID}
	in.DiscountPct = ptr(dec("3"))
	v, err := f.svc.AddLine(f.office, f.propID, ev.ID, in)
	must(t, err)
	if v.Lines[0].DiscountBy == nil || v.Lines[0].DiscountApprovedBy != nil {
		t.Fatalf("a discount below the limit needs no approval: %+v", v.Lines[0])
	}
	in.DiscountPct = ptr(dec("8"))
	_, err = f.svc.AddLine(f.office, f.propID, ev.ID, in)
	_ = code(t, err, "APPROVAL_REQUIRED")
	in.Approval = m.approval()
	v, err = f.svc.AddLine(f.office, f.propID, ev.ID, in)
	must(t, err)
	if v.Lines[1].DiscountApprovedBy == nil {
		t.Fatalf("the approver is on the line: %+v", v.Lines[1])
	}
	// a discount in amount is a percent of the base of the line
	in = banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID, DiscountAmount: ptr(dec("200000"))}
	_, err = f.svc.AddLine(f.office, f.propID, ev.ID, in)
	_ = code(t, err, "APPROVAL_REQUIRED")
	in.DiscountPct = ptr(dec("1"))
	_, err = f.svc.AddLine(f.office, f.propID, ev.ID, in)
	_ = code(t, err, "VALIDATION_FAILED")
	// DISTINCT: the person who gives the discount cannot approve it
	f.SetRule(t, f.tenantID, f.propID, string(iam.KindBanquetDiscount), "DISTINCT")
	selfCtx, selfEmail := f.Account(t, f.tenantID, f.propID, auth.PermBanquetConfirm, auth.PermBanquetManage)
	in = banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID, DiscountPct: ptr(dec("9")), Approval: &iam.ApprovalInput{Email: selfEmail, Password: roomstest.Password}}
	_, err = f.svc.AddLine(selfCtx, f.propID, ev.ID, in)
	_ = code(t, err, "APPROVAL_SAME_USER")
	_, err = f.svc.AddLine(selfCtx, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID, DiscountPct: ptr(dec("9")), Approval: m.approval()})
	must(t, err)
}

func TestASessionThatIsPostedCannotBeChangedOrHaveLinesAdded(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Posted", "2026-09-30", 10)
	v, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID})
	must(t, err)
	m.confirm(t, ev.ID)
	_, err = f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	_, err = f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.coffee.ID})
	_ = code(t, err, "SESSION_POSTED")
	_, err = f.svc.RemoveLine(f.office, f.propID, ev.ID, v.Lines[0].ID)
	_ = code(t, err, "SESSION_POSTED")
	_, err = f.svc.CancelSession(f.office, f.propID, ev.ID, sid)
	_ = code(t, err, "SESSION_POSTED")
	in := f.session(f.a.ID, "2026-09-30", "12:00", "16:00", false)
	_, err = f.svc.UpdateSession(f.office, f.propID, ev.ID, sid, in)
	_ = code(t, err, "SESSION_POSTED")
}

// ---------------------------------------------------------------- 10.5 and the rules of posting

func TestTheBilledGuestsAreTheLargerNumberAndAFutureSessionIsNotPosted(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Guests", "2026-09-30", 100)
	_, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID})
	must(t, err)
	m.confirm(t, ev.ID)
	_, err = f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{PaxActual: ptr(104)})
	must(t, err)
	var pax int
	var basis string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT pax_billed, pax_basis FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&pax, &basis))
	if pax != 104 || basis != banquet.BasisActual {
		t.Fatalf("billed %d on %s", pax, basis)
	}
	_, err = f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	_ = code(t, err, "SESSION_ALREADY_POSTED")

	ev2, sid2 := m.eventOn(t, "Later", "2026-10-02", 10)
	m.confirm(t, ev2.ID)
	_, err = f.svc.PostSession(f.office, f.propID, ev2.ID, sid2, banquet.PostInput{})
	_ = code(t, err, "SESSION_NOT_DUE")
}

func TestASessionWithNoGuestsIsBlockedForTheCloseAndPostedByAPersonOnTheEstimate(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "No number", "2026-09-30", 0)
	_, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID})
	must(t, err)
	m.confirm(t, ev.ID)
	_, err = f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	_ = code(t, err, "NO_GUARANTEED_GUESTS")
}
