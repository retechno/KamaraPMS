package inventory_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
)

// The stock count and the freeze (35-inventory.md 4.7, 5.5, 8.6): start freezes, the server stamps counted_at, the variance is counted minus what was expected, a gain is valued at the last cost and a loss by
// the method, a shortfall is closed by the count, the approval is a decision under the rule INV_COUNT, a cancel ends the freeze.

func (m *mfx) startCount(t *testing.T, wh string, scope string, mut ...func(in *inventory.CountInput)) inventory.Count {
	t.Helper()
	in := inventory.CountInput{ClientUUID: m.id(), WarehouseID: m.wh[wh], Scope: scope}
	for _, f := range mut {
		f(&in)
	}
	c, err := m.svc.StartCount(m.cnt, m.propID, in)
	must(t, err)
	return c
}

func (m *mfx) found(t *testing.T, c inventory.Count, item, qty string) inventory.Count {
	t.Helper()
	q := dec(qty)
	out, err := m.svc.EnterCountLines(m.cnt, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item[item], Qty: &q}})
	must(t, err)
	return out
}

func lineOf(t *testing.T, c inventory.Count, itemID int64) inventory.CountLine {
	t.Helper()
	for _, l := range c.Lines {
		if l.ItemID == itemID {
			return l
		}
	}
	t.Fatalf("no line for item %d", itemID)
	return inventory.CountLine{}
}

func (m *mfx) submitCount(t *testing.T, c inventory.Count) inventory.Count {
	t.Helper()
	out, err := m.svc.SubmitCount(m.cnt, m.propID, c.ID)
	must(t, err)
	return out
}

func (m *mfx) approveCount(t *testing.T, c inventory.Count) inventory.Count {
	t.Helper()
	out, err := m.svc.ApproveCount(m.apr, m.propID, c.ID)
	must(t, err)
	return out
}

// movementOf reads the movement of the kind a count line made.
func (m *mfx) countMovement(t *testing.T, countID int64, kind string) (qty, value, debit, credit string) {
	t.Helper()
	must(t, m.Pool.QueryRow(context.Background(), `SELECT qty_delta::text, value_delta::text, COALESCE(gl_debit_account_id, 0)::text, COALESCE(gl_credit_account_id, 0)::text FROM inv_movements WHERE source_type = 'INV_COUNT' AND source_id = $1 AND kind = $2`,
		countID, kind).Scan(&qty, &value, &debit, &credit))
	return
}

func TestStartingACountFreezesTheWarehouseForTheManualMovementsOnly(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	uuid := m.id()
	c := m.startCount(t, "HK", inventory.ScopeAll, func(in *inventory.CountInput) { in.ClientUUID = uuid })
	if c.Status != inventory.CountOpen || len(c.Lines) != 1 || c.Number == "" {
		t.Fatalf("count: %+v", c)
	}
	if !c.Blind || c.Lines[0].BookQty != nil {
		t.Fatalf("the sheet is blind while the count is open: %+v", c.Lines[0])
	}
	// every manual mutation of the frozen warehouse is refused, in either direction
	_, err := m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SOAP", "1"))
	wantCode(t, err, "WAREHOUSE_FROZEN")
	_, err = m.transfer(t, m.mover, "HK", "MAIN", nil, m.tl("SOAP", "1"))
	wantCode(t, err, "WAREHOUSE_FROZEN")
	m.open(t, "MAIN", "SOAP", "pcs", "5", "2960")
	_, err = m.transfer(t, m.mover, "MAIN", "HK", nil, m.tl("SOAP", "1"))
	wantCode(t, err, "WAREHOUSE_FROZEN")
	// another warehouse goes on
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "1"))
	must(t, err)
	// one count of a warehouse at a time
	_, err = m.svc.StartCount(m.cnt, m.propID, inventory.CountInput{ClientUUID: m.id(), WarehouseID: m.wh["HK"], Scope: inventory.ScopeAll})
	wantCode(t, err, "COUNT_IN_PROGRESS")
	// a replay of the start answers the first count
	again, err := m.svc.StartCount(m.cnt, m.propID, inventory.CountInput{ClientUUID: uuid, WarehouseID: m.wh["HK"], Scope: inventory.ScopeAll})
	must(t, err)
	if again.ID != c.ID {
		t.Fatal("a replay made a second count")
	}
	// cancelling ends the freeze and posts nothing
	_, err = m.svc.CancelCount(m.cnt, m.propID, c.ID)
	must(t, err)
	_, err = m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SOAP", "1"))
	must(t, err)
	m.wantBal(t, m.pair("HK", "SOAP"), "99", "293040")
	if m.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_COUNT'`) != 0 {
		t.Fatal("a cancelled count posted something")
	}
	m.consistent(t)
}

func TestTheServerStampsWhenAQuantityWasCountedAndAUnchangedQuantityKeepsItsTime(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	c := m.startCount(t, "HK", inventory.ScopeAll)
	m.Clock.Advance(10 * time.Minute)
	c = m.found(t, c, "SOAP", "97")
	first := lineOf(t, c, m.item["SOAP"]).CountedAt
	if first == nil || !first.Equal(m.Clock.Now()) {
		t.Fatalf("counted_at %v, the server clock is %v", first, m.Clock.Now())
	}
	m.Clock.Advance(5 * time.Minute)
	c = m.found(t, c, "SOAP", "97") // the same quantity again
	if got := lineOf(t, c, m.item["SOAP"]).CountedAt; !got.Equal(*first) {
		t.Fatalf("an unchanged quantity moved counted_at: %v -> %v", first, got)
	}
	m.Clock.Advance(5 * time.Minute)
	c = m.found(t, c, "SOAP", "98") // changed
	if got := lineOf(t, c, m.item["SOAP"]).CountedAt; !got.Equal(m.Clock.Now()) {
		t.Fatalf("a changed quantity keeps the old time: %v", got)
	}
	// a quantity in a unit of the item is converted; a quantity that is not a unit of the item is refused
	_, err := m.svc.EnterCountLines(m.cnt, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item["SOAP"], UnitID: ptr(m.unit["karton"]), Qty: ptr(dec("1"))}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = m.svc.EnterCountLines(m.cnt, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item["SOAP"], Qty: ptr(dec("-1"))}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = m.svc.EnterCountLines(m.cnt, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item["BERAS"], Qty: ptr(dec("1"))}}) // not on the sheet
	wantCode(t, err, "VALIDATION_FAILED")
	// the sheet shows the book quantity only after it is submitted
	c = m.submitCount(t, c)
	if l := lineOf(t, c, m.item["SOAP"]); l.BookQty == nil || !l.BookQty.Equal(dec("100")) || l.VarianceQty == nil || !l.VarianceQty.Equal(dec("-2")) {
		t.Fatalf("the review of a submitted count: %+v", l)
	}
	_, err = m.svc.EnterCountLines(m.cnt, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item["SOAP"], Qty: ptr(dec("1"))}})
	wantCode(t, err, "COUNT_STATE_INVALID")
}

// 4.7: a loss is an OUT valued by the method, a gain an IN at the last cost; the pair of accounts is the variance account against the inventory account; the freeze ends.
func TestACountPostsALossByTheMethodAndAGainAtTheLastCost(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			m := newMovements(t, method)
			m.open(t, "HK", "SOAP", "pcs", "100", "2960")
			m.open(t, "HK", "SHAMPOO", "pcs", "50", "3250")
			c := m.startCount(t, "HK", inventory.ScopeAll)
			c = m.found(t, c, "SOAP", "97")    // 3 fewer: a loss of 3 x 2,960
			c = m.found(t, c, "SHAMPOO", "52") // 2 more: a gain of 2 x 3,250
			c = m.submitCount(t, c)
			c = m.approveCount(t, c)
			if c.Status != inventory.CountApproved || c.TotalVarianceValue == nil || !c.TotalVarianceValue.Equal(dec("-2380")) { // -8,880 + 6,500
				t.Fatalf("%s %v", c.Status, c.TotalVarianceValue)
			}
			m.wantBal(t, m.pair("HK", "SOAP"), "97", "287120")
			m.wantBal(t, m.pair("HK", "SHAMPOO"), "52", "169000")
			q, v, dr, cr := m.countMovement(t, c.ID, "COUNT_LOSS")
			if !dec(q).Equal(dec("-3")) || !dec(v).Equal(dec("-8880")) || dr != itoa64(m.accounts["5295"]) || cr != itoa64(m.accounts["1330"]) {
				t.Fatalf("loss: %s %s Dr %s Cr %s", q, v, dr, cr)
			}
			q, v, dr, cr = m.countMovement(t, c.ID, "COUNT_GAIN")
			if !dec(q).Equal(dec("2")) || !dec(v).Equal(dec("6500")) || dr != itoa64(m.accounts["1330"]) || cr != itoa64(m.accounts["5295"]) {
				t.Fatalf("gain: %s %s Dr %s Cr %s", q, v, dr, cr)
			}
			if l := lineOf(t, c, m.item["SOAP"]); l.ExpectedQty == nil || !l.ExpectedQty.Equal(dec("100")) || !l.VarianceQty.Equal(dec("-3")) {
				t.Fatalf("line: %+v", l)
			}
			// the freeze is over
			_, err := m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SOAP", "1"))
			must(t, err)
			// a count is approved once: a replay answers the same count and posts nothing more
			again, err := m.svc.ApproveCount(m.apr, m.propID, c.ID)
			must(t, err)
			if again.Status != inventory.CountApproved {
				t.Fatal(again.Status)
			}
			if n := m.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_COUNT'`); n != 2 {
				t.Fatalf("%d count movements", n)
			}
			if method == inventory.MethodFIFO { // a gain makes a layer at the last cost
				if ls := m.layers(t, m.pair("HK", "SHAMPOO")); len(ls) != 2 {
					t.Fatalf("layers: %+v", ls)
				}
			}
			m.consistent(t)
		})
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestAnItemThatWasNotCountedIsNotChangedAndACountNeedsAtLeastOneLine(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	m.open(t, "HK", "SHAMPOO", "pcs", "50", "3250")
	c := m.startCount(t, "HK", inventory.ScopeAll)
	_, err := m.svc.SubmitCount(m.cnt, m.propID, c.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	c = m.found(t, c, "SOAP", "100") // no variance
	c = m.submitCount(t, c)
	_, err = m.svc.ApproveCount(m.apr, m.propID, c.ID+999)
	wantCode(t, err, "COUNT_NOT_FOUND")
	c = m.approveCount(t, c)
	m.wantBal(t, m.pair("HK", "SHAMPOO"), "50", "162500")
	if m.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_COUNT'`) != 0 {
		t.Fatal("a count with no variance posted a movement")
	}
	// a count of a category and a count of a list
	c2 := m.startCount(t, "HK", inventory.ScopeCategory, func(in *inventory.CountInput) { in.CategoryID = ptr(m.cat["SUP"]) })
	if len(c2.Lines) != 2 {
		t.Fatalf("lines of the category: %d", len(c2.Lines))
	}
	_, err = m.svc.CancelCount(m.cnt, m.propID, c2.ID)
	must(t, err)
	c3 := m.startCount(t, "HK", inventory.ScopeList, func(in *inventory.CountInput) { in.ItemIDs = []int64{m.item["SHAMPOO"]} })
	if len(c3.Lines) != 1 || c3.Lines[0].ItemCode != "SHAMPOO" {
		t.Fatalf("lines of the list: %+v", c3.Lines)
	}
}

// The approval is a decision of the signed-in approver under the rule INV_COUNT (9.2): under DISTINCT the person who submitted the count does not approve it.
func TestTheApprovalOfACountFollowsItsRuleAndNeedsItsPermission(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	m.SetRule(t, m.tenantID, m.propID, "INV_COUNT", "DISTINCT")
	both := m.User(t, m.tenantID, m.propID, auth.PermInventoryCount, auth.PermInventoryApprove, auth.PermInventoryView)
	c := m.startCount(t, "HK", inventory.ScopeAll)
	q := dec("90")
	_, err := m.svc.EnterCountLines(both, m.propID, c.ID, []inventory.CountLineInput{{ItemID: m.item["SOAP"], Qty: &q}})
	must(t, err)
	_, err = m.svc.SubmitCount(both, m.propID, c.ID)
	must(t, err)
	_, err = m.svc.ApproveCount(m.cnt, m.propID, c.ID) // a counter, not an approver
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.ApproveCount(both, m.propID, c.ID) // the person who submitted it
	if err == nil {
		t.Fatal("the person who submitted the count approved it under DISTINCT")
	}
	out, err := m.svc.ApproveCount(m.apr, m.propID, c.ID)
	must(t, err)
	if out.Status != inventory.CountApproved {
		t.Fatal(out.Status)
	}
	m.wantBal(t, m.pair("HK", "SOAP"), "90", "266400")
}

// 4.4 item 4: a count that sets the quantity ends the shortfalls of the item without a settlement; the balance is what was found, at cost.
func TestACountClosesTheShortfallsOfAnItemWithoutASettlement(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			m := newMovements(t, method)
			m.open(t, "BAR", "SIRUP", "botol", "1", "50000")
			m.sale(t, m.pair("BAR", "SIRUP"), "3") // 1 from the shelf, 2 below zero at the last cost
			m.wantBal(t, m.pair("BAR", "SIRUP"), "-2", "-100000")
			c := m.startCount(t, "BAR", inventory.ScopeAll)
			c = m.found(t, c, "SIRUP", "0")
			c = m.submitCount(t, c)
			c = m.approveCount(t, c)
			m.wantBal(t, m.pair("BAR", "SIRUP"), "0", "0")
			if n := m.Count(t, `SELECT count(*) FROM inv_shortfalls WHERE warehouse_id = $1 AND status = 'CLOSED_BY_COUNT'`, m.wh["BAR"]); n != 1 {
				t.Fatalf("%d shortfalls closed by the count", n)
			}
			if n := m.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'NEG_SETTLE'`); n != 0 {
				t.Fatalf("%d settlements: a count closes a shortfall without one", n)
			}
			if l := lineOf(t, c, m.item["SIRUP"]); !l.VarianceQty.Equal(dec("2")) || !l.ExpectedQty.Equal(dec("-2")) {
				t.Fatalf("line: %+v", l)
			}
			m.consistent(t)
		})
	}
}

// D6: an open count is a blocker of the night audit by default, a warning when the property chooses so.
func TestAnOpenCountIsABlockerOfTheNightAuditOrAWarningBySetting(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	c := m.startCount(t, "HK", inventory.ScopeAll)
	day, err := m.Tenancy.CurrentBusinessDay(m.admin, m.propID)
	must(t, err)
	b, w, err := m.svc.NightAuditStock(m.admin, m.tenantID, m.propID, day.BusinessDate)
	must(t, err)
	if !hasFinding(b, "INVENTORY_COUNT_OPEN") || hasFinding(w, "INVENTORY_COUNT_OPEN") {
		t.Fatalf("blockers %v warnings %v", b, w)
	}
	m.settings(t, func(in *inventory.SettingsInput) { in.CountBlocksNightAudit = false })
	b, w, err = m.svc.NightAuditStock(m.admin, m.tenantID, m.propID, day.BusinessDate)
	must(t, err)
	if hasFinding(b, "INVENTORY_COUNT_OPEN") || !hasFinding(w, "INVENTORY_COUNT_OPEN") {
		t.Fatalf("blockers %v warnings %v", b, w)
	}
	_, err = m.svc.CancelCount(m.cnt, m.propID, c.ID)
	must(t, err)
	b, w, _ = m.svc.NightAuditStock(m.admin, m.tenantID, m.propID, day.BusinessDate)
	if hasFinding(b, "INVENTORY_COUNT_OPEN") || hasFinding(w, "INVENTORY_COUNT_OPEN") {
		t.Fatal("a cancelled count is not open")
	}
}

func hasFinding(list []inventory.Finding, code string) bool {
	for _, f := range list {
		if f.Code == code {
			return true
		}
	}
	return false
}
