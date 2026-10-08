package accounting_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/reservations"
)

// Audit F-09: what a transfer of a charge between the folios of a stay does to the books, and the company-pays path from the booking to the check-out.
//
// A transfer moves the ownership of a charge: a reversal on the source folio and a copy on the target. The books must not notice. The way to prove it is a counterfactual inside one
// property: day 1 has a charge that stays where it is, day 2 has the same charge that is transferred, and the two day-close journals must be line for line the same (account,
// department, source), not merely balanced.

// withTaxes: the minibar carries 10% service and 11% VAT on it (100,000 becomes 122,100) and belongs to the Food and beverage department.
func (h *hotel) withTaxes(t *testing.T) (minibar int64) {
	t.Helper()
	svc, err := h.Billing.CreateServiceCharge(h.admin, h.propID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	vat, err := h.Billing.CreateTax(h.admin, h.propID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, IsActive: true})
	must(t, err)
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, h.propID).Scan(&minibar))
	_, err = h.Billing.ReplaceRules(h.admin, h.propID, minibar, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	})
	must(t, err)
	must(t, h.Exec(t, `UPDATE charge_codes SET department_id = $2 WHERE id = $1`, minibar, h.deptID(t, "FB")))
	return minibar
}

// aStayBilledToACompany: booked, the room line billed to a company, checked in. The stay has its guest folio and the company folio.
type companyStay struct {
	res          reservations.Reservation
	stay         frontdesk.CheckInResult
	company      int64
	guestFolio   int64
	companyFolio int64
}

func (h *hotel) aStayBilledToACompany(t *testing.T, arrival, departure string, instructionFirst bool) companyStay {
	t.Helper()
	var s companyStay
	s.res = h.book(t, arrival, departure)
	co, err := h.Companies.Create(h.admin, h.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	s.company = co.ID
	instruct := func() {
		_, err := h.Folios.SetBillingInstructions(h.admin, h.propID, s.res.ID, s.res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: co.ID}})
		must(t, err)
	}
	if instructionFirst {
		instruct()
	}
	s.stay = h.checkIn(t, h.reload(t, s.res.ID), h.r101)
	if !instructionFirst {
		instruct()
	}
	s.guestFolio = s.stay.Folio.ID
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY' AND bill_to_company_id = $2`, s.stay.Stay.ID, co.ID).Scan(&s.companyFolio))
	return s
}

func (h *hotel) chargeID(t *testing.T, folio int64, code, unit, key string) int64 {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, h.propID, code).Scan(&id))
	r, err := h.Folios.PostCharge(h.admin, h.propID, folio, key, folios.ChargeInput{ChargeCodeID: id, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	return r.Item.ID
}

func (h *hotel) transfer(t *testing.T, item, to int64) folios.TransferItemResult {
	t.Helper()
	r, err := h.Folios.TransferItem(h.admin, h.propID, item, folios.TransferItemInput{FolioID: to, Reason: "company pays", Approval: h.approval()})
	must(t, err)
	return r
}

// dayJournal: the day-close journal of a business date, net (debit less credit) by account, department, source type and source reference.
func (h *hotel) dayJournal(t *testing.T, date string) map[string]string {
	t.Helper()
	rows, err := h.Pool.Query(context.Background(), `SELECT a.code, COALESCE(dp.code, ''), COALESCE(l.source_type, ''), COALESCE(l.source_ref, ''), sum(l.debit - l.credit)::text
		FROM gl_journal_lines l
		JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
		JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		LEFT JOIN departments dp ON dp.property_id = l.property_id AND dp.id = l.department_id
		WHERE l.property_id = $1 AND j.journal_type = 'DAY_CLOSE' AND j.journal_date = $2
		GROUP BY 1, 2, 3, 4`, h.propID, date)
	must(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var acct, dept, st, ref, net string
		must(t, rows.Scan(&acct, &dept, &st, &ref, &net))
		v := decimal.RequireFromString(net)
		if v.IsZero() {
			continue
		}
		out[fmt.Sprintf("%s|%s|%s|%s", acct, dept, st, ref)] = v.String()
	}
	if len(out) == 0 {
		t.Fatalf("no day-close journal for %s", date)
	}
	return out
}

func sameJournal(t *testing.T, want, got map[string]string) {
	t.Helper()
	var keys []string
	seen := map[string]bool{}
	for k := range want {
		keys, seen[k] = append(keys, k), true
	}
	for k := range got {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var diff []string
	for _, k := range keys {
		if want[k] != got[k] {
			diff = append(diff, fmt.Sprintf("%s: day without the transfer %q, day with it %q", k, want[k], got[k]))
		}
	}
	if len(diff) > 0 {
		t.Fatalf("the two day-close journals differ:\n%s", strings.Join(diff, "\n"))
	}
}

// 1. Transfer then PostDay: the journal is the one of a day without the transfer, per account and per department.
func TestATransferLeavesTheDayJournalExactlyAsItWasPerAccountAndDepartment(t *testing.T) {
	h := setupHotel(t)
	h.withTaxes(t)
	s := h.aStayBilledToACompany(t, "2026-09-30", "2026-10-03", true)

	// day 1: the minibar stays on the guest folio
	h.chargeID(t, s.guestFolio, "MINIBAR", "100000", "x")
	h.closeDay(t)
	// day 2: the same charge, moved to the company folio
	y := h.chargeID(t, s.guestFolio, "MINIBAR", "100000", "y")
	moved := h.transfer(t, y, s.companyFolio)
	if moved.Charge.Item.FolioID != s.companyFolio || moved.Reversal.Item.FolioID != s.guestFolio {
		t.Fatalf("transfer: %+v", moved)
	}
	h.closeDay(t)

	day1, day2 := h.dayJournal(t, "2026-09-30"), h.dayJournal(t, "2026-10-01")
	sameJournal(t, day1, day2)
	// the journal is not trivially equal: the minibar revenue sits in the department of its charge code
	if day1["4230|FB|CHARGE_CODE|MINIBAR"] != "-100000" {
		t.Fatalf("minibar revenue of day 1: %v", day1)
	}
	// no revenue moved to another department
	for k, v := range day2 {
		if strings.HasPrefix(k, "4230|") && !strings.HasPrefix(k, "4230|FB|") {
			t.Fatalf("minibar revenue in another department: %s %s", k, v)
		}
	}
	h.requireBalanced(t)
	if got := h.folioBalance(t, s.guestFolio).Add(h.folioBalance(t, s.companyFolio)); got.IsZero() {
		t.Fatal("the folios hold the charges")
	}
	// the reversal and the copy exist as ledger rows (the books are not equal because nothing was written)
	if n := h.Count(t, `SELECT count(*) FROM folio_items WHERE reference_type = 'FOLIO_TRANSFER' AND reference_id = $1`, fmt.Sprint(y)); n != 2 {
		t.Fatalf("%d transfer rows", n)
	}
}

// 2. A transfer of an INCLUSIVE charge with service and tax moves the ownership and recomputes nothing.
func TestATransferOfAnInclusiveChargeCopiesTheSnapshotAndRecomputesNothing(t *testing.T) {
	h := setupHotel(t)
	minibar := h.withTaxes(t)
	s := h.aStayBilledToACompany(t, "2026-09-30", "2026-10-03", true)
	mode := "INCLUSIVE"
	gross := "122100"
	posted, err := h.Folios.PostCharge(h.admin, h.propID, s.guestFolio, "inc", folios.ChargeInput{ChargeCodeID: minibar, Quantity: "1", UnitPrice: &gross, PriceMode: &mode})
	must(t, err)
	orig := posted.Item.ID

	snapshot := func(id int64) string {
		var v string
		must(t, h.Pool.QueryRow(context.Background(), `SELECT to_jsonb(i)::text FROM folio_items i WHERE id = $1`, id).Scan(&v))
		return v
	}
	numbers := func(id int64) string {
		var v string
		must(t, h.Pool.QueryRow(context.Background(), `SELECT concat_ws('|', price_mode, quantity::text, unit_price::text, base_amount::text, discount_amount::text, net_amount::text, service_charge_total::text, tax_total::text,
			debit::text, credit::text, charge_code_id::text, COALESCE(revenue_account_code, ''), COALESCE(department_id::text, ''), service_date::text) FROM folio_items WHERE id = $1`, id).Scan(&v))
		return v
	}
	components := func(id int64) string {
		var v string
		must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(string_agg(concat_ws('/', component_type, code, rate::text, base_amount::text, amount::text, sequence::text), ';' ORDER BY sequence, component_type), '') FROM folio_item_components WHERE folio_item_id = $1`, id).Scan(&v))
		return v
	}
	before, beforeNumbers, beforeComponents := snapshot(orig), numbers(orig), components(orig)
	if !strings.HasPrefix(beforeNumbers, "INCLUSIVE|") || beforeComponents == "" {
		t.Fatalf("an inclusive charge with its components: %s %s", beforeNumbers, beforeComponents)
	}
	var net, svc, tax, debit string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT net_amount::text, service_charge_total::text, tax_total::text, debit::text FROM folio_items WHERE id = $1`, orig).Scan(&net, &svc, &tax, &debit))
	if debit != "122100.000" && debit != "122100" {
		t.Fatalf("the gross is what the guest pays: %s", debit)
	}
	// the rates change after the posting: a recomputation would show it
	must(t, h.Exec(t, `UPDATE taxes SET rate = 20 WHERE property_id = $1`, h.propID))
	must(t, h.Exec(t, `UPDATE service_charges SET rate = 5 WHERE property_id = $1`, h.propID))

	folioTotal := h.folioBalance(t, s.guestFolio).Add(h.folioBalance(t, s.companyFolio))
	moved := h.transfer(t, orig, s.companyFolio)

	if snapshot(orig) != before {
		t.Fatal("the original row is immutable: a transfer does not touch it")
	}
	copyID, revID := moved.Charge.Item.ID, moved.Reversal.Item.ID
	if numbers(copyID) != beforeNumbers {
		t.Fatalf("the copy keeps every number of the original:\n  original %s\n  copy     %s", beforeNumbers, numbers(copyID))
	}
	if components(copyID) != beforeComponents {
		t.Fatalf("components: original %s, copy %s", beforeComponents, components(copyID))
	}
	// the reversal carries the components of the original with the sign turned, rate and sequence as they were
	if mirrored := h.Count(t, `SELECT count(*) FROM folio_item_components r JOIN folio_item_components o ON o.component_type = r.component_type AND o.code = r.code AND o.rate = r.rate AND o.sequence = r.sequence
		AND o.base_amount = -r.base_amount AND o.amount = -r.amount WHERE r.folio_item_id = $1 AND o.folio_item_id = $2`, revID, orig); mirrored != strings.Count(beforeComponents, ";")+1 {
		t.Fatalf("the reversal mirrors the components of the original: %d of %q", mirrored, beforeComponents)
	}
	neg := func(a, b string) bool { return decimal.RequireFromString(a).Equal(decimal.RequireFromString(b).Neg()) }
	var rNet, rSvc, rTax, rDebit, rCredit, rMode string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT net_amount::text, service_charge_total::text, tax_total::text, debit::text, credit::text, price_mode FROM folio_items WHERE id = $1`, revID).Scan(&rNet, &rSvc, &rTax, &rDebit, &rCredit, &rMode))
	if !neg(rNet, net) || !neg(rSvc, svc) || !neg(rTax, tax) || !decimal.RequireFromString(rCredit).Equal(decimal.RequireFromString(debit)) || !decimal.RequireFromString(rDebit).IsZero() || rMode != "INCLUSIVE" {
		t.Fatalf("the reversal mirrors the snapshot of the original with the sign turned: net %s service %s tax %s debit %s credit %s mode %s", rNet, rSvc, rTax, rDebit, rCredit, rMode)
	}
	if now := h.folioBalance(t, s.guestFolio).Add(h.folioBalance(t, s.companyFolio)); !now.Equal(folioTotal) {
		t.Fatalf("the total of the folios is the same after the transfer: %s then %s", folioTotal, now)
	}
	if !h.folioBalance(t, s.guestFolio).IsZero() || !h.folioBalance(t, s.companyFolio).Equal(decimal.RequireFromString("122100")) {
		t.Fatalf("guest %s company %s", h.folioBalance(t, s.guestFolio), h.folioBalance(t, s.companyFolio))
	}
	// no tax or service was calculated again: one set of components for each of the three rows, each with the rates of the day of the posting
	if n := h.Count(t, `SELECT count(*) FROM folio_item_components WHERE folio_item_id IN ($1, $2, $3)`, orig, copyID, revID); n != 3*strings.Count(beforeComponents, ";")+3 {
		t.Fatalf("%d components for three rows of %q", n, beforeComponents)
	}
	h.closeDay(t)
	h.requireBalanced(t)
}

// 3. The room night through a chain of transfers: one night, one live charge, the register always on it, a posting run adds nothing.
func TestARoomNightMovedTwiceIsStillOneChargeAndPostingAgainAddsNothing(t *testing.T) {
	h := setupHotel(t)
	s := h.aStayBilledToACompany(t, "2026-09-30", "2026-10-02", false)
	bd := d("2026-09-30")

	run := func() []string {
		res, err := h.Charges.PostManual(h.admin, h.propID, bd, nil)
		must(t, err)
		var out []string
		for _, r := range res.Results {
			out = append(out, r.Status)
		}
		return out
	}
	if got := run(); len(got) != 1 || got[0] != "POSTED" {
		t.Fatalf("the first run: %v", got)
	}
	var a int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_item_id FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, s.stay.Stay.ID).Scan(&a))
	live := func() (int, int64, int64) {
		t.Helper()
		var folio, item int64
		var n int
		must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(max(i.folio_id), 0), COALESCE(max(i.id), 0) FROM folio_items i
			WHERE i.stay_id = $1 AND i.transaction_type = 'CHARGE' AND NOT EXISTS (SELECT 1 FROM folio_items r WHERE r.reverses_item_id = i.id)`, s.stay.Stay.ID).Scan(&n, &folio, &item))
		return n, folio, item
	}
	registered := func() (int, int64) {
		t.Helper()
		var n int
		var item int64
		must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(max(folio_item_id), 0) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, s.stay.Stay.ID).Scan(&n, &item))
		return n, item
	}
	if n, folio, item := live(); n != 1 || folio != s.companyFolio || item != a {
		t.Fatalf("the night is on the company folio: %d %d %d", n, folio, item)
	}

	// A (company folio) -> B (guest folio) -> C (company folio)
	b := h.transfer(t, a, s.guestFolio).Charge.Item.ID
	if n, item := registered(); n != 1 || item != b {
		t.Fatalf("the register follows the first transfer: %d %d (want %d)", n, item, b)
	}
	if got := run(); len(got) != 1 || got[0] != "ALREADY_POSTED" {
		t.Fatalf("a run after the first transfer: %v", got)
	}
	c := h.transfer(t, b, s.companyFolio).Charge.Item.ID
	if n, item := registered(); n != 1 || item != c {
		t.Fatalf("the register follows the second transfer: %d %d (want %d)", n, item, c)
	}
	if got := run(); len(got) != 1 || got[0] != "ALREADY_POSTED" {
		t.Fatalf("a run after the chain: %v", got)
	}
	if n, folio, item := live(); n != 1 || folio != s.companyFolio || item != c {
		t.Fatalf("one live charge, the last copy, on the company folio: %d %d %d", n, folio, item)
	}
	// the reversals are not nights: three charges and two reversals in the ledger, one night in the register, no row of the register for a reversal
	if charges, reversals := h.Count(t, `SELECT count(*) FROM folio_items WHERE stay_id = $1 AND transaction_type = 'CHARGE'`, s.stay.Stay.ID), h.Count(t, `SELECT count(*) FROM folio_items WHERE stay_id = $1 AND transaction_type = 'REVERSAL'`, s.stay.Stay.ID); charges != 3 || reversals != 2 {
		t.Fatalf("%d charges and %d reversals", charges, reversals)
	}
	if n := h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED' AND folio_item_id IN (SELECT id FROM folio_items WHERE transaction_type = 'REVERSAL')`, s.stay.Stay.ID); n != 0 {
		t.Fatalf("%d reversals registered as a night", n)
	}
	if !h.folioBalance(t, s.guestFolio).IsZero() || !h.folioBalance(t, s.companyFolio).Equal(decimal.RequireFromString("1000000")) {
		t.Fatalf("guest %s company %s", h.folioBalance(t, s.guestFolio), h.folioBalance(t, s.companyFolio))
	}
	// the night audit posts again (it is idempotent) and closes: the journal holds the night once
	h.closeDay(t)
	h.requireBalanced(t)
	if got := h.balance(t, "4110"); !got.Equal(decimal.RequireFromString("-1000000")) {
		t.Fatalf("room revenue after the chain: %s, want one night", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED' AND service_date = '2026-09-30'`, s.stay.Stay.ID); n != 1 {
		t.Fatalf("the night is registered %d times", n)
	}
}

// 4. The company pays the room: from the instruction on the booking to the check-out.
func TestTheCompanyPaysTheRoomFromTheBookingToTheCheckOut(t *testing.T) {
	h := setupHotel(t)
	s := h.aStayBilledToACompany(t, "2026-09-30", "2026-10-02", true) // the instruction is on the line before the check-in

	companyFolios := func() int {
		return h.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY'`, s.stay.Stay.ID)
	}
	if companyFolios() != 1 || h.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND folio_type = 'GUEST'`, s.stay.Stay.ID) != 1 {
		t.Fatal("the check-in opened the guest folio and the company folio the instruction names")
	}
	roomDept := ""
	_ = h.Pool.QueryRow(context.Background(), `SELECT COALESCE(d.code, '') FROM charge_codes cc LEFT JOIN departments d ON d.property_id = cc.property_id AND d.id = cc.department_id WHERE cc.property_id = $1 AND cc.code = 'ROOM'`, h.propID).Scan(&roomDept)
	items := func(folio int64) int {
		return h.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE'`, folio)
	}

	// night 1 (30 Sep): to the company folio, nothing on the guest folio; the books and the register agree
	h.closeDay(t)
	if items(s.companyFolio) != 1 || items(s.guestFolio) != 0 {
		t.Fatalf("night 1: company %d, guest %d", items(s.companyFolio), items(s.guestFolio))
	}
	h.requireBalanced(t)
	day1 := h.dayJournal(t, "2026-09-30")
	if day1["4110|"+roomDept+"|CHARGE_CODE|ROOM"] != "-1000000" {
		t.Fatalf("room revenue by the department of the room charge code (%q): %v", roomDept, day1)
	}
	var night1 int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_item_id FROM stay_charge_postings WHERE stay_id = $1 AND service_date = '2026-09-30' AND status = 'POSTED'`, s.stay.Stay.ID).Scan(&night1))

	// the company then asks for night 1 on the guest's own bill: a transfer; the routing of the next nights stays what the instruction says
	copyOfNight1 := h.transfer(t, night1, s.guestFolio).Charge.Item.ID
	h.closeDay(t) // 1 Oct: night 2
	if items(s.companyFolio) != 2 || items(s.guestFolio) != 1 {
		t.Fatalf("after the transfer and night 2: company %d charges (night 1 and its reversal-free count includes the moved original and night 2), guest %d", items(s.companyFolio), items(s.guestFolio))
	}
	if n := h.Count(t, `SELECT count(*) FROM folio_items i WHERE i.folio_id = $1 AND i.transaction_type = 'CHARGE' AND NOT EXISTS (SELECT 1 FROM folio_items r WHERE r.reverses_item_id = i.id)`, s.companyFolio); n != 1 {
		t.Fatalf("one live charge on the company folio (night 2), got %d", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, s.stay.Stay.ID); n != 2 {
		t.Fatalf("two nights in the register, got %d", n)
	}
	var item1, item2 int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_item_id FROM stay_charge_postings WHERE stay_id = $1 AND service_date = '2026-09-30' AND status = 'POSTED'`, s.stay.Stay.ID).Scan(&item1))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_item_id FROM stay_charge_postings WHERE stay_id = $1 AND service_date = '2026-10-01' AND status = 'POSTED'`, s.stay.Stay.ID).Scan(&item2))
	var f1, f2 int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_id FROM folio_items WHERE id = $1`, item1).Scan(&f1))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_id FROM folio_items WHERE id = $1`, item2).Scan(&f2))
	if item1 != copyOfNight1 || f1 != s.guestFolio || f2 != s.companyFolio {
		t.Fatalf("night 1 is the copy on the guest folio, night 2 is on the company folio: %d %d (copy %d) / %d", item1, f1, copyOfNight1, f2)
	}
	// day 2 of the books: only night 2 of revenue (the transfer of night 1 nets to zero in the same account and department)
	day2 := h.dayJournal(t, "2026-10-01")
	if day2["4110|"+roomDept+"|CHARGE_CODE|ROOM"] != "-1000000" {
		t.Fatalf("room revenue of day 2 is one night: %v", day2)
	}
	h.requireBalanced(t)

	// check out on the 2nd: each folio is paid and closed, and no second company folio appears
	for _, f := range []int64{s.guestFolio, s.companyFolio} {
		if bal := h.folioBalance(t, f); bal.IsPositive() {
			_, err := h.Folios.PostPayment(h.admin, h.propID, f, fmt.Sprintf("pay%d", f), folios.PaymentInput{Amount: bal.String(), PaymentMethod: "CASH"})
			must(t, err)
		}
	}
	detail, err := h.Front.GetStay(h.admin, h.propID, s.stay.Stay.ID)
	must(t, err)
	out, err := h.Front.CheckOut(h.admin, h.propID, s.stay.Stay.ID, frontdesk.CheckOutInput{Version: detail.Stay.Version})
	must(t, err)
	if len(out.Folios) != 2 {
		t.Fatalf("both folios close: %+v", out.Folios)
	}
	if companyFolios() != 1 {
		t.Fatalf("%d company folios after the check-out", companyFolios())
	}
	if n := h.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND status = 'OPEN'`, s.stay.Stay.ID); n != 0 {
		t.Fatalf("%d folios still open after the check-out", n)
	}
	h.requireBalanced(t)
}
