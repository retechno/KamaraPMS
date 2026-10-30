package documents

import (
	"context"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

// The five reports of doc 38 7.2 to 7.5 as PDFs. Each one prints the figures of the report service, never its own: the permission is answered before the query is read, as for the JSON.

// napMoney formats a money string of a report; an empty value stays empty.
func napMoney(l Lang, dec int32) func(string) string {
	return func(v string) string {
		if v == "" {
			return ""
		}
		d, err := decimal.NewFromString(v)
		if err != nil {
			return v
		}
		return l.Money(d, dec)
	}
}

func napOpt(f func(string) string, v *string) string {
	if v == nil {
		return ""
	}
	return f(*v)
}

func napKind(k string) string {
	switch k {
	case reports.CancelKindCancelled:
		return "Cancelled"
	case reports.CancelKindRoomCancelled:
		return "Room cancelled"
	case reports.CancelKindNoShow:
		return "No-show"
	}
	return k
}

// napMethod is the name of a payment method; the codes are never printed. The Indonesian page translates the English name.
func napMethod(code string) string {
	switch code {
	case "CASH":
		return "Cash"
	case "CARD":
		return "Card"
	case "BANK_TRANSFER":
		return "Bank transfer"
	case "OTHER":
		return "Other"
	case "QRIS":
		return "QRIS"
	case "CITY_LEDGER":
		return "City ledger"
	case "COMP":
		return "Complimentary"
	}
	return code
}

// napSource is the name of the source of a reservation.
func napSource(code string) string {
	switch code {
	case "WALK_IN":
		return "Walk-in"
	case "PHONE":
		return "Phone"
	case "EMAIL":
		return "E-mail"
	case "WEBSITE":
		return "Website"
	case "OTA":
		return "OTA"
	case "AGENT":
		return "Agent"
	case "OTHER":
		return "Other"
	}
	return code
}

// napReleaseMode is the name of the way the deposits are released to revenue.
func napReleaseMode(code string) string {
	switch code {
	case "EVERY_DAY":
		return "Every day"
	case "ON_CLOSE":
		return "On close"
	}
	return code
}

// napCorrKind is the name of a kind of the report of the corrections.
func napCorrKind(k string) string {
	switch k {
	case reports.CorrVoid:
		return "Void"
	case reports.CorrAdjust:
		return "Adjustment"
	case reports.CorrAllowance:
		return "Allowance"
	case reports.CorrRefund:
		return "Refund"
	case reports.CorrDiscount:
		return "Discount"
	case reports.CorrComp:
		return "Complimentary"
	case reports.CorrPosVoid:
		return "POS void"
	}
	return k
}

// napTender is the name of the kind of a tender of a shift.
func napTender(k string) string {
	switch k {
	case "PAYMENT":
		return "Payment"
	case "REFUND":
		return "Refund"
	}
	return k
}

// napOr is the name, or the words for "none" when there is no name.
func napOr(name, none string) string {
	if name == "" {
		return none
	}
	return name
}

// napCell is the net of a row of the corrections; a payment is money, it has no net.
func napCell(m func(string) string, x reports.CorrectionRow) string {
	if x.PaymentMethod != "" {
		return "-"
	}
	return m(x.Net)
}

func napGroups(gs []reports.CancellationGroup, m func(string) string, name func(string) string) [][]string {
	rows := make([][]string, 0, len(gs))
	for _, x := range gs {
		rows = append(rows, []string{name(x.Key), strconv.Itoa(x.Events), strconv.Itoa(x.Rooms), strconv.Itoa(x.Nights), m(x.Value)})
	}
	return rows
}

// CancellationsPDF is the cancellations and no-shows of a period (report.view).
func (s *Service) CancellationsPDF(ctx context.Context, propertyID int64, in reports.CancellationParams) (Document, error) {
	rep, err := s.reports.Cancellations(ctx, propertyID, in)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l := dc.lang
	m := napMoney(l, dc.decimals)
	g := newPage(dc.hotel, l, "CANCELLATIONS AND NO-SHOWS", dc.printed)
	g.title("CANCELLATIONS AND NO-SHOWS", l.Date(rep.From)+" - "+l.Date(rep.To))
	g.pairs([][2]string{
		{"Period", l.Date(rep.From) + " - " + l.Date(rep.To)}, {"Currency", dc.prop.CurrencyCode},
		{"Fees posted", m(rep.FeesPosted)}, {"Deposits held", m(rep.DepositsHeld)},
	})
	g.section("Events")
	rows := make([][]string, 0, len(rep.Events))
	for _, e := range rep.Events {
		fee := napOpt(m, e.FeeGross)
		nights := ""
		if e.Nights != nil {
			nights = strconv.Itoa(*e.Nights)
		}
		reason := e.Reason
		if e.Reinstated {
			reason = "Reinstated"
		}
		rows = append(rows, []string{l.Date(e.BusinessDate), e.ConfirmationNumber, e.Guest, napKind(e.Kind), strconv.Itoa(e.Rooms), nights, napOpt(m, e.ValueLost), fee, reason})
	}
	g.table([]col{{17, "Date", "L"}, {24, "Reservation", "L"}, {31, "Guest", "L"}, {19, "Kind", "L"}, {10, "Rooms", "R"}, {10, "Nights", "R"}, {23, "Value lost", "R"}, {21, "Fee", "R"}, {25, "Reason", "L"}}, rows)
	for _, sec := range []struct {
		title string
		gs    []reports.CancellationGroup
		name  func(string) string
	}{
		{"By kind", rep.ByKind, napKind},
		{"By reason", rep.ByReason, func(k string) string { return napOr(k, "No reason") }},
		{"By source", rep.BySource, func(k string) string { return napOr(napSource(k), "No source") }},
		{"By company", rep.ByCompany, func(k string) string { return napOr(k, "No company") }},
	} {
		if len(sec.gs) == 0 {
			continue
		}
		g.section(sec.title)
		g.table([]col{{80, "", "L"}, {25, "Events", "R"}, {25, "Rooms", "R"}, {25, "Nights", "R"}, {25, "Value lost", "R"}}, napGroups(sec.gs, m, sec.name))
	}
	pdf, err := g.bytes()
	return Document{Filename: "cancellations-" + rep.From.String() + "-" + rep.To.String() + ".pdf", PDF: pdf}, err
}

func napChecks(g *page, l Lang, m func(string) string, cs []reports.LedgerCheck) {
	g.section("Checks")
	rows := make([][]string, 0, len(cs))
	for _, c := range cs {
		t := c.Title
		if c.Account != "" {
			t += " (" + c.Account + ")"
		}
		rows = append(rows, []string{t, m(c.Report), m(c.Other), m(c.Difference)})
	}
	g.table([]col{{78, "", "L"}, {34, "Report", "R"}, {34, "Other source", "R"}, {34, "Difference", "R"}}, rows)
}

func napReconciled(g *page, ok bool) {
	if ok {
		return
	}
	g.color(alert)
	g.note("The report does not agree with its control source: see the difference above.", "B", 9)
	g.color(ink)
}

// GuestLedgerPDF is the guest ledger of a closed business date (report.view).
func (s *Service) GuestLedgerPDF(ctx context.Context, propertyID int64, asOf *civil.Date) (Document, error) {
	rep, err := s.reports.GuestLedger(ctx, propertyID, asOf)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l := dc.lang
	m := napMoney(l, dc.decimals)
	g := newPage(dc.hotel, l, "GUEST LEDGER", dc.printed)
	g.title("GUEST LEDGER", l.Date(rep.AsOf))
	g.pairs([][2]string{{"As of", l.Date(rep.AsOf)}, {"Currency", dc.prop.CurrencyCode}, {"Deposit release", napReleaseMode(rep.ReleaseMode)}, {"Balance", m(rep.Balance)}})
	g.section("Folios")
	rows := make([][]string, 0, len(rep.Rows)+1)
	for _, x := range rep.Rows {
		age := ""
		if x.AgeDays != nil {
			age = strconv.Itoa(*x.AgeDays)
		}
		rows = append(rows, []string{x.FolioNumber, x.Owner, x.Room, m(x.Charges), m(x.Settlements), m(x.Balance), m(x.DepositHeld), age})
	}
	rows = append(rows, []string{"Total", "", "", m(rep.Charges), m(rep.Settlements), m(rep.Balance), m(rep.DepositHeld), ""})
	last := len(rows) - 1
	g.tableB([]col{{22, "Folio", "L"}, {44, "Owner", "L"}, {14, "Room", "L"}, {22, "Charges", "R"}, {22, "Settlements", "R"}, {22, "Balance", "R"}, {22, "Deposit held", "R"}, {12, "Days", "R"}}, rows, func(i int) bool { return i == last })
	g.section("Aging of the debts")
	ar := make([][]string, 0, len(rep.Aging))
	for _, a := range rep.Aging {
		ar = append(ar, []string{a.Bucket, strconv.Itoa(a.Folios), m(a.Balance)})
	}
	g.table([]col{{90, "Age (days)", "L"}, {40, "Folios", "R"}, {50, "Balance", "R"}}, ar)
	napChecks(g, l, m, rep.Checks)
	napReconciled(g, rep.Reconciled)
	pdf, err := g.bytes()
	return Document{Filename: "guest-ledger-" + rep.AsOf.String() + ".pdf", PDF: pdf}, err
}

// DepositLedgerPDF is the deposit ledger of a closed business date (report.view).
func (s *Service) DepositLedgerPDF(ctx context.Context, propertyID int64, asOf *civil.Date) (Document, error) {
	rep, err := s.reports.DepositLedger(ctx, propertyID, asOf)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l := dc.lang
	m := napMoney(l, dc.decimals)
	g := newPage(dc.hotel, l, "DEPOSIT LEDGER", dc.printed)
	g.title("DEPOSIT LEDGER", l.Date(rep.AsOf))
	g.pairs([][2]string{{"As of", l.Date(rep.AsOf)}, {"Currency", dc.prop.CurrencyCode}, {"Deposit release", napReleaseMode(rep.ReleaseMode)}, {"Closing", m(rep.Closing)}})
	g.section("Folios")
	rows := make([][]string, 0, len(rep.Rows)+1)
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.FolioNumber, x.Owner, m(x.Opening), m(x.Received), m(x.Applied), m(x.Refunded), m(x.Closing)})
	}
	rows = append(rows, []string{"Total", "", m(rep.Opening), m(rep.Received), m(rep.Applied), m(rep.Refunded), m(rep.Closing)})
	last := len(rows) - 1
	g.tableB([]col{{24, "Folio", "L"}, {40, "Owner", "L"}, {23, "Opening", "R"}, {23, "Received", "R"}, {23, "Applied", "R"}, {23, "Refunded", "R"}, {24, "Closing", "R"}}, rows, func(i int) bool { return i == last })
	napChecks(g, l, m, rep.Checks)
	napReconciled(g, rep.Reconciled)
	pdf, err := g.bytes()
	return Document{Filename: "deposit-ledger-" + rep.AsOf.String() + ".pdf", PDF: pdf}, err
}

func napTotals(ts []reports.CorrectionTotal, m func(string) string, name func(string) string) [][]string {
	rows := make([][]string, 0, len(ts))
	for _, x := range ts {
		rows = append(rows, []string{name(x.Key), strconv.Itoa(x.Count), m(x.Net), m(x.Gross)})
	}
	return rows
}

// CorrectionsPDF is the voids, corrections, discounts and allowances of a period (report.view).
func (s *Service) CorrectionsPDF(ctx context.Context, propertyID int64, in reports.CorrectionParams) (Document, error) {
	rep, err := s.reports.Corrections(ctx, propertyID, in)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l := dc.lang
	m := napMoney(l, dc.decimals)
	g := newPage(dc.hotel, l, "VOIDS, CORRECTIONS AND DISCOUNTS", dc.printed)
	g.title("VOIDS, CORRECTIONS AND DISCOUNTS", l.Date(rep.From)+" - "+l.Date(rep.To))
	share := "-"
	if rep.SharePercent != nil {
		share = *rep.SharePercent + " %"
	}
	g.pairs([][2]string{
		{"Period", l.Date(rep.From) + " - " + l.Date(rep.To)}, {"Currency", dc.prop.CurrencyCode},
		{"Revenue taken back", m(rep.RevenueTakenBack)}, {"Share of the net revenue", share},
	})
	g.section("Events")
	rows := make([][]string, 0, len(rep.Rows))
	for _, x := range rep.Rows {
		ref := x.Folio
		if ref == "" {
			ref = x.Bill
		}
		by := x.DoneBy
		if x.ApprovedBy != "" {
			by += " / " + x.ApprovedBy
		}
		item := x.ChargeCodeName
		if x.PaymentMethod != "" {
			item = napMethod(x.PaymentMethod)
		}
		rows = append(rows, []string{l.Date(x.BusinessDate), napCorrKind(x.Kind), ref, item, x.Description, napCell(m, x), m(x.Gross), x.Reason, by})
	}
	g.table([]col{{15, "Date", "L"}, {17, "Kind", "L"}, {20, "Reference", "L"}, {24, "Item", "L"}, {26, "Description", "L"}, {17, "Net", "R"}, {18, "Gross", "R"}, {22, "Reason", "L"}, {21, "By", "L"}}, rows)
	// The totals are two: the revenue the events reduced or gave away (the ledger items) and the money that was cancelled or given back (the payment voids and the refunds, which are not revenue).
	g.section("Totals")
	g.table([]col{{80, "", "L"}, {20, "Count", "R"}, {20, "Net", "R"}, {20, "Service", "R"}, {20, "Tax", "R"}, {20, "Gross", "R"}}, [][]string{
		{"Revenue reduced", strconv.Itoa(rep.Reduced.Count), m(rep.Reduced.Net), m(rep.Reduced.Service), m(rep.Reduced.Tax), m(rep.Reduced.Gross)},
		{"Money cancelled or returned", strconv.Itoa(rep.MoneyBack.Count), "-", "-", "-", m(rep.MoneyBack.Amount)},
	})
	for _, sec := range []struct {
		title string
		ts    []reports.CorrectionTotal
		name  func(string) string
	}{
		{"By kind", rep.ByKind, napCorrKind},
		{"By reason", rep.ByReason, func(k string) string { return napOr(k, "No reason") }},
		{"By user", rep.ByUser, func(k string) string { return k }},
		{"By outlet", rep.ByOutlet, func(k string) string { return napOr(k, "Hotel folios") }},
	} {
		if len(sec.ts) == 0 {
			continue
		}
		g.section(sec.title)
		g.table([]col{{90, "", "L"}, {30, "Count", "R"}, {30, "Net", "R"}, {30, "Gross", "R"}}, napTotals(sec.ts, m, sec.name))
	}
	pdf, err := g.bytes()
	return Document{Filename: "corrections-" + rep.From.String() + "-" + rep.To.String() + ".pdf", PDF: pdf}, err
}

// ZReportsPDF is the Z report of all the drawers of a business date (report.view and cashier.shift_manage); an open shift is printed as an X report.
func (s *Service) ZReportsPDF(ctx context.Context, propertyID int64, day civil.Date) (Document, error) {
	rep, err := s.reports.ZReports(ctx, propertyID, day)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l := dc.lang
	m := napMoney(l, dc.decimals)
	g := newPage(dc.hotel, l, "Z REPORT", dc.printed)
	g.title("Z REPORT", l.Date(rep.Date))
	g.pairs([][2]string{{"Business date", l.Date(rep.Date)}, {"Currency", dc.prop.CurrencyCode}, {"Cash outside a shift", m(rep.CashOffShift)}, {"Voided after close", m(rep.VoidedAfterClose)}})
	two := []col{{120, "", "L"}, {60, "Amount", "R"}}
	for _, b := range rep.Blocks {
		title := "Z report " + b.Number
		if b.Kind == "X" {
			title = "X report (shift open) " + b.Number
		}
		g.section(title)
		closed := "-"
		if b.ClosedAt != nil {
			closed = l.Time(*b.ClosedAt, dc.prop.Location())
		}
		g.pairs([][2]string{{"Drawer", b.Drawer}, {"Cashier", b.Cashier}, {"Opened", l.Time(b.OpenedAt, dc.prop.Location())}, {"Closed", closed}})
		rows := [][]string{
			{"Opening float", m(b.OpeningFloat)}, {"Cash payments", m(b.CashPayments)}, {"Cash refunds", m(b.CashRefunds)}, {"Cash receipts", m(b.CashReceipts)},
			{"Voided after close", m(b.VoidedAfterClose)}, {"Pay-ins", m(b.PayIns)}, {"Pay-outs", m(b.PayOuts)}, {"Drops", m(b.Drops)}, {"Expected cash", m(b.ExpectedCash)},
		}
		if b.CountedCash != nil {
			rows = append(rows, []string{"Counted cash", m(*b.CountedCash)}, []string{"Over / short", napOpt(m, b.OverShort)})
		}
		g.table(two, rows)
		if len(b.Tenders) > 0 {
			tr := make([][]string, 0, len(b.Tenders))
			for _, t := range b.Tenders {
				tr = append(tr, []string{napMethod(t.Method), napTender(t.Kind), strconv.Itoa(t.Count), m(t.Amount)})
			}
			g.table([]col{{70, "Method", "L"}, {40, "Kind", "L"}, {20, "Count", "R"}, {50, "Amount", "R"}}, tr)
		}
		if b.VarianceReason != "" || b.ApprovedBy != "" {
			g.note(l.T("Reason")+": "+b.VarianceReason+"  "+l.T("Approved by")+": "+b.ApprovedBy, "I", 8.5)
		}
	}
	g.section("By drawer")
	dr := func(x reports.ZDrawer) []string {
		name := x.Drawer
		if name == "" {
			name = "Property"
		}
		return []string{name, strconv.Itoa(x.Shifts), m(x.ExpectedCash), napOpt(m, x.CountedCash), napOpt(m, x.OverShort), m(x.TotalTakings)}
	}
	drows := make([][]string, 0, len(rep.Drawers)+1)
	for _, x := range rep.Drawers {
		drows = append(drows, dr(x))
	}
	drows = append(drows, dr(rep.Property))
	last := len(drows) - 1
	g.tableB([]col{{46, "Drawer", "L"}, {16, "Shifts", "R"}, {30, "Expected cash", "R"}, {30, "Counted cash", "R"}, {26, "Over / short", "R"}, {32, "Takings", "R"}}, drows, func(i int) bool { return i == last })
	g.section("Takings by method")
	tk := make([][]string, 0, len(rep.Takings))
	for _, x := range rep.Takings {
		tk = append(tk, []string{napMethod(x.Method), m(x.OnShift), m(x.OffShift), m(x.Total), m(x.CashierReport), m(x.Difference)})
	}
	g.table([]col{{40, "Method", "L"}, {28, "On a shift", "R"}, {28, "Outside a shift", "R"}, {28, "Total", "R"}, {28, "Cashier report", "R"}, {28, "Difference", "R"}}, tk)
	napReconciled(g, rep.Reconciled)
	pdf, err := g.bytes()
	return Document{Filename: "z-reports-" + rep.Date.String() + ".pdf", PDF: pdf}, err
}

// registerNap mounts the PDFs of the five reports. Each handler asks for its permission before it reads the query, as the JSON route does; the routes are written out so that the route tests read them.
func (h *Handler) registerNap(mux *http.ServeMux) {
	query := func(r *http.Request) func(string) string {
		return func(k string) string { return r.URL.Query().Get(k) }
	}
	asOf := func(render func(context.Context, int64, *civil.Date) (Document, error)) httpx.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			pid, err := tenancy.PropertyID(r)
			if err != nil {
				return err
			}
			ctx := langCtx(r)
			at, err := h.svc.reports.AsOfFrom(ctx, pid, query(r))
			if err != nil {
				return err
			}
			doc, err := render(ctx, pid, at)
			if err != nil {
				return err
			}
			return writePDF(w, doc)
		}
	}
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/cancellations.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		ctx := langCtx(r)
		in, err := h.svc.reports.CancellationParamsFrom(ctx, pid, query(r))
		if err != nil {
			return err
		}
		doc, err := h.svc.CancellationsPDF(ctx, pid, in)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/guest-ledger.pdf", asOf(h.svc.GuestLedgerPDF))
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/deposit-ledger.pdf", asOf(h.svc.DepositLedgerPDF))
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/corrections.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		ctx := langCtx(r)
		in, err := h.svc.reports.CorrectionParamsFrom(ctx, pid, query(r))
		if err != nil {
			return err
		}
		doc, err := h.svc.CorrectionsPDF(ctx, pid, in)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/z-reports.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		ctx := langCtx(r)
		day, err := h.svc.reports.ZDateFrom(ctx, pid, r)
		if err != nil {
			return err
		}
		doc, err := h.svc.ZReportsPDF(ctx, pid, day)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
}
