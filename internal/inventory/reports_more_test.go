package inventory_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The other reports of the inventory (35-inventory.md 12.5, section 22): the figures of known scenarios, read from the ledger and the documents. Real PostgreSQL.

func (m *mfx) today(t *testing.T) civil.Date {
	t.Helper()
	d, err := m.Tenancy.CurrentBusinessDay(m.admin, m.propID)
	must(t, err)
	return d.BusinessDate
}

func wantDec(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Fatalf("%s: %s, want %s", what, got, want)
	}
}

// ---------------------------------------------------------------- waste, meals and the documents of a day

func TestWasteByReasonAndTheMealsNetTheirReversalsAndTheDayListsItsDocuments(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "BAR", "SIRUP", "botol", "20", "50000")
	spoil, err := m.issue(m.mover, "MAIN", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("SPOILAGE") }, m.il("BERAS", "2"))
	must(t, err)
	taste, err := m.issue(m.mover, "MAIN", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("TASTING") }, m.il("BERAS", "1"))
	must(t, err)
	meal, err := m.issue(m.mover, "MAIN", inventory.PurposeStaffMeal, func(in *inventory.IssueInput) { in.Covers = ptr(25) }, m.il("BERAS", "5"))
	must(t, err)
	_, err = m.issue(m.mover, "BAR", inventory.PurposeEntertainment, func(in *inventory.IssueInput) { in.Covers = ptr(4); in.Beneficiary = ptr("Bapak Gubernur") }, m.il("SIRUP", "2"))
	must(t, err)

	// the waste: 2 kg and 1 kg of rice at 15,500
	w, err := m.svc.Waste(m.admin, m.propID, nil, nil, inventory.WasteFilter{})
	must(t, err)
	wantDec(t, "waste total", w.Total, "46500")
	if w.Docs != 2 || len(w.Rows) != 2 || len(w.Reasons) != 2 {
		t.Fatalf("waste: %+v", w)
	}
	byReason := map[string]inventory.WasteReason{}
	for _, r := range w.Reasons {
		byReason[r.Reason] = r
	}
	wantDec(t, "spoilage", byReason["SPOILAGE"].Value, "31000")
	wantDec(t, "tasting", byReason["TASTING"].Value, "15500")
	for _, r := range w.Rows {
		if r.WarehouseCode != "MAIN" || r.ItemCode != "BERAS" || r.Docs != 1 {
			t.Fatalf("row: %+v", r)
		}
	}
	// narrowed to the bar, there is none
	bar, err := m.svc.Waste(m.admin, m.propID, nil, nil, inventory.WasteFilter{WarehouseID: ptr(m.wh["BAR"])})
	must(t, err)
	if len(bar.Rows) != 0 || !bar.Total.IsZero() {
		t.Fatalf("the bar: %+v", bar)
	}

	// staff meals and entertainment with the cost of a portion
	ml, err := m.svc.Meals(m.admin, m.propID, nil, nil, nil)
	must(t, err)
	sm, en := ml.Totals["STAFF_MEAL"], ml.Totals["ENTERTAINMENT"]
	wantDec(t, "meal value", sm.Value, "77500")
	wantDec(t, "entertainment value", en.Value, "100000")
	if sm.Docs != 1 || sm.Covers != 25 || sm.CostPerCover == nil || !sm.CostPerCover.Equal(dec("3100")) || en.Covers != 4 || en.CostPerCover == nil || !en.CostPerCover.Equal(dec("25000")) {
		t.Fatalf("meals: %+v %+v", sm, en)
	}

	// the day lists its documents, and each kind adds up to the column of the daily cost
	day := m.today(t)
	cd, err := m.svc.CostDay(m.admin, m.propID, day)
	must(t, err)
	dc, err := m.svc.DailyCost(m.admin, m.propID, &day, &day)
	must(t, err)
	if len(dc.Days) != 1 {
		t.Fatalf("days: %+v", dc.Days)
	}
	wantDec(t, "the day", cd.Total, dc.Days[0].Total.String())
	for _, k := range cd.Kinds {
		wantDec(t, "kind "+k.CostKind, k.Total, dc.Days[0].Kinds[k.CostKind].String())
		var sum decimal.Decimal
		for _, d := range k.Docs {
			sum = sum.Add(d.Amount)
		}
		wantDec(t, "documents of "+k.CostKind, sum, k.Total.String())
	}
	var wasteDocs []string
	for _, k := range cd.Kinds {
		if k.CostKind == inventory.CostWaste {
			for _, d := range k.Docs {
				if d.SourceType != "INV_ISSUE" || d.LinkID == 0 {
					t.Fatalf("the waste document has no link: %+v", d)
				}
				wasteDocs = append(wasteDocs, d.Number)
			}
		}
	}
	if len(wasteDocs) != 2 || (wasteDocs[0] != spoil.Number && wasteDocs[1] != spoil.Number) || (wasteDocs[0] != taste.Number && wasteDocs[1] != taste.Number) {
		t.Fatalf("the documents of the waste: %v (want %s and %s)", wasteDocs, spoil.Number, taste.Number)
	}
	// a day after the open day is refused
	_, err = m.svc.CostDay(m.admin, m.propID, day.AddDays(1))
	wantCode(t, err, "VALIDATION_FAILED")

	// reversals net the documents out: the meal and the tasting are taken back
	_, err = m.svc.ReverseIssue(m.mover, m.propID, meal.ID, "counted twice")
	must(t, err)
	_, err = m.svc.ReverseIssue(m.mover, m.propID, taste.ID, "wrong item")
	must(t, err)
	ml, err = m.svc.Meals(m.admin, m.propID, nil, nil, nil)
	must(t, err)
	sm = ml.Totals["STAFF_MEAL"]
	if sm.Docs != 0 || sm.Covers != 0 || !sm.Value.IsZero() || sm.CostPerCover != nil {
		t.Fatalf("meals after the reversal: %+v", sm)
	}
	w, err = m.svc.Waste(m.admin, m.propID, nil, nil, inventory.WasteFilter{})
	must(t, err)
	wantDec(t, "waste after the reversal", w.Total, "31000")
	if w.Docs != 1 {
		t.Fatalf("documents after the reversal: %d", w.Docs)
	}
	m.consistent(t)
}

func TestTheReportsAreForWhoMayReadTheStockAndOnlyInTheirTenant(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	day := m.today(t)
	nobody := m.User(t, m.tenantID, m.propID, auth.PermHousekeepingUpdate) // no right on the stock
	_, err := m.svc.Waste(nobody, m.propID, nil, nil, inventory.WasteFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.PendingApprovals(nobody, m.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.CostDay(nobody, m.propID, day)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.TransfersWithoutRequisition(nobody, m.propID, nil, nil, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.CountVariances(nobody, m.propID, nil, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	other := m.Tenant(t, "OTH")
	op := m.PropertyIn(t, other.ID, "OTH1", "IDR", 0)
	stranger := m.User(t, other.ID, op.ID, auth.PermInventoryView)
	_, err = m.svc.Waste(stranger, m.propID, nil, nil, inventory.WasteFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = m.svc.AmenityMonths(stranger, m.propID, nil, nil)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// ---------------------------------------------------------------- transfers without a requisition

func TestTransfersWithoutARequisitionSkipTheOnesWithOneAndDoNotCountAReversedOne(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "MAIN", "SIRUP", "botol", "30", "50000")
	free, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "2"))
	must(t, err)
	r := m.approved(t, m.requisition(t, m.req, m.srLine("SIRUP", "6")))
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("SIRUP", "6")) // with a requisition: not in the report
	must(t, err)
	undone, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "1"))
	must(t, err)
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, undone.ID, "sent by mistake")
	must(t, err)

	rep, err := m.svc.TransfersWithoutRequisition(m.admin, m.propID, nil, nil, nil, nil)
	must(t, err)
	if len(rep.Rows) != 2 || rep.Docs != 1 {
		t.Fatalf("rows %d docs %d: %+v", len(rep.Rows), rep.Docs, rep.Rows)
	}
	wantDec(t, "total", rep.Total, "31000") // only the 2 kg at 15,500; the reversed one is listed, not counted
	for _, x := range rep.Rows {
		switch x.TransferID {
		case free.ID:
			if x.Status == "REVERSED" || x.FromWarehouse == "" || x.CreatedByName == "" {
				t.Fatalf("the free transfer: %+v", x)
			}
			wantDec(t, "value", x.Value, "31000")
		case undone.ID:
			if x.Status != "REVERSED" {
				t.Fatalf("the reversed transfer: %+v", x)
			}
		default:
			t.Fatalf("a transfer with a requisition is in the report: %+v", x)
		}
	}
	// the filter on the destination
	none, err := m.svc.TransfersWithoutRequisition(m.admin, m.propID, nil, nil, nil, ptr(m.wh["MAIN"]))
	must(t, err)
	if len(none.Rows) != 0 {
		t.Fatalf("to the main store: %+v", none.Rows)
	}
}

// ---------------------------------------------------------------- count variances

func TestCountVariancesListTheApprovedCountsWithTheirLinesAndItems(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			m := newMovements(t, method)
			m.open(t, "HK", "SOAP", "pcs", "100", "2960")
			m.open(t, "HK", "SHAMPOO", "pcs", "50", "3250")
			c := m.startCount(t, "HK", inventory.ScopeAll)
			c = m.found(t, c, "SOAP", "97")    // a loss of 3 x 2,960
			c = m.found(t, c, "SHAMPOO", "52") // a gain of 2 x 3,250
			c = m.submitCount(t, c)
			// a count that is not approved yet is not in the report
			before, err := m.svc.CountVariances(m.admin, m.propID, nil, nil, nil)
			must(t, err)
			if len(before.Counts) != 0 {
				t.Fatalf("an unapproved count: %+v", before.Counts)
			}
			c = m.approveCount(t, c)
			rep, err := m.svc.CountVariances(m.admin, m.propID, nil, nil, nil)
			must(t, err)
			if len(rep.Counts) != 1 || rep.Counts[0].ID != c.ID || rep.Counts[0].Number != c.Number || rep.Counts[0].ApproverName == "" {
				t.Fatalf("counts: %+v", rep.Counts)
			}
			wantDec(t, "gain", rep.Counts[0].Gain, "6500")
			wantDec(t, "loss", rep.Counts[0].Loss, "8880")
			wantDec(t, "net", rep.Counts[0].Net, "-2380")
			wantDec(t, "total net", rep.Net, "-2380")
			if rep.Counts[0].Lines != 2 || rep.Counts[0].LinesWithVariance != 2 || len(rep.Lines) != 2 || len(rep.Items) != 2 {
				t.Fatalf("lines: %+v / %+v / %+v", rep.Counts[0], rep.Lines, rep.Items)
			}
			for _, l := range rep.Lines {
				switch l.ItemCode {
				case "SOAP":
					wantDec(t, "soap book", l.BookQty, "100")
					wantDec(t, "soap found", l.CountedQty, "97")
					wantDec(t, "soap difference", l.VarianceQty, "-3")
					wantDec(t, "soap value", l.VarianceValue, "-8880")
				case "SHAMPOO":
					wantDec(t, "shampoo difference", l.VarianceQty, "2")
					wantDec(t, "shampoo value", l.VarianceValue, "6500")
				default:
					t.Fatalf("line: %+v", l)
				}
			}
			// another warehouse has none
			other, err := m.svc.CountVariances(m.admin, m.propID, nil, nil, ptr(m.wh["MAIN"]))
			must(t, err)
			if len(other.Counts) != 0 {
				t.Fatalf("the main store: %+v", other.Counts)
			}
		})
	}
}

// ---------------------------------------------------------------- purchase price variance

func TestThePurchasePriceVarianceIsBySupplierAndMarksTheBillsAboveTheTolerance(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
	// a 2% rise on five cartons of 600,000, billed with the approval it needs
	po1 := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r1 := f.receive(t, po1, "5")
	in := invoice(f.sup, "INV-1", billLine(r1, 1, "5", "612000", "0"))
	in.Approval = f.approver(t)
	_, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k1")
	must(t, err)
	// a rise of half a percent at the market, inside the tolerance
	po2 := f.order(t, f.pasar, "MAIN", "SIRUP", "karton", "5", "600000")
	r2 := f.receive(t, po2, "5")
	_, err = f.svc.ConfirmBill(f.admin, f.propID, invoice(f.pasar, "INV-2", billLine(r2, 1, "5", "603000", "0")), "k2")
	must(t, err)

	rep, err := f.svc.PriceVariances(f.admin, f.propID, nil, nil, nil)
	must(t, err)
	if rep.Bills != 2 || rep.BillsAbove != 1 || len(rep.Suppliers) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	wantDec(t, "received", rep.ReceiptValue, "6000000")
	wantDec(t, "billed", rep.BillCost, "6075000")
	wantDec(t, "variance", rep.Variance, "75000")
	if rep.Pct == nil || !rep.Pct.Equal(dec("1.25")) {
		t.Fatalf("the share of the value: %v", rep.Pct)
	}
	for _, s := range rep.Suppliers {
		switch s.SupplierID {
		case f.sup:
			wantDec(t, "supplier variance", s.Variance, "60000")
			if s.Bills != 1 || s.BillsAbove != 1 || len(s.Items) != 1 || s.Pct == nil || !s.Pct.Equal(dec("2")) {
				t.Fatalf("supplier one: %+v", s)
			}
			it := s.Items[0]
			if it.ReceiptPrice == nil || !it.ReceiptPrice.Equal(dec("50000")) || it.BillPrice == nil || !it.BillPrice.Equal(dec("51000")) || !it.Qty.Equal(dec("60")) || it.LinesAboveTol != 1 {
				t.Fatalf("the item (prices per bottle, quantity in bottles): %+v", it)
			}
		case f.pasar:
			wantDec(t, "market variance", s.Variance, "15000")
			if s.Bills != 1 || s.BillsAbove != 0 {
				t.Fatalf("the market: %+v", s)
			}
		default:
			t.Fatalf("supplier: %+v", s)
		}
	}
	// narrowed to one supplier
	one, err := f.svc.PriceVariances(f.admin, f.propID, nil, nil, &f.pasar)
	must(t, err)
	if len(one.Suppliers) != 1 || one.Bills != 1 {
		t.Fatalf("one supplier: %+v", one)
	}
}

// ---------------------------------------------------------------- the documents waiting for approval

func TestAPendingDocumentIsAsOldAsTheEntryToItsCurrentLevelNotAsItsSubmission(t *testing.T) {
	c := newChain(t)
	c.open(t, "MAIN", "BERAS", "kg", "10", "15000")
	r := c.request(t, c.req, c.line("BERAS", "kg", "10", "15000"))
	// a draft is not waiting for anyone
	rep, err := c.svc.PendingApprovals(c.admin, c.propID)
	must(t, err)
	if len(rep.Rows) != 0 {
		t.Fatalf("a draft: %+v", rep.Rows)
	}
	r = c.submit(t, c.req, r)
	if active(r) != 1 {
		t.Fatalf("level %d", active(r))
	}
	c.Clock.Advance(30 * time.Hour)
	rep, err = c.svc.PendingApprovals(c.admin, c.propID)
	must(t, err)
	if len(rep.Rows) != 1 {
		t.Fatalf("rows: %+v", rep.Rows)
	}
	d := rep.Rows[0]
	if d.DocType != "PR" || d.DocID != r.ID || d.Number != r.Number || d.LevelNo != 1 || d.AgeHours != 30 || d.AgeDays != 1 || d.Awaiting == "" || d.StepName == "" {
		t.Fatalf("level one: %+v", d)
	}
	wantDec(t, "value", d.Value, "150000")

	// the head approves; the request reaches the next level at this moment, and its age starts again
	r = c.approve(t, c.head, r, 1)
	if r.Status != "SUBMITTED" || active(r) != 2 {
		t.Fatalf("after the head: %s level %d", r.Status, active(r))
	}
	c.Clock.Advance(5 * time.Hour)
	rep, err = c.svc.PendingApprovals(c.admin, c.propID)
	must(t, err)
	if len(rep.Rows) != 1 {
		t.Fatalf("rows: %+v", rep.Rows)
	}
	d = rep.Rows[0]
	if d.LevelNo != 2 || d.AgeHours != 5 || d.AgeDays != 0 {
		t.Fatalf("level two: %+v (the 30 hours before it are not its age)", d)
	}
	if d.SubmittedAt == nil || d.EnteredAt == nil || !d.EnteredAt.After(*d.SubmittedAt) {
		t.Fatalf("the dates: submitted %v, entered %v", d.SubmittedAt, d.EnteredAt)
	}

	// a requisition in its chain is listed with the requests, the oldest first
	sr := c.requisitionInChain(t)
	rep, err = c.svc.PendingApprovals(c.admin, c.propID)
	must(t, err)
	if len(rep.Rows) != 2 || rep.Rows[0].DocType != "PR" || rep.Rows[1].DocType != "SR" || rep.Rows[1].DocID != sr {
		t.Fatalf("the list: %+v", rep.Rows)
	}
	// an approved request leaves the list
	r = c.approve(t, c.cc, r, 2)
	rep, err = c.svc.PendingApprovals(c.admin, c.propID)
	must(t, err)
	for _, x := range rep.Rows {
		if x.DocType == "PR" && x.DocID == r.ID && r.Status == "SUBMITTED" && active(r) == 0 {
			t.Fatalf("a request with no active level is listed: %+v", x)
		}
	}
}

// requisitionInChain submits a requisition of the bar to the main store and leaves it at its first level.
func (c *cfx) requisitionInChain(t *testing.T) int64 {
	t.Helper()
	m := finishMovements(t, c)
	m.open(t, "MAIN", "SIRUP", "botol", "30", "50000")
	r := m.requisition(t, m.req, m.srLine("SIRUP", "6"))
	r, err := m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	return r.ID
}

// ---------------------------------------------------------------- items below the minimum, and the request made from them

func TestAPurchaseRequestIsMadeFromTheItemsBelowTheirMinimum(t *testing.T) {
	c := newChain(t)
	c.open(t, "MAIN", "BERAS", "kg", "4", "15500")
	c.open(t, "MAIN", "SIRUP", "botol", "3", "50000")
	_, err := c.svc.SetLevels(c.admin, c.propID, c.wh["MAIN"], []inventory.Level{
		{ItemID: c.item["BERAS"], MinQty: dec("10"), MaxQty: ptr(dec("30"))},     // up to the maximum: 26 kg
		{ItemID: c.item["SIRUP"], MinQty: dec("12"), ReorderQty: ptr(dec("24"))}, // the reorder quantity: 24 bottles
	})
	must(t, err)
	rows, err := c.svc.LowStock(c.admin, c.propID, nil)
	must(t, err)
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	for _, r := range rows {
		switch r.ItemCode {
		case "BERAS":
			wantDec(t, "rice suggestion", r.SuggestQty, "26")
		case "SIRUP":
			wantDec(t, "syrup suggestion", r.SuggestQty, "24")
		}
	}

	in := inventory.LowStockRequestInput{ClientUUID: c.id(), WarehouseID: c.wh["MAIN"], ItemIDs: []int64{c.item["BERAS"], c.item["SIRUP"]}}
	req, err := c.svc.CreateRequestFromLowStock(c.req, c.propID, in)
	must(t, err)
	if req.Status != "DRAFT" || len(req.Lines) != 2 {
		t.Fatalf("request: %+v", req)
	}
	for _, l := range req.Lines {
		switch l.ItemID {
		case c.item["BERAS"]:
			// a sack holds 25 kg: 26 kg are 1.04 sacks
			wantDec(t, "rice in sacks", l.Qty, "1.04")
		case c.item["SIRUP"]:
			// a carton holds 12 bottles: 24 bottles are 2 cartons
			wantDec(t, "syrup in cartons", l.Qty, "2")
		default:
			t.Fatalf("line: %+v", l)
		}
	}
	// the same request again answers the first one
	again, err := c.svc.CreateRequestFromLowStock(c.req, c.propID, in)
	must(t, err)
	if again.ID != req.ID {
		t.Fatalf("a replay made another request: %d and %d", again.ID, req.ID)
	}
	// who may not request cannot
	_, err = c.svc.CreateRequestFromLowStock(c.User(t, c.tenantID, c.propID, auth.PermInventoryView), c.propID, inventory.LowStockRequestInput{ClientUUID: c.id(), WarehouseID: c.wh["MAIN"], ItemIDs: []int64{c.item["BERAS"]}})
	wantCode(t, err, "PERMISSION_DENIED")
	// nothing is asked for when the items are not below their minimum any more
	c.open(t, "BAR", "SIRUP", "botol", "20", "50000")
	_, err = c.svc.CreateRequestFromLowStock(c.req, c.propID, inventory.LowStockRequestInput{ClientUUID: c.id(), WarehouseID: c.wh["BAR"], ItemIDs: []int64{c.item["SIRUP"]}})
	wantCode(t, err, "NOTHING_TO_ORDER")
	_, err = c.svc.CreateRequestFromLowStock(c.req, c.propID, inventory.LowStockRequestInput{ClientUUID: c.id(), WarehouseID: c.wh["MAIN"]})
	wantCode(t, err, "VALIDATION_FAILED")
}

// ---------------------------------------------------------------- amenities by month

func TestTheAmenitiesByMonthGiveTheCostPerOccupiedRoomAndTheChangeAgainstTheMonthBefore(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	a.savePlan(t)
	must(t, a.closeDay())
	first := a.run(t, a.night)
	rep, err := a.svc.AmenityMonths(a.admin, a.propID, nil, nil)
	must(t, err)
	sep := monthOf(t, rep, "2026-09-01")
	if sep.Rooms != 4 || sep.Guests != 8 || sep.Nights != 1 {
		t.Fatalf("september: %+v", sep)
	}
	wantDec(t, "september cost", sep.Cost, "44890") // 4 soaps, 8 toothbrushes, 1 shampoo and 2 slippers
	if sep.CostPerRoom == nil || !sep.CostPerRoom.Equal(dec("11222.5")) || sep.CostPerGuest == nil || !sep.CostPerGuest.Equal(dec("5611.25")) {
		t.Fatalf("per room %v, per guest %v", sep.CostPerRoom, sep.CostPerGuest)
	}
	if sep.PrevCost != nil || sep.ChangePct != nil || len(sep.Items) != 4 {
		t.Fatalf("there is no month before: %+v", sep)
	}
	var items decimal.Decimal
	for _, it := range sep.Items {
		items = items.Add(it.Cost)
	}
	wantDec(t, "items add up to the month", items, sep.Cost.String())

	// the next night, in October: compared with September
	a.nextBusinessDay(t)
	must(t, a.closeDay())
	second := a.run(t, "2026-10-01")
	rep, err = a.svc.AmenityMonths(a.admin, a.propID, nil, nil)
	must(t, err)
	oct := monthOf(t, rep, "2026-10-01")
	wantDec(t, "october cost", oct.Cost, second.TotalValue.String())
	if oct.PrevCost == nil || !oct.PrevCost.Equal(first.TotalValue) || oct.PrevCostPerRoom == nil || !oct.PrevCostPerRoom.Equal(*sep.CostPerRoom) {
		t.Fatalf("the month before: %v / %v", oct.PrevCost, oct.PrevCostPerRoom)
	}
	want := second.TotalValue.Sub(first.TotalValue).Mul(decimal.NewFromInt(100)).Div(first.TotalValue).Round(2)
	if oct.ChangePct == nil || !oct.ChangePct.Equal(want) {
		t.Fatalf("the change: %v, want %s", oct.ChangePct, want)
	}
	// a period inside October still reads September for the comparison
	from, to := civil.MustParseDate("2026-10-01"), civil.MustParseDate("2026-10-31")
	only, err := a.svc.AmenityMonths(a.admin, a.propID, &from, &to)
	must(t, err)
	if len(only.Months) != 1 || only.Months[0].PrevCost == nil {
		t.Fatalf("october only: %+v", only.Months)
	}
}

func monthOf(t *testing.T, r inventory.AmenityMonthsReport, month string) inventory.AmenityMonth {
	t.Helper()
	for _, m := range r.Months {
		if m.Month.String() == month {
			return m
		}
	}
	t.Fatalf("no month %s in %+v", month, r.Months)
	return inventory.AmenityMonth{}
}
