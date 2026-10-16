package printing

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Document kinds.
const (
	KindReceipt = "RECEIPT"
	KindPrebill = "PREBILL"
	KindTicket  = "TICKET"
	KindDrawer  = "DRAWER"
)

// Labels are the words printed on paper. The paper of an Indonesian hotel is in Indonesian.
type Labels struct {
	Bill, Table, Room, Guest, Cashier, Waiter, Time, Net, Service, Tax, Rounding, Total, ToPay, Change, Balance string
	NotReceipt, RoomSheet, Signature, Thanks, Addition, Cancel, Order, Round, Ticket, Note, Customer, Sent      string
	Cash, Card, QRIS, Bank, Other, RoomCharge, Company, Comp                                                    string
	TaxID                                                                                                       string
}

// LabelsID is the wording of the papers.
var LabelsID = Labels{
	Bill: "Bill", Table: "MEJA", Room: "KAMAR", Guest: "Tamu", Cashier: "Kasir", Waiter: "Pelayan", Time: "Jam", Net: "Neto", Service: "Layanan", Tax: "Pajak",
	Rounding: "Pembulatan", Total: "TOTAL", ToPay: "YANG HARUS DIBAYAR", Change: "Kembali", Balance: "Sisa",
	NotReceipt: "*** BUKAN STRUK ***", RoomSheet: "LEMBAR BEBAN KE KAMAR", Signature: "Tanda tangan tamu", Thanks: "Terima kasih",
	Addition: "TAMBAHAN", Cancel: "BATAL", Order: "Pesanan", Round: "Ronde", Ticket: "T", Note: "Catatan", Customer: "Pelanggan", Sent: "Kirim",
	Cash: "Tunai", Card: "Kartu", QRIS: "QRIS", Bank: "Transfer", Other: "Lainnya", RoomCharge: "Beban ke kamar", Company: "Tagihan perusahaan", Comp: "Komplimen",
	TaxID: "NPWP",
}

// FormatMoney writes an amount as the hotel writes it: a point between thousands, a comma before the decimals, the decimals of the currency.
func FormatMoney(d decimal.Decimal, decimals int) string {
	s := d.StringFixed(int32(decimals)) //nolint:gosec // G115: currency decimals are 0 to 3
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var sb strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			sb.WriteByte('.')
		}
		sb.WriteRune(c)
	}
	out := sb.String()
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// Qty writes a quantity without trailing zeros ("2", "0,5").
func Qty(d decimal.Decimal) string {
	return strings.Replace(d.String(), ".", ",", 1)
}

// Header is what the head of a receipt says. The caller has already filled what the outlet leaves empty from the property.
type Header struct {
	Hotel   string
	Address string
	TaxID   string
	Outlet  string
}

// ReceiptLine is a line of a receipt: the quantity, the name, the options and the note, and the amount net of service and tax (as the bill shows it).
type ReceiptLine struct {
	Qty     string
	Name    string
	Options string
	Note    string
	Amount  string
}

// Row is a label and an amount.
type Row struct {
	Label  string
	Amount string
}

// RoomSheet is the second sheet of a bill charged to a room: the guest signs it.
type RoomSheet struct {
	Room   string
	Guest  string
	Amount string
}

// ReceiptInput is a receipt, or a bill before it is paid (Prebill), with everything already written as the paper shows it.
type ReceiptInput struct {
	Header     Header
	Footer     string
	BillNumber string
	Place      string // "MEJA P1", "KAMAR 101", a customer name, "Bawa pulang"
	Guests     string
	Waiter     string
	Cashier    string
	At         string
	Lines      []ReceiptLine
	Summary    []Row // net, service, tax, rounding
	TotalLabel string
	Total      string
	Payments   []Row
	Change     string
	Balance    string
	Prebill    bool
}

func center(text string, bold bool) Block {
	return Block{T: BlockText, Text: text, Align: AlignCenter, Bold: bold}
}

func headBlocks(h Header, l Labels) []Block {
	var b []Block
	if h.Hotel != "" {
		b = append(b, center(h.Hotel, true))
	}
	if h.Address != "" {
		b = append(b, center(h.Address, false))
	}
	if h.TaxID != "" {
		b = append(b, center(l.TaxID+": "+h.TaxID, false))
	}
	if h.Outlet != "" {
		b = append(b, center(h.Outlet, true))
	}
	return b
}

// Receipt builds a receipt, or a pre-bill marked "BUKAN STRUK" when in.Prebill. The paper is cut at the end.
func Receipt(in ReceiptInput, l Labels) Document {
	kind := KindReceipt
	b := headBlocks(in.Header, l)
	b = append(b, Block{T: BlockRule})
	if in.Prebill {
		kind = KindPrebill
		b = append(b, center(l.NotReceipt, true))
	}
	b = append(b, Block{T: BlockPair, Left: l.Bill + " " + in.BillNumber, Right: in.At})
	if in.Place != "" {
		b = append(b, Block{T: BlockText, Text: in.Place, Bold: true})
	}
	if in.Guests != "" {
		b = append(b, Block{T: BlockText, Text: in.Guests})
	}
	if in.Cashier != "" {
		b = append(b, Block{T: BlockText, Text: l.Cashier + ": " + in.Cashier})
	}
	if in.Waiter != "" {
		b = append(b, Block{T: BlockText, Text: l.Waiter + ": " + in.Waiter})
	}
	b = append(b, Block{T: BlockRule})
	for _, ln := range in.Lines {
		b = append(b, Block{T: BlockPair, Left: ln.Qty + " x " + ln.Name, Right: ln.Amount})
		if ln.Options != "" {
			b = append(b, Block{T: BlockText, Text: ln.Options, Indent: 3})
		}
		if ln.Note != "" {
			b = append(b, Block{T: BlockText, Text: "* " + ln.Note, Indent: 3})
		}
	}
	b = append(b, Block{T: BlockRule})
	for _, r := range in.Summary {
		b = append(b, Block{T: BlockPair, Left: r.Label, Right: r.Amount})
	}
	b = append(b, Block{T: BlockPair, Left: in.TotalLabel, Right: in.Total, Bold: true})
	if len(in.Payments) > 0 {
		b = append(b, Block{T: BlockRule})
		for _, r := range in.Payments {
			b = append(b, Block{T: BlockPair, Left: r.Label, Right: r.Amount})
		}
	}
	if in.Change != "" {
		b = append(b, Block{T: BlockPair, Left: l.Change, Right: in.Change})
	}
	if in.Balance != "" {
		b = append(b, Block{T: BlockPair, Left: l.Balance, Right: in.Balance, Bold: true})
	}
	b = append(b, Block{T: BlockBlank})
	if in.Footer != "" {
		b = append(b, center(in.Footer, false))
	} else {
		b = append(b, center(l.Thanks, false))
	}
	return Document{Kind: kind, Blocks: b, Cut: true}
}

// RoomChargeSheet is the second sheet of a bill that was charged to a room: the room, the guest, the amount, a line to sign.
func RoomChargeSheet(in ReceiptInput, sheet RoomSheet, l Labels) Document {
	b := headBlocks(in.Header, l)
	b = append(b, Block{T: BlockRule}, center(l.RoomSheet, true))
	b = append(b, Block{T: BlockPair, Left: l.Bill + " " + in.BillNumber, Right: in.At})
	b = append(b, Block{T: BlockText, Text: l.Room + " " + sheet.Room, Bold: true})
	if sheet.Guest != "" {
		b = append(b, Block{T: BlockText, Text: l.Guest + ": " + sheet.Guest})
	}
	if in.Cashier != "" {
		b = append(b, Block{T: BlockText, Text: l.Cashier + ": " + in.Cashier})
	}
	b = append(b, Block{T: BlockRule})
	for _, ln := range in.Lines {
		b = append(b, Block{T: BlockPair, Left: ln.Qty + " x " + ln.Name, Right: ln.Amount})
	}
	b = append(b, Block{T: BlockRule}, Block{T: BlockPair, Left: l.RoomCharge, Right: sheet.Amount, Bold: true})
	b = append(b, Block{T: BlockBlank}, Block{T: BlockBlank}, Block{T: BlockBlank}, Block{T: BlockRule, Rule: "_"}, center(l.Signature, false))
	return Document{Kind: KindReceipt, Blocks: b, Cut: true}
}

// TicketLine is a line of a kitchen ticket.
type TicketLine struct {
	Qty     string
	Name    string
	Options string
	Note    string
	Void    bool
}

// TicketInput is a kitchen ticket.
type TicketInput struct {
	Outlet     string
	Station    string
	BillNumber string
	Round      int
	TicketNo   int64
	Place      string // "MEJA P1", "KAMAR 101", a customer name
	Waiter     string
	SentAt     string
	Addition   bool
	Cancel     bool
	Lines      []TicketLine
}

// Ticket builds a kitchen ticket: the outlet, the order id (bill, round, ticket), the table in double size, the waiter and the time, the lines,
// TAMBAHAN for a later round and BATAL for a cancellation in double size.
func Ticket(in TicketInput, l Labels) Document {
	var b []Block
	if in.Outlet != "" {
		b = append(b, center(strings.ToUpper(in.Outlet), true))
	}
	if in.Station != "" {
		b = append(b, center(in.Station, false))
	}
	switch {
	case in.Cancel:
		b = append(b, Block{T: BlockText, Text: l.Cancel, Align: AlignCenter, Bold: true, Big: true})
	case in.Addition:
		b = append(b, Block{T: BlockText, Text: l.Addition, Align: AlignCenter, Bold: true, Big: true})
	}
	b = append(b, center(fmt.Sprintf("%s | %s %d | %s-%04d", in.BillNumber, l.Round, in.Round, l.Ticket, in.TicketNo), false))
	if in.Place != "" {
		b = append(b, Block{T: BlockText, Text: strings.ToUpper(in.Place), Align: AlignCenter, Bold: true, Big: true})
	}
	var who []string
	if in.Waiter != "" {
		who = append(who, l.Waiter+": "+in.Waiter)
	}
	if in.SentAt != "" {
		who = append(who, l.Time+" "+in.SentAt)
	}
	if len(who) > 0 {
		b = append(b, center(strings.Join(who, " | "), false))
	}
	b = append(b, Block{T: BlockRule})
	for _, ln := range in.Lines {
		left := ln.Qty + " x " + ln.Name
		if in.Cancel || ln.Void {
			left = l.Cancel + " " + left
		}
		b = append(b, Block{T: BlockText, Text: left, Bold: true})
		if ln.Options != "" {
			b = append(b, Block{T: BlockText, Text: "- " + ln.Options, Indent: 3})
		}
		if ln.Note != "" {
			b = append(b, Block{T: BlockText, Text: "! " + ln.Note, Indent: 3, Bold: true})
		}
	}
	b = append(b, Block{T: BlockRule})
	return Document{Kind: KindTicket, Blocks: b, Cut: true}
}

// Drawer is a pulse of the cash drawer with nothing printed.
func Drawer() Document {
	return Document{Kind: KindDrawer, Pulse: true}
}

// TestPage is what `pos-agent test-print` prints: the names and a ruler of the width of the paper, to check the width, the characters and the cut.
func TestPage(printer, device, version string, at time.Time) Document {
	b := []Block{
		{T: BlockText, Text: "KamaraPMS", Align: AlignCenter, Bold: true, Big: true},
		center("TEST PRINT", true),
		{T: BlockRule},
		{T: BlockText, Text: "Printer: " + printer},
		{T: BlockText, Text: "Device: " + device},
		{T: BlockText, Text: "Agent: " + version},
		{T: BlockText, Text: at.UTC().Format("2006-01-02 15:04:05 UTC")},
		{T: BlockRule},
		{T: BlockText, Text: "1234567890123456789012345678901234567890123456789"},
		{T: BlockPair, Left: "Left", Right: "Right"},
		center("OK", true),
	}
	return Document{Kind: KindTicket, Blocks: b, Cut: true}
}
