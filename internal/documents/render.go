package documents

import (
	"strings"

	"kamarapms/internal/platform/civil"
)

// Party is the guest block of a document. Empty fields print as a dash, or as a line to write on (registration card).
type Party struct {
	Name, Address, City, Email, Phone, Nationality, IDType, IDNumber, DateOfBirth string
}

func (p Party) addressLine() string { return strings.Join(nonEmpty(p.Address, p.City), ", ") }

// InvoiceLine is a ledger line with signed amounts already split into debit and credit and formatted.
type InvoiceLine struct {
	Date                civil.Date
	Description         string
	Debit, Credit       string
	DebitSet, CreditSet bool
}

// Amount is a labelled amount of the summary.
type Amount struct{ Label, Value string }

// InvoiceData is a guest folio as a document. A closed folio is an invoice; an open one is a bill that can still
// change and says so.
type InvoiceData struct {
	Hotel     Hotel
	Printed   string
	Number    string
	Final     bool
	Guest     Party
	Booking   string
	Stay      string
	Room      string
	Arrival   civil.Date
	Departure civil.Date
	Currency  string
	Lines     []InvoiceLine
	Summary   []Amount // net, service charges, taxes, total charges, payments, balance (the last one is emphasised)
}

// RenderInvoice draws the invoice or guest bill.
func RenderInvoice(d InvoiceData) ([]byte, error) {
	title := "INVOICE"
	if !d.Final {
		title = "GUEST BILL (not final)"
	}
	g := newPage(d.Hotel, title, d.Printed)
	g.title(title, d.Number)
	g.pairs([][2]string{
		{"Guest", d.Guest.Name}, {"Reservation", d.Booking},
		{"Address", d.Guest.addressLine()}, {"Stay", d.Stay},
		{"Room", d.Room}, {"Stay dates", fmtDate(d.Arrival) + " to " + fmtDate(d.Departure)},
	})
	g.section("Charges and payments (" + d.Currency + ")")
	rows := make([][]string, 0, len(d.Lines))
	for _, l := range d.Lines {
		debit, credit := "", ""
		if l.DebitSet {
			debit = l.Debit
		}
		if l.CreditSet {
			credit = l.Credit
		}
		rows = append(rows, []string{fmtDate(l.Date), l.Description, debit, credit})
	}
	g.table([]col{{24, "Date", "L"}, {86, "Description", "L"}, {35, "Debit", "R"}, {35, "Credit", "R"}}, rows)
	items := make([][2]string, len(d.Summary))
	for i, a := range d.Summary {
		items[i] = [2]string{a.Label, a.Value}
	}
	g.totals(items)
	if !d.Final {
		g.p.Ln(3)
		g.color(muted)
		g.note("This bill is not final: charges can still be posted until check-out. A final invoice is issued when the folio is closed.", "I", 8.5)
	}
	return g.bytes()
}

// CardData is the registration card of a stay.
type CardData struct {
	Hotel        Hotel
	Printed      string
	Number       string // the stay number
	Booking      string
	Guest        Party
	Companions   []string
	Room         string
	RoomType     string
	Arrival      civil.Date
	CheckInTime  string
	Departure    civil.Date
	CheckOutTime string
	Nights       int
	Adults       int
	Children     int
	RatePlan     string
	NightlyRate  string // formatted, may be empty
	Currency     string
	SpecialNeeds string
	Terms        string
}

// RenderRegistrationCard draws the card the guest signs at check-in.
func RenderRegistrationCard(d CardData) ([]byte, error) {
	g := newPage(d.Hotel, "Registration card", d.Printed)
	g.title("REGISTRATION CARD", d.Number)
	g.section("Stay")
	rate := d.NightlyRate
	if rate != "" {
		rate = d.Currency + " " + rate + " per night"
	}
	g.pairs([][2]string{
		{"Reservation", d.Booking}, {"Room", strings.Join(nonEmpty(d.Room, d.RoomType), "  ·  ")},
		{"Arrival", fmtDate(d.Arrival) + "  (check-in from " + d.CheckInTime + ")"}, {"Departure", fmtDate(d.Departure) + "  (check-out by " + d.CheckOutTime + ")"},
		{"Nights", itoa(d.Nights)}, {"Guests", itoa(d.Adults) + " adult(s), " + itoa(d.Children) + " child(ren)"},
		{"Rate plan", d.RatePlan}, {"Rate", rate},
	})
	g.section("Guest")
	g.pairs([][2]string{
		{"Name", d.Guest.Name}, {"Nationality", d.Guest.Nationality},
		{"Date of birth", d.Guest.DateOfBirth}, {"ID document", strings.Join(nonEmpty(d.Guest.IDType, d.Guest.IDNumber), "  ")},
		{"Address", d.Guest.addressLine()}, {"Phone", d.Guest.Phone},
		{"E-mail", d.Guest.Email}, {"Special requests", d.SpecialNeeds},
	})
	if len(d.Companions) > 0 {
		g.section("Accompanying guests")
		g.font("", 10)
		for _, c := range d.Companions {
			g.p.CellFormat(bodyW, 5.2, g.tr("- "+c), "", 1, "L", false, 0, "")
		}
	}
	g.section("Terms")
	g.color(muted)
	g.note(d.Terms, "", 8.5)
	g.color(ink)
	g.p.Ln(16)
	y := g.p.GetY()
	g.signature(margin, y, 80, "Guest signature")
	g.signature(margin+100, y, 80, "Received by (front desk)")
	return g.bytes()
}

// ReceiptData is a payment or a refund.
type ReceiptData struct {
	Hotel     Hotel
	Printed   string
	Number    string
	Refund    bool
	Voided    bool
	VoidNote  string
	Guest     Party
	Folio     string
	Booking   string
	Date      civil.Date
	At        string
	Method    string
	Reference string
	Currency  string
	Amount    string
	RefundOf  string
	Remarks   string
}

// RenderReceipt draws a payment or refund receipt. A voided payment is stamped, so a cancelled receipt cannot pass
// for a valid one.
func RenderReceipt(d ReceiptData) ([]byte, error) {
	title := "PAYMENT RECEIPT"
	label := "Amount received"
	if d.Refund {
		title, label = "REFUND RECEIPT", "Amount refunded"
	}
	g := newPage(d.Hotel, title, d.Printed)
	g.title(title, d.Number)
	if d.Voided {
		g.stamp("VOID - this payment was cancelled")
		if d.VoidNote != "" {
			g.color(muted)
			g.note(d.VoidNote, "I", 9)
			g.color(ink)
		}
		g.p.Ln(2)
	}
	who := "Received from"
	if d.Refund {
		who = "Refunded to"
	}
	g.pairs([][2]string{
		{who, d.Guest.Name}, {"Date", fmtDate(d.Date) + "  " + d.At},
		{"Folio", d.Folio}, {"Reservation", d.Booking},
		{"Method", d.Method}, {"Reference", d.Reference},
	})
	if d.RefundOf != "" {
		g.pairs([][2]string{{"Refund of payment", d.RefundOf}, {"", ""}})
	}
	g.p.Ln(4)
	g.p.SetFillColor(band[0], band[1], band[2])
	g.font("", 10)
	g.p.CellFormat(bodyW*0.5, 12, g.tr("  "+label), "", 0, "L", true, 0, "")
	g.font("B", 15)
	g.p.CellFormat(bodyW*0.5, 12, g.tr(d.Currency+" "+d.Amount+"  "), "", 1, "R", true, 0, "")
	if d.Remarks != "" {
		g.p.Ln(3)
		g.note("Note: "+d.Remarks, "", 9)
	}
	g.p.Ln(18)
	g.signature(margin+100, g.p.GetY(), 80, "Cashier")
	return g.bytes()
}

// BookedRoom is a line of the confirmation.
type BookedRoom struct {
	RoomType  string
	RatePlan  string
	Arrival   civil.Date
	Departure civil.Date
	Nights    int
	Guests    string
	Estimate  string
}

// ConfirmationData is a reservation confirmation letter.
type ConfirmationData struct {
	Hotel        Hotel
	Printed      string
	Number       string
	Status       string
	Guest        Party
	Booked       civil.Date
	Rooms        []BookedRoom
	Currency     string
	Total        string
	CheckInTime  string
	CheckOutTime string
	Request      string
}

// RenderConfirmation draws the confirmation sent to the guest.
func RenderConfirmation(d ConfirmationData) ([]byte, error) {
	g := newPage(d.Hotel, "Reservation confirmation", d.Printed)
	g.title("RESERVATION CONFIRMATION", d.Number)
	g.pairs([][2]string{
		{"Guest", d.Guest.Name}, {"Status", d.Status},
		{"Booked on", fmtDate(d.Booked)}, {"Check-in / check-out", "from " + d.CheckInTime + " / by " + d.CheckOutTime},
	})
	g.section("Your rooms")
	rows := make([][]string, 0, len(d.Rooms))
	for _, r := range d.Rooms {
		rows = append(rows, []string{r.RoomType, r.RatePlan, fmtDate(r.Arrival), fmtDate(r.Departure), itoa(r.Nights), r.Guests, r.Estimate})
	}
	g.table([]col{{30, "Room type", "L"}, {24, "Rate plan", "L"}, {25, "Arrival", "L"}, {25, "Departure", "L"}, {12, "Nights", "R"}, {24, "Guests", "L"}, {40, "Estimate (" + d.Currency + ")", "R"}}, rows)
	g.totals([][2]string{{"Estimated total", d.Total}})
	if d.Request != "" {
		g.section("Your request")
		g.note(d.Request, "", 9.5)
	}
	g.p.Ln(4)
	g.color(muted)
	g.note("Please quote the confirmation number when you contact us. The estimate is calculated from the rates booked; the final amount is the invoice issued at check-out.", "I", 8.5)
	return g.bytes()
}

// StatementLine is a movement on a company's statement.
type StatementLine struct {
	Date        civil.Date
	Number      string
	Description string
	Reference   string
	Status      string
	Debit       string
	Credit      string
	Balance     string
}

// StatementData is a company's city ledger statement for a period.
type StatementData struct {
	Hotel     Hotel
	Printed   string
	Company   Party
	Code      string
	TaxID     string
	Period    string
	Terms     string
	Currency  string
	Opening   string
	Lines     []StatementLine
	Debit     string
	Credit    string
	Closing   string
	AgingAsOf string
	Aging     [][2]string
}

// RenderStatement draws the statement of account of a company: transfers owed (debit), receipts (credit) and the
// balance after each, with the aging of what is still open. Voided lines are listed and marked, and do not count.
func RenderStatement(d StatementData) ([]byte, error) {
	g := newPage(d.Hotel, "STATEMENT OF ACCOUNT", d.Printed)
	g.title("STATEMENT OF ACCOUNT", d.Code)
	g.pairs([][2]string{
		{"Company", d.Company.Name}, {"Period", d.Period},
		{"Address", d.Company.addressLine()}, {"Payment terms", d.Terms},
		{"Tax ID", d.TaxID}, {"Currency", d.Currency},
	})
	g.p.Ln(3)
	rows := [][]string{{"", "", "Opening balance", "", "", "", d.Opening}}
	for _, l := range d.Lines {
		desc := l.Description
		if l.Status == "VOIDED" {
			desc += " (voided)"
		}
		rows = append(rows, []string{fmtDate(l.Date), l.Number, desc, l.Reference, l.Debit, l.Credit, l.Balance})
	}
	g.table([]col{
		{23, "Date", "L"}, {25, "Number", "L"}, {42, "Description", "L"}, {30, "Reference", "L"},
		{20, "Debit", "R"}, {20, "Credit", "R"}, {20, "Balance", "R"},
	}, rows)
	g.totals([][2]string{{"Total debit", d.Debit}, {"Total credit", d.Credit}, {"Balance due", d.Currency + " " + d.Closing}})
	if len(d.Aging) > 0 {
		g.p.Ln(4)
		g.section("Aging as of " + d.AgingAsOf + " (days since the folio was transferred)")
		g.pairs(pairUp(d.Aging))
	}
	return g.bytes()
}

// pairUp lays label/value items out two to a row for pairs().
func pairUp(items [][2]string) [][2]string {
	out := make([][2]string, 0, len(items)+1)
	out = append(out, items...)
	if len(out)%2 == 1 {
		out = append(out, [2]string{"", ""})
	}
	return out
}
