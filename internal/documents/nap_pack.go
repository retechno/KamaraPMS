package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"kamarapms/internal/auditpack"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

// The PDF of the night audit pack (docs/architecture/38-reports-night-audit-pack.md, 8.4 and 10): a cover and a page range for each section, in English, made from the JSON the pack kept, never from the
// ledger. The same JSON gives the same bytes: nothing here reads a clock (the footer says when the pack was built, not when it was printed).

// PackRendererVersion is the version of the layout of the PDF. A change of the layout raises it: the e-mail of a pack that is sent again (PR 3) says which one it was made with.
const PackRendererVersion = 1

// PackSection is a section as the pack stores it.
type PackSection struct {
	Code      string
	Status    string
	Error     string
	Data      []byte
	BuiltLate bool
}

// PackInput is a pack to print: the sections to print, in the order of the catalog.
type PackInput struct {
	BusinessDate civil.Date
	Status       string
	BuiltAt      time.Time
	BuiltLate    bool
	Sections     []PackSection
}

// packTitles are the names of the sections.
var packTitles = map[string]string{
	auditpack.SecSummary: "Night audit summary", auditpack.SecFlash: "Daily flash report", auditpack.SecRevenue: "Revenue", auditpack.SecTax: "Tax and service charge",
	auditpack.SecPayments: "Payments", auditpack.SecZReports: "Z reports", auditpack.SecGuestLedger: "Guest ledger", auditpack.SecDepositLedger: "Deposit ledger",
	auditpack.SecCorrections: "Voids, corrections and discounts", auditpack.SecCancellations: "Cancellations and no-shows", auditpack.SecOutlets: "Outlets", auditpack.SecOutOfOrder: "Rooms out of order",
	auditpack.SecInHouse: "In-house guests", auditpack.SecNextDay: "Tomorrow's arrivals and departures", auditpack.SecAuditNotes: "Audit notes",
}

// napChargeType is the English name of a charge type, so that no code is printed.
func napChargeType(t string) string {
	switch t {
	case "ROOM":
		return "Rooms"
	case "FOOD_BEVERAGE":
		return "Food and beverage"
	case "SERVICE":
		return "Services"
	case "FEE":
		return "Fees"
	case "TRANSFER":
		return "Transfers"
	case "OTHER":
		return "Other"
	}
	return t
}

// PackSectionTitle is the English name of a section.
func PackSectionTitle(code string) string {
	if t, ok := packTitles[code]; ok {
		return t
	}
	return code
}

// packCtx is what a section needs to print figures.
type packCtx struct {
	dc docContext
	m  func(string) string
}

// RenderPack prints a pack: the cover, then each section from its first page. Two passes: the first learns the pages of the sections, which the cover lists.
func RenderPack(h Hotel, prop tenancy.Property, in PackInput) ([]byte, error) {
	l := LangEN
	dc := docContext{hotel: h, prop: prop, decimals: prop.CurrencyDecimals, lang: l,
		printed: "Night audit pack  ·  business date " + l.Date(in.BusinessDate) + "  ·  built " + l.Time(in.BuiltAt, prop.Location())}
	pc := packCtx{dc: dc, m: napMoney(l, dc.decimals)}
	pages := map[string]string{}
	var out []byte
	for pass := 0; pass < 2; pass++ {
		g := newPage(h, l, "NIGHT AUDIT PACK", dc.printed)
		heads := map[string]string{}
		for _, s := range in.Sections {
			heads[s.Code] = pc.headline(s)
		}
		pc.cover(g, in, heads, pages)
		for i, s := range in.Sections {
			g.p.AddPage()
			first := g.p.PageNo()
			g.color(muted)
			g.font("", 8)
			g.p.CellFormat(bodyW, 4.5, g.tr(fmt.Sprintf("%s  ·  Night audit pack  ·  %s  ·  section %d of %d", h.Name, l.Date(in.BusinessDate), i+1, len(in.Sections))), "", 1, "L", false, 0, "")
			g.color(ink)
			pc.section(g, s)
			g.p.Ln(3)
			g.note("As built at the close of the business day. A later correction is posted on a later business day.", "I", 8)
			last := g.p.PageNo()
			if last == first {
				pages[s.Code] = "page " + strconv.Itoa(first)
			} else {
				pages[s.Code] = "pages " + strconv.Itoa(first) + "-" + strconv.Itoa(last)
			}
		}
		b, err := g.bytes()
		if err != nil {
			return nil, err
		}
		out = b
	}
	return out, nil
}

func (pc packCtx) cover(g *page, in PackInput, heads, pages map[string]string) {
	l := pc.dc.lang
	g.title("NIGHT AUDIT PACK", l.Date(in.BusinessDate))
	closedBy := "-"
	for _, s := range in.Sections {
		if s.Code != auditpack.SecSummary || s.Status != auditpack.StatusOK {
			continue
		}
		var d auditpack.DaySummary
		if json.Unmarshal(s.Data, &d) == nil {
			closedBy = d.ClosedBy
			if d.ClosedAt != nil {
				closedBy += ", " + l.Time(*d.ClosedAt, pc.dc.prop.Location())
			}
		}
	}
	state := "complete"
	if in.Status == auditpack.PackPartial {
		state = "partial: a section failed"
	}
	if in.BuiltLate {
		state += ", built later from the ledger"
	}
	g.pairs([][2]string{
		{"Business date", l.Date(in.BusinessDate)}, {"Currency", pc.dc.prop.CurrencyCode},
		{"Closed by", closedBy}, {"Pack built", l.Time(in.BuiltAt, pc.dc.prop.Location()) + " (" + state + ")"},
	})
	g.section("Sections in this file")
	rows := make([][]string, 0, len(in.Sections))
	for i, s := range in.Sections {
		rows = append(rows, []string{strconv.Itoa(i + 1), PackSectionTitle(s.Code) + "\n" + heads[s.Code], pages[s.Code]})
	}
	g.table([]col{{10, "No.", "L"}, {145, "Section", "L"}, {25, "Pages", "R"}}, rows)
	g.p.Ln(3)
	g.note("Amounts in "+pc.dc.prop.CurrencyCode+". Net revenue excludes service charge and tax, which are shown as memo lines. Budget figures for a day and for month to date are a pro rata of the monthly budget.", "", 8)
	g.note("As built at the close of the business day. A later correction is posted on a later business day.", "", 8)
}

// section prints one section on its page. A section that is not OK says why instead.
func (pc packCtx) section(g *page, s PackSection) {
	switch s.Status {
	case auditpack.StatusFailed:
		g.title(PackSectionTitle(s.Code), "")
		g.color(alert)
		g.note("This section could not be built when the day was closed: "+s.Error, "B", 9)
		g.color(ink)
		return
	case auditpack.StatusOff:
		g.title(PackSectionTitle(s.Code), "")
		g.note("Not applicable to this property.", "I", 9)
		return
	case auditpack.StatusNotAvailable:
		g.title(PackSectionTitle(s.Code), "")
		g.note("Not available: only the close of the night saw this, and the pack of this day was built later from the ledger.", "I", 9)
		return
	}
	if err := pc.body(g, s); err != nil {
		g.title(PackSectionTitle(s.Code), "")
		g.color(alert)
		g.note("The stored data of this section could not be read.", "B", 9)
		g.color(ink)
	}
}

func decodeSection[T any](s PackSection) (T, error) {
	var v T
	err := json.Unmarshal(s.Data, &v)
	return v, err
}

func (pc packCtx) body(g *page, s PackSection) error {
	dc := pc.dc
	g.empty = "None"
	if s.Code == auditpack.SecPayments {
		g.empty = "No payments"
	}
	switch s.Code {
	case auditpack.SecSummary:
		v, err := decodeSection[auditpack.DaySummary](s)
		if err != nil {
			return err
		}
		var sum nightaudit.Summary
		if err := json.Unmarshal(v.Summary, &sum); err != nil {
			return err
		}
		pc.summary(g, v, sum)
	case auditpack.SecFlash:
		v, err := decodeSection[reports.DailyFlash](s)
		if err != nil {
			return err
		}
		renderFlash(g, dc, v, false)
	case auditpack.SecRevenue:
		v, err := decodeSection[reports.Revenue](s)
		if err != nil {
			return err
		}
		pc.revenue(g, v)
	case auditpack.SecTax:
		v, err := decodeSection[reports.TaxReport](s)
		if err != nil {
			return err
		}
		pc.tax(g, v)
	case auditpack.SecPayments:
		v, err := decodeSection[reports.Cashier](s)
		if err != nil {
			return err
		}
		pc.payments(g, v)
	case auditpack.SecZReports:
		v, err := decodeSection[reports.ZReport](s)
		if err != nil {
			return err
		}
		renderZReports(g, dc, v)
	case auditpack.SecGuestLedger:
		v, err := decodeSection[reports.GuestLedgerReport](s)
		if err != nil {
			return err
		}
		renderGuestLedger(g, dc, v)
	case auditpack.SecDepositLedger:
		v, err := decodeSection[reports.DepositLedgerReport](s)
		if err != nil {
			return err
		}
		renderDepositLedger(g, dc, v)
	case auditpack.SecCorrections:
		v, err := decodeSection[reports.CorrectionReport](s)
		if err != nil {
			return err
		}
		renderCorrections(g, dc, v)
	case auditpack.SecCancellations:
		v, err := decodeSection[reports.CancellationReport](s)
		if err != nil {
			return err
		}
		renderCancellations(g, dc, v)
	case auditpack.SecOutlets:
		v, err := decodeSection[auditpack.OutletsData](s)
		if err != nil {
			return err
		}
		pc.outlets(g, v)
	case auditpack.SecOutOfOrder:
		v, err := decodeSection[auditpack.OutOfOrderData](s)
		if err != nil {
			return err
		}
		pc.outOfOrder(g, v)
	case auditpack.SecInHouse:
		v, err := decodeSection[reports.StayList](s)
		if err != nil {
			return err
		}
		g.title(PackSectionTitle(s.Code), plural(len(v.Rows), "stay", "stays"))
		pc.stays(g, v)
	case auditpack.SecNextDay:
		v, err := decodeSection[nextDay](s)
		if err != nil {
			return err
		}
		pc.nextDay(g, v)
	case auditpack.SecAuditNotes:
		v, err := decodeSection[nightaudit.PackNotes](s)
		if err != nil {
			return err
		}
		pc.notes(g, v)
	default:
		return fmt.Errorf("unknown section %q", s.Code)
	}
	return nil
}

// nextDay is the JSON of the section NEXT_DAY.
type nextDay struct {
	Date       civil.Date          `json:"date"`
	Arrivals   reports.ArrivalList `json:"arrivals"`
	Departures reports.StayList    `json:"departures"`
}

func (pc packCtx) summary(g *page, v auditpack.DaySummary, sum nightaudit.Summary) {
	l, m := pc.dc.lang, pc.m
	g.title("NIGHT AUDIT SUMMARY", l.Date(v.BusinessDate))
	pct := sum.OccupancyPercent
	g.pairs([][2]string{
		{"Occupancy", pct + " %"}, {"Rooms sold", strconv.Itoa(sum.Rooms.Sold)},
		{"ADR", m(sum.ADR)}, {"RevPAR", m(sum.RevPAR)},
		{"Arrivals", strconv.Itoa(sum.Arrivals)}, {"Departures", strconv.Itoa(sum.Departures)},
		{"No-shows", strconv.Itoa(sum.NoShows)}, {"Room charges posted", strconv.Itoa(sum.RoomChargesPosted)},
	})
	g.section("Rooms")
	g.table([]col{{60, "", "L"}, {30, "Rooms", "R"}}, [][]string{
		{"Total", strconv.Itoa(sum.Rooms.Total)}, {"Out of order", strconv.Itoa(sum.Rooms.OutOfOrder)}, {"Out of service", strconv.Itoa(sum.Rooms.OutOfService)},
		{"House use", strconv.Itoa(sum.Rooms.HouseUse)}, {"Sellable", strconv.Itoa(sum.Rooms.Sellable)}, {"Occupied", strconv.Itoa(sum.Rooms.Occupied)},
		{"Complimentary", strconv.Itoa(sum.Rooms.Complimentary)},
	})
	g.section("Room revenue")
	g.table([]col{{60, "", "L"}, {40, "Amount", "R"}}, [][]string{{"Net", m(sum.RoomRevenue.Net)}, {"Service charge", m(sum.RoomRevenue.Service)}, {"Tax", m(sum.RoomRevenue.Tax)}})
	g.section("Revenue by charge type")
	rows := make([][]string, 0, len(sum.RevenueByChargeType))
	for _, r := range sum.RevenueByChargeType {
		rows = append(rows, []string{napChargeType(r.ChargeType), m(r.Net), m(r.Service), m(r.Tax)})
	}
	g.table([]col{{60, "Charge type", "L"}, {40, "Net", "R"}, {40, "Service", "R"}, {40, "Tax", "R"}}, rows)
	g.section("Payments by method")
	g.empty = "No payments"
	pay := make([][]string, 0, len(sum.PaymentsByMethod))
	for _, r := range sum.PaymentsByMethod {
		pay = append(pay, []string{napMethod(r.Method), m(r.Payments), m(r.Refunds), m(r.Net)})
	}
	g.table([]col{{60, "Method", "L"}, {40, "Payments", "R"}, {40, "Refunds", "R"}, {40, "Net", "R"}}, pay)
	g.empty = "None"
	g.section("City ledger")
	g.table([]col{{80, "", "L"}, {40, "Amount", "R"}}, [][]string{
		{"Transferred from folios", m(sum.CityLedger.Transferred)}, {"Received", m(sum.CityLedger.Received)}, {"Outstanding", m(sum.CityLedger.Outstanding)},
	})
}

func (pc packCtx) revenue(g *page, v reports.Revenue) {
	m := pc.m
	g.title("REVENUE", pc.dc.lang.Date(v.From))
	rows := make([][]string, 0, len(v.ByCode)+1)
	for _, x := range v.ByCode {
		rows = append(rows, []string{napChargeType(x.ChargeType), x.ChargeCode, x.Name, strconv.Itoa(x.Items), m(x.Net), m(x.Service), m(x.Tax), m(x.Total)})
	}
	rows = append(rows, []string{"Total", "", "", strconv.Itoa(v.Totals.Items), m(v.Totals.Net), m(v.Totals.Service), m(v.Totals.Tax), m(v.Totals.Total)})
	last := len(rows) - 1
	g.tableB([]col{{22, "Type", "L"}, {20, "Code", "L"}, {38, "Name", "L"}, {12, "Items", "R"}, {22, "Net", "R"}, {20, "Service", "R"}, {20, "Tax", "R"}, {26, "Total", "R"}}, rows, func(i int) bool { return i == last })
}

func (pc packCtx) tax(g *page, v reports.TaxReport) {
	m := pc.m
	g.title("TAX AND SERVICE CHARGE", pc.dc.lang.Date(v.From))
	cols := []col{{20, "Code", "L"}, {50, "Name", "L"}, {16, "Rate %", "R"}, {14, "Items", "R"}, {40, "Base", "R"}, {40, "Amount", "R"}}
	lines := func(ls []reports.TaxLine) [][]string {
		rows := make([][]string, 0, len(ls))
		for _, x := range ls {
			rows = append(rows, []string{x.Code, x.Name, x.Rate, strconv.Itoa(x.Items), m(x.Base), m(x.Amount)})
		}
		return rows
	}
	g.section("Taxes")
	g.table(cols, append(lines(v.Taxes), []string{"Total", "", "", "", "", m(v.TaxTotal)}))
	g.section("Service charges")
	g.table(cols, append(lines(v.ServiceCharges), []string{"Total", "", "", "", "", m(v.ServiceTotal)}))
}

func (pc packCtx) payments(g *page, v reports.Cashier) {
	m := pc.m
	g.title("PAYMENTS", pc.dc.lang.Date(v.From))
	rows := make([][]string, 0, len(v.ByMethod)+1)
	for _, x := range v.ByMethod {
		rows = append(rows, []string{napMethod(x.Method), m(x.Payments), m(x.Refunds), m(x.Net)})
	}
	rows = append(rows, []string{"Total", "", "", m(v.Net)})
	last := len(rows) - 1
	g.tableB([]col{{60, "Method", "L"}, {40, "Payments", "R"}, {40, "Refunds", "R"}, {40, "Net", "R"}}, rows, func(i int) bool { return i == last })
	g.section("Detail by method")
	det := make([][]string, 0, len(v.Lines))
	for _, x := range v.Lines {
		det = append(det, []string{napMethod(x.Method), strconv.Itoa(x.Count), m(x.Payments), m(x.Refunds), m(x.Net), m(x.Voided)})
	}
	g.table([]col{{40, "Method", "L"}, {16, "Count", "R"}, {30, "Payments", "R"}, {30, "Refunds", "R"}, {30, "Net", "R"}, {34, "Voided", "R"}}, det)
}

func (pc packCtx) outlets(g *page, v auditpack.OutletsData) {
	m := pc.m
	g.title("OUTLETS", pc.dc.lang.Date(v.Date))
	row := func(x auditpack.OutletRow) []string {
		return []string{x.Name, strconv.Itoa(x.Bills), strconv.Itoa(x.Covers), m(x.Net), m(x.Service), m(x.Tax), m(x.Total)}
	}
	rows := make([][]string, 0, len(v.Outlets)+1)
	for _, x := range v.Outlets {
		rows = append(rows, row(x))
	}
	rows = append(rows, row(v.Total))
	last := len(rows) - 1
	g.tableB([]col{{42, "Outlet", "L"}, {14, "Bills", "R"}, {16, "Covers", "R"}, {26, "Net", "R"}, {24, "Service", "R"}, {24, "Tax", "R"}, {34, "Total", "R"}}, rows, func(i int) bool { return i == last })
	g.section("Voids, complimentary and discounts")
	ev := make([][]string, 0, len(v.Outlets)+1)
	evRow := func(x auditpack.OutletRow) []string {
		return []string{x.Name, strconv.Itoa(x.Voids.Count), m(x.Voids.Amount), strconv.Itoa(x.Comps.Count), m(x.Comps.Amount), strconv.Itoa(x.Discounts.Count), m(x.Discounts.Amount)}
	}
	for _, x := range v.Outlets {
		ev = append(ev, evRow(x))
	}
	ev = append(ev, evRow(v.Total))
	last = len(ev) - 1
	g.tableB([]col{{42, "Outlet", "L"}, {14, "Voids", "R"}, {28, "Void amount", "R"}, {14, "Comps", "R"}, {28, "Comp amount", "R"}, {14, "Discounts", "R"}, {40, "Discount amount", "R"}}, ev, func(i int) bool { return i == last })
	if v.Total.BillsWithoutCovers > 0 {
		g.note(plural(v.Total.BillsWithoutCovers, "bill has", "bills have")+" no guest count: their covers are not counted.", "I", 8.5)
	}
}

func (pc packCtx) outOfOrder(g *page, v auditpack.OutOfOrderData) {
	l := pc.dc.lang
	g.title("ROOMS OUT OF ORDER", l.Date(v.Date))
	g.pairs([][2]string{{"Out of order", strconv.Itoa(v.OutOfOrder)}, {"Out of service", strconv.Itoa(v.OutOfService)}})
	rows := make([][]string, 0, len(v.Rooms))
	for _, r := range v.Rooms {
		kind := "Out of order"
		if r.Kind == "OOS" {
			kind = "Out of service"
		}
		rows = append(rows, []string{r.Room, r.RoomType, kind, l.Date(r.From), l.Date(r.Until), r.Reason})
	}
	g.table([]col{{16, "Room", "L"}, {24, "Room type", "L"}, {28, "Status", "L"}, {22, "From", "L"}, {22, "Until", "L"}, {68, "Reason", "L"}}, rows)
}

func (pc packCtx) stays(g *page, v reports.StayList) {
	l, m := pc.dc.lang, pc.m
	rows := make([][]string, 0, len(v.Rows))
	for _, r := range v.Rows {
		rows = append(rows, []string{r.StayNumber, r.Room, r.Guest, l.Date(r.ArrivalDate), l.Date(r.DepartureDate), strconv.Itoa(r.Adults) + "+" + strconv.Itoa(r.Children), m(r.Balance)})
	}
	g.table([]col{{22, "Stay", "L"}, {14, "Room", "L"}, {50, "Guest", "L"}, {22, "Arrival", "L"}, {22, "Departure", "L"}, {14, "Pax", "R"}, {36, "Balance", "R"}}, rows)
}

func (pc packCtx) nextDay(g *page, v nextDay) {
	l := pc.dc.lang
	g.title("TOMORROW'S ARRIVALS AND DEPARTURES", l.Date(v.Date))
	g.section("Arrivals")
	ar := make([][]string, 0, len(v.Arrivals.Rows))
	for _, r := range v.Arrivals.Rows {
		ar = append(ar, []string{r.ConfirmationNumber, r.Status, r.Guest, r.RoomType, r.Room, l.Date(r.DepartureDate), strconv.Itoa(r.Adults) + "+" + strconv.Itoa(r.Children)})
	}
	g.table([]col{{28, "Reservation", "L"}, {24, "Status", "L"}, {46, "Guest", "L"}, {22, "Room type", "L"}, {14, "Room", "L"}, {28, "Departure", "L"}, {18, "Pax", "R"}}, ar)
	g.section("Departures")
	pc.stays(g, v.Departures)
}

func (pc packCtx) notes(g *page, v nightaudit.PackNotes) {
	g.title("AUDIT NOTES", "")
	w := v.Warnings
	g.pairs([][2]string{{"Room charges posted", strconv.Itoa(v.RoomChargesPosted)}})
	m := pc.m
	none := true
	list := func(title string, rows [][]string, cols []col) {
		if len(rows) == 0 {
			return
		}
		none = false
		g.section(title)
		g.table(cols, rows)
	}
	var drafts, folios, masters, closed, blocks, bills, stock [][]string
	for _, x := range w.StaleDrafts {
		drafts = append(drafts, []string{x.ConfirmationNumber, pc.dc.lang.Date(x.ArrivalDate)})
	}
	for _, x := range w.OpenFolios {
		folios = append(folios, []string{x.FolioNumber, x.ConfirmationNumber, m(x.Balance)})
	}
	for _, x := range w.OpenMasters {
		masters = append(masters, []string{x.FolioNumber, x.GroupCode + " " + x.GroupName, m(x.Balance)})
	}
	for _, x := range w.ClosedMasters {
		closed = append(closed, []string{x.GroupCode + " " + x.GroupName, x.FolioNumber, strconv.Itoa(x.Stays)})
	}
	for _, x := range w.BlocksEnding {
		blocks = append(blocks, []string{x.Room, x.BlockType, pc.dc.lang.Date(x.EndDate)})
	}
	for _, x := range w.OutletBills {
		bills = append(bills, []string{x.Ref, x.Message})
	}
	for _, x := range w.Stock {
		stock = append(stock, []string{x.Ref, x.Message})
	}
	list("Reservations still waiting for a decision", drafts, []col{{60, "Reservation", "L"}, {120, "Arrival", "L"}})
	list("Open folios of cancelled reservations", folios, []col{{50, "Folio", "L"}, {70, "Reservation", "L"}, {60, "Balance", "R"}})
	list("Open master folios of groups that have left", masters, []col{{50, "Folio", "L"}, {80, "Group", "L"}, {50, "Balance", "R"}})
	list("Groups whose master folio is closed", closed, []col{{90, "Group", "L"}, {50, "Folio", "L"}, {40, "Stays", "R"}})
	var partial [][]string
	for _, d := range w.PackIncomplete {
		partial = append(partial, []string{pc.dc.lang.Date(d)})
	}
	list("Earlier packs with a section that failed", partial, []col{{180, "Business date", "L"}})
	list("Room blocks ending today", blocks, []col{{40, "Room", "L"}, {40, "Kind", "L"}, {100, "Ends", "L"}})
	list("Open bills of the outlets", bills, []col{{40, "Reference", "L"}, {140, "Finding", "L"}})
	list("Stock", stock, []col{{40, "Reference", "L"}, {140, "Finding", "L"}})
	if none {
		g.note("The audit had no findings.", "I", 9)
	}
}

// headline is the one line under the name of a section on the cover.
func (pc packCtx) headline(s PackSection) string {
	switch s.Status {
	case auditpack.StatusFailed:
		return "Could not be built: " + s.Error
	case auditpack.StatusOff:
		return "Not applicable to this property."
	case auditpack.StatusNotAvailable:
		return "Not available: only the close of the night saw this."
	}
	h, err := pc.headlineOK(s)
	if err != nil {
		return "The stored data could not be read."
	}
	return h
}

func (pc packCtx) headlineOK(s PackSection) (string, error) {
	m := pc.m
	switch s.Code {
	case auditpack.SecSummary:
		v, err := decodeSection[auditpack.DaySummary](s)
		if err != nil {
			return "", err
		}
		var sum nightaudit.Summary
		if err := json.Unmarshal(v.Summary, &sum); err != nil {
			return "", err
		}
		return "Occupancy " + sum.OccupancyPercent + "% · ADR " + m(sum.ADR) + " · RevPAR " + m(sum.RevPAR) + " · room revenue " + m(sum.RoomRevenue.Net), nil
	case auditpack.SecFlash:
		v, err := decodeSection[reports.DailyFlash](s)
		if err != nil {
			return "", err
		}
		out := "Net revenue today " + napFlash(m, v.Revenue.TotalNet.Today) + " · GTR " + napFlash(m, v.Revenue.GTR.Today) + " · payments (GTP) " + napFlash(m, v.Payments.GTP.Today)
		if v.Control != nil {
			if v.Control.Balanced {
				out += " · control balance 0"
			} else {
				out += " · control balance difference " + napFlash(m, v.Control.Today.Difference)
			}
		}
		return out, nil
	case auditpack.SecRevenue:
		v, err := decodeSection[reports.Revenue](s)
		if err != nil {
			return "", err
		}
		return "Net " + m(v.Totals.Net) + " · service " + m(v.Totals.Service) + " · tax " + m(v.Totals.Tax), nil
	case auditpack.SecTax:
		v, err := decodeSection[reports.TaxReport](s)
		if err != nil {
			return "", err
		}
		return "Tax " + m(v.TaxTotal) + " · service charge " + m(v.ServiceTotal), nil
	case auditpack.SecPayments:
		v, err := decodeSection[reports.Cashier](s)
		if err != nil {
			return "", err
		}
		return "Net payments " + m(v.Net) + " in " + plural(len(v.ByMethod), "method", "methods"), nil
	case auditpack.SecZReports:
		v, err := decodeSection[reports.ZReport](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Blocks), "shift report", "shift reports") + " · takings " + m(v.Property.TotalTakings), nil
	case auditpack.SecGuestLedger:
		v, err := decodeSection[reports.GuestLedgerReport](s)
		if err != nil {
			return "", err
		}
		return "Balance " + m(v.Balance) + " in " + plural(len(v.Rows), "folio", "folios"), nil
	case auditpack.SecDepositLedger:
		v, err := decodeSection[reports.DepositLedgerReport](s)
		if err != nil {
			return "", err
		}
		return "Closing " + m(v.Closing) + " in " + plural(len(v.Rows), "folio", "folios"), nil
	case auditpack.SecCorrections:
		v, err := decodeSection[reports.CorrectionReport](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Rows), "event", "events") + " · revenue taken back " + m(v.RevenueTakenBack), nil
	case auditpack.SecCancellations:
		v, err := decodeSection[reports.CancellationReport](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Events), "event", "events") + " · fees posted " + m(v.FeesPosted), nil
	case auditpack.SecOutlets:
		v, err := decodeSection[auditpack.OutletsData](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Outlets), "outlet", "outlets") + " · net " + m(v.Total.Net) + " · " + plural(v.Total.Covers, "cover", "covers"), nil
	case auditpack.SecOutOfOrder:
		v, err := decodeSection[auditpack.OutOfOrderData](s)
		if err != nil {
			return "", err
		}
		return plural(v.OutOfOrder, "room", "rooms") + " out of order · " + strconv.Itoa(v.OutOfService) + " out of service", nil
	case auditpack.SecInHouse:
		v, err := decodeSection[reports.StayList](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Rows), "stay", "stays") + " in house", nil
	case auditpack.SecNextDay:
		v, err := decodeSection[nextDay](s)
		if err != nil {
			return "", err
		}
		return plural(len(v.Arrivals.Rows), "arrival", "arrivals") + " · " + plural(len(v.Departures.Rows), "departure", "departures") + " on " + pc.dc.lang.Date(v.Date), nil
	case auditpack.SecAuditNotes:
		v, err := decodeSection[nightaudit.PackNotes](s)
		if err != nil {
			return "", err
		}
		w := v.Warnings
		n := len(w.StaleDrafts) + len(w.OpenFolios) + len(w.OpenMasters) + len(w.ClosedMasters) + len(w.BlocksEnding) + len(w.OutletBills) + len(w.Stock) + len(w.PackIncomplete)
		return plural(v.RoomChargesPosted, "room charge", "room charges") + " posted · " + plural(n, "finding", "findings"), nil
	}
	return "", fmt.Errorf("unknown section %q", s.Code)
}

// napFlash is a cell of the flash as a figure; a blank cell is a dash.
func napFlash(m func(string) string, v string) string {
	if v == "" {
		return "-"
	}
	return m(v)
}

// plural is a number with its noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// SetPacks gives the documents the archive of the night audit packs.
func (s *Service) SetPacks(p *auditpack.Service) { s.packs = p }

// NightAuditPackPDF draws the pack of a business date from the sections it kept (nightaudit.pack_view): every section that holds data, or the ones asked for. The language is English.
func (s *Service) NightAuditPackPDF(ctx context.Context, propertyID int64, date civil.Date, sections []string) (string, []byte, error) {
	got, err := s.packs.ForPDF(ctx, propertyID, date, sections)
	if err != nil {
		return "", nil, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return "", nil, err
	}
	in := PackInput{BusinessDate: date, Status: got.Manifest.Status, BuiltAt: got.Manifest.BuiltAt, BuiltLate: got.Manifest.BuiltLate}
	for _, sec := range got.Sections {
		in.Sections = append(in.Sections, PackSection{Code: sec.Code, Status: sec.Status, Data: sec.Data, BuiltLate: sec.BuiltLate})
	}
	h := Hotel{Name: prop.Name, Address: prop.Address, City: prop.City, Country: prop.CountryCode, Phone: prop.Phone, Email: prop.Email, TaxID: prop.TaxID, Footer: prop.DocumentFooter}
	pdf, err := RenderPack(h, prop, in)
	return "night-audit-pack-" + date.String() + ".pdf", pdf, err
}
