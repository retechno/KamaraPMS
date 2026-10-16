package printing

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares the preview and the bytes of a document with the files of testdata: the preview is the text of the paper (to be read in a
// review), the hex is the exact bytes that go to the printer.
func golden(t *testing.T, name string, doc Document, p Profile, mark string) {
	t.Helper()
	bs, err := Render(doc, p, mark)
	if err != nil {
		t.Fatal(err)
	}
	got := Preview(doc, p, mark) + "\n--- bytes ---\n" + hexdump(bs)
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from its golden file.\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func hexdump(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 16 {
		end := min(i+16, len(b))
		fmt.Fprintf(&sb, "%04x  %s\n", i, hex.EncodeToString(b[i:end]))
	}
	return sb.String()
}

var (
	p80 = Profile{WidthMM: 80, Codepage: "CP437"}
	p58 = Profile{WidthMM: 58, Codepage: "CP437"}
)

func receipt() ReceiptInput {
	m := func(s string) string { return FormatMoney(decimal.RequireFromString(s), 0) }
	return ReceiptInput{
		Header:     Header{Hotel: "Hotel Teratai Bali", Address: "Jl. Pantai Kuta No. 8, Badung", TaxID: "01.234.567.8-901.000", Outlet: "Restoran Teratai"},
		Footer:     "Terima kasih. Wi-Fi: teratai / kolam2026",
		BillNumber: "RESTO-0012", Place: "MEJA P1", Guests: "2 tamu", Waiter: "Made", Cashier: "Ayu", At: "16/10/2026 19:42",
		Lines: []ReceiptLine{
			{Qty: "2", Name: "Nasi Goreng Kampung", Options: "Reguler, Sedang", Amount: m("170000")},
			{Qty: "1", Name: "Es Teh Manis", Amount: m("25000")}, //nolint:misspell // an Indonesian name of a drink
			{Qty: "1", Name: "Bebek Betutu with a very long name that wraps", Note: "tanpa sambal", Amount: m("165000")},
		},
		Summary:    []Row{{"Neto", m("360000")}, {"Layanan", m("36000")}, {"Pajak", m("39600")}},
		TotalLabel: "TOTAL", Total: m("435600"),
		Payments: []Row{{"Tunai", m("500000")}}, Change: m("64400"),
	}
}

func TestReceipt80And58(t *testing.T) {
	golden(t, "receipt.80", Receipt(receipt(), LabelsID), p80, "")
	golden(t, "receipt.58", Receipt(receipt(), LabelsID), p58, "")
}

func TestReprintDiffersOnlyByTheMark(t *testing.T) {
	doc := Receipt(receipt(), LabelsID)
	orig, _ := Render(doc, p80, "")
	cp, _ := Render(doc, p80, "COPY 2")
	if bytes.Equal(orig, cp) {
		t.Fatal("a reprint carries its mark")
	}
	// the same document and mark always give the same bytes, and without the mark line the bytes are the original's
	again, _ := Render(doc, p80, "COPY 2")
	if !bytes.Equal(cp, again) {
		t.Fatal("rendering is deterministic")
	}
	tail := cp[bytes.Index(cp, []byte("Hotel Teratai Bali")):]
	if !bytes.HasSuffix(orig, tail) {
		t.Fatal("under the mark a reprint is the original")
	}
	golden(t, "receipt.reprint.80", doc, p80, "COPY 2")
}

func TestPrebillSaysItIsNotAReceipt(t *testing.T) {
	in := receipt()
	in.Prebill, in.Payments, in.Change = true, nil, ""
	in.TotalLabel, in.Total = "YANG HARUS DIBAYAR", "435.600"
	doc := Receipt(in, LabelsID)
	if doc.Kind != KindPrebill {
		t.Fatalf("kind %s", doc.Kind)
	}
	golden(t, "prebill.80", doc, p80, "")
}

func TestRoomChargeSheet(t *testing.T) {
	in := receipt()
	golden(t, "roomsheet.80", RoomChargeSheet(in, RoomSheet{Room: "101", Guest: "Santoso Wijaya", Amount: "435.600"}, LabelsID), p80, "")
	golden(t, "roomsheet.58", RoomChargeSheet(in, RoomSheet{Room: "101", Guest: "Santoso Wijaya", Amount: "435.600"}, LabelsID), p58, "")
}

func TestKitchenTickets(t *testing.T) {
	base := TicketInput{Outlet: "Restoran Teratai", Station: "Dapur utama", BillNumber: "RESTO-0012", Round: 2, TicketNo: 345, Place: "Meja P1", Waiter: "Made", SentAt: "19:42",
		Lines: []TicketLine{{Qty: "2", Name: "Nasi Goreng Kampung", Options: "Reguler, Sedang", Note: "Tanpa bawang goreng"}, {Qty: "1", Name: "Sate Ayam Madura"}}}
	golden(t, "ticket.new.80", Ticket(base, LabelsID), p80, "")
	add := base
	add.Addition = true
	golden(t, "ticket.addition.80", Ticket(add, LabelsID), p80, "")
	golden(t, "ticket.addition.58", Ticket(add, LabelsID), p58, "")
	cancel := base
	cancel.Cancel = true
	cancel.Lines = []TicketLine{{Qty: "1", Name: "Bebek Betutu", Void: true}}
	golden(t, "ticket.cancel.80", Ticket(cancel, LabelsID), p80, "")
}

func TestDrawerPulse(t *testing.T) {
	bs, err := Render(Drawer(), Profile{WidthMM: 80, Codepage: "CP437", DrawerPin: 0}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(bs, []byte{0x1B, 0x70, 0x00, 0x19, 0xFA}) {
		t.Fatalf("the pulse command is missing: %x", bs)
	}
	bs, _ = Render(Drawer(), Profile{WidthMM: 80, Codepage: "CP437", DrawerPin: 1}, "")
	if !bytes.Contains(bs, []byte{0x1B, 0x70, 0x01, 0x19, 0xFA}) {
		t.Fatalf("pin 1: %x", bs)
	}
}

func TestNonASCIIIsTransliteratedAndNothingExceedsTheWidth(t *testing.T) {
	doc := Document{Blocks: []Block{{T: BlockText, Text: "Café “Müller” – 2 × crème brûlée ★ 日本"}, {T: BlockPair, Left: "Pâté de campagne with a long long name", Right: "1.234.567"}}}
	bs, err := Render(doc, p58, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range bs {
		if c >= 0x80 {
			t.Fatalf("byte %#x is not ASCII", c)
		}
	}
	for _, line := range strings.Split(Preview(doc, p58, ""), "\n") {
		if len(line) > 32 {
			t.Fatalf("%q is wider than 32", line)
		}
	}
	if !strings.Contains(strings.ReplaceAll(Preview(doc, p58, ""), "\n", " "), `Cafe "Muller" - 2 x creme brulee ? ??`) {
		t.Fatalf("transliteration: %s", Preview(doc, p58, ""))
	}
}

func TestMoneyFormat(t *testing.T) {
	cases := map[string]string{"0": "0", "999": "999", "1000": "1.000", "1234567": "1.234.567", "-85000": "-85.000", "12.5": "13"}
	for in, want := range cases {
		if got := FormatMoney(decimal.RequireFromString(in), 0); got != want {
			t.Errorf("%s: %s want %s", in, got, want)
		}
	}
	if got := FormatMoney(decimal.RequireFromString("1234.5"), 2); got != "1.234,50" {
		t.Errorf("decimals: %s", got)
	}
}

func TestPayloadLimit(t *testing.T) {
	var blocks []Block
	for range 3000 {
		blocks = append(blocks, Block{T: BlockText, Text: strings.Repeat("x", 40)})
	}
	if _, err := Render(Document{Blocks: blocks}, p80, ""); err == nil {
		t.Fatal("a document over 64 KB is refused")
	}
}
