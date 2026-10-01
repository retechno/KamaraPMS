package documents

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"kamarapms/internal/platform/civil"
)

func init() { compressPDF = false } // the tests read the text out of the page streams

var hotel = Hotel{Name: "Hotel Bali", Address: "Jl. Sunset 1", City: "Denpasar", Country: "ID", Phone: "+62 361 1", Email: "info@bali.test", TaxID: "01.234.567.8-901.000", Footer: "Thank you for staying with us"}

func d(s string) civil.Date { return civil.MustParseDate(s) }

func invoice(final bool, lines int) InvoiceData {
	in := InvoiceData{
		Hotel: hotel, Printed: "Printed 30 Sep 2026 20:00", Number: "FOL000001", Final: final, Guest: Party{Name: "Siti Nurhaliza", Address: "Jl. Mawar 2", City: "Jakarta"},
		Booking: "RES000009", Stay: "STY000001", Room: "101 > 102", Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Currency: "IDR",
		Summary: []Amount{{"Charges (net)", "2000000"}, {"VAT 11%", "220000"}, {"Total charges", "2220000"}, {"Payments received", "500000"}, {"Balance due", "1720000"}},
	}
	for i := 0; i < lines; i++ {
		in.Lines = append(in.Lines, InvoiceLine{Date: d("2026-09-30"), Description: "Room 101 - 30 Sep 2026", Debit: "1110000", DebitSet: true})
	}
	in.Lines = append(in.Lines, InvoiceLine{Date: d("2026-09-30"), Description: "Cash payment PAY000001", Credit: "500000", CreditSet: true})
	return in
}

func text(t *testing.T, pdf []byte) string {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Fatalf("not a PDF: %q", pdf[:min(20, len(pdf))])
	}
	return string(pdf)
}

func TestInvoiceAndBill(t *testing.T) {
	final, err := RenderInvoice(invoice(true, 2))
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, final)
	for _, want := range []string{"INVOICE", "FOL000001", "Hotel Bali", "Tax ID: 01.234.567.8-901.000", "Siti Nurhaliza", "RES000009", "101 > 102", "30 Sep 2026 to 2 Oct 2026", "Balance due", "1720000", "Thank you for staying with us", "Printed 30 Sep 2026 20:00"} {
		if !strings.Contains(s, want) {
			t.Errorf("invoice lacks %q", want)
		}
	}
	if strings.Contains(s, "not final") {
		t.Error("a final invoice does not say it is not final")
	}
	bill, err := RenderInvoice(invoice(false, 2))
	if err != nil {
		t.Fatal(err)
	}
	if b := text(t, bill); !strings.Contains(b, "GUEST BILL") || !strings.Contains(b, "not final") || strings.Contains(b, "(INVOICE)") {
		t.Error("an open folio prints as a bill that says it can still change")
	}
}

func TestLongInvoicePaginatesAndNumbersPages(t *testing.T) {
	pdf, err := RenderInvoice(invoice(true, 120))
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, pdf)
	pages := strings.Count(s, "/Type /Page\n") + strings.Count(s, "/Type /Page ")
	if pages < 3 {
		t.Fatalf("120 lines should need several pages, got %d", pages)
	}
	if !strings.Contains(s, "Page 1 of "+itoa(pages)) || !strings.Contains(s, "Page "+itoa(pages)+" of "+itoa(pages)) {
		t.Errorf("page numbers: %d pages", pages)
	}
	if strings.Count(s, "Description") < pages {
		t.Error("the table header repeats on every page")
	}
	if !strings.Contains(s, "Balance due") {
		t.Error("the totals follow the last line")
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	a, _ := RenderInvoice(invoice(true, 5))
	b, _ := RenderInvoice(invoice(true, 5))
	if !bytes.Equal(a, b) {
		t.Fatal("the same data must give the same bytes")
	}
}

func TestNamesOutsideLatin1AreNotFatal(t *testing.T) {
	in := invoice(true, 1)
	in.Guest.Name = "田中 太郎 Müller Zoë"
	pdf, err := RenderInvoice(in)
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, pdf)
	if !strings.Contains(s, "Zo\xeb") || !strings.Contains(s, "M\xfcller") || strings.Contains(s, "太郎") {
		t.Error("characters the PDF core fonts cannot show are replaced, the rest is kept")
	}
}

func TestRegistrationCard(t *testing.T) {
	pdf, err := RenderRegistrationCard(CardData{
		Hotel: hotel, Printed: "p", Number: "STY000001", Booking: "RES000009", Guest: Party{Name: "Siti", Nationality: "ID", IDType: "KTP", IDNumber: "3171"},
		Companions: []string{"Budi Santoso"}, Room: "101", RoomType: "DLX", Arrival: d("2026-09-30"), CheckInTime: "14:00", Departure: d("2026-10-02"), CheckOutTime: "12:00",
		Nights: 2, Adults: 2, RatePlan: "BAR", NightlyRate: "1000000", Currency: "IDR", Terms: registrationTerms,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, pdf)
	for _, want := range []string{"REGISTRATION CARD", "STY000001", "KTP 3171", "Budi Santoso", "IDR 1000000 per night", "check-in from 14:00", "Guest signature", "Received by", "settle my account"} {
		if !strings.Contains(s, want) {
			t.Errorf("card lacks %q", want)
		}
	}
}

func TestReceiptAndVoidStamp(t *testing.T) {
	r := ReceiptData{Hotel: hotel, Printed: "p", Number: "PAY000001", Guest: Party{Name: "Siti"}, Folio: "FOL000001", Booking: "RES000009", Date: d("2026-09-30"), At: "20:15",
		Method: "Cash", Currency: "IDR", Amount: "500000"}
	pdf, err := RenderReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, pdf)
	if !strings.Contains(s, "PAYMENT RECEIPT") || !strings.Contains(s, "Amount received") || !strings.Contains(s, "IDR 500000") || strings.Contains(s, "VOID") {
		t.Error("a valid payment receipt")
	}
	r.Voided, r.VoidNote = true, "Cancelled on 30 Sep 2026 21:00: typo"
	v, _ := RenderReceipt(r)
	if vs := text(t, v); !strings.Contains(vs, "VOID - this payment was cancelled") || !strings.Contains(vs, "typo") {
		t.Error("a voided receipt is stamped and says why")
	}
	r.Voided, r.Refund, r.RefundOf = false, true, "PAY000001"
	rf, _ := RenderReceipt(r)
	if rs := text(t, rf); !strings.Contains(rs, "REFUND RECEIPT") || !strings.Contains(rs, "Amount refunded") || !strings.Contains(rs, "Refunded to") {
		t.Error("a refund is a refund receipt")
	}
}

func TestConfirmation(t *testing.T) {
	pdf, err := RenderConfirmation(ConfirmationData{
		Hotel: hotel, Printed: "p", Number: "RES000009", Status: "Confirmed", Guest: Party{Name: "Siti"}, Booked: d("2026-09-29"), Currency: "IDR", Total: "2220000",
		CheckInTime: "14:00", CheckOutTime: "12:00", Request: "Quiet room",
		Rooms: []BookedRoom{{RoomType: "DLX", RatePlan: "BAR", Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Nights: 2, Guests: "2 adult(s)", Estimate: "2220000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, pdf)
	for _, want := range []string{"RESERVATION CONFIRMATION", "RES000009", "Quiet room", "Estimated total", "2220000", "2 Oct 2026", "from 14:00 / by 12:00"} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q", want)
		}
	}
}

// TestDumpSamples writes sample documents for a visual check: PMS_DOC_DUMP=/some/dir go test ./internal/documents -run Dump
func TestDumpSamples(t *testing.T) {
	dir := os.Getenv("PMS_DOC_DUMP")
	if dir == "" {
		t.Skip("PMS_DOC_DUMP is not set")
	}
	compressPDF = true
	defer func() { compressPDF = false }()
	write := func(name string, b []byte, err error) {
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/"+name, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := RenderInvoice(invoice(true, 40))
	write("invoice.pdf", b, err)
	b, err = RenderRegistrationCard(CardData{Hotel: hotel, Printed: "Printed 30 Sep 2026", Number: "STY000001", Booking: "RES000009", Guest: Party{Name: "Siti Nurhaliza", Nationality: "ID", IDType: "KTP", IDNumber: "3171", Address: "Jl. Mawar 2", City: "Jakarta"},
		Companions: []string{"Budi Santoso"}, Room: "101", RoomType: "DLX", Arrival: d("2026-09-30"), CheckInTime: "14:00", Departure: d("2026-10-02"), CheckOutTime: "12:00", Nights: 2, Adults: 2, RatePlan: "BAR", NightlyRate: "1000000", Currency: "IDR", Terms: registrationTerms})
	write("card.pdf", b, err)
	b, err = RenderReceipt(ReceiptData{Hotel: hotel, Printed: "Printed", Number: "PAY000001", Guest: Party{Name: "Siti"}, Folio: "FOL000001", Booking: "RES000009", Date: d("2026-09-30"), At: "20:15", Method: "Cash", Currency: "IDR", Amount: "500000", Voided: true, VoidNote: "Cancelled: typo"})
	write("receipt.pdf", b, err)
	b, err = RenderConfirmation(ConfirmationData{Hotel: hotel, Printed: "Printed", Number: "RES000009", Status: "Confirmed", Guest: Party{Name: "Siti"}, Booked: d("2026-09-29"), Currency: "IDR", Total: "2220000", CheckInTime: "14:00", CheckOutTime: "12:00", Request: "Quiet room",
		Rooms: []BookedRoom{{RoomType: "DLX", RatePlan: "BAR", Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Nights: 2, Guests: "2 adult(s)", Estimate: "2220000"}}})
	write("confirmation.pdf", b, err)
}
