package documents

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestLang(t *testing.T) {
	if l, err := ParseLang(""); err != nil || l != LangEN {
		t.Fatalf("empty is English: %v %v", l, err)
	}
	if l, err := ParseLang(" ID "); err != nil || l != LangID {
		t.Fatalf("id: %v %v", l, err)
	}
	if _, err := ParseLang("fr"); err == nil {
		t.Fatal("an unknown language is invalid")
	}
	if LangEN.T("Balance due") != "Balance due" || LangID.T("Balance due") != "Saldo terutang" {
		t.Fatal("labels")
	}
	for in, want := range map[string]string{
		"2 adult(s), 1 child(ren)":                       "2 dewasa, 1 anak",
		"IDR 800,000 per night":                          "IDR 800,000 per malam",
		"Note: late arrival":                             "Catatan: late arrival",
		"Paid 50,000 on 30 Sep 2026 (CASH), reference X": "Dibayar 50,000 pada 30 Sep 2026 (CASH), referensi X",
		"Page 2 of {nb}":                                 "Halaman 2 dari {nb}",
		"Something the guest wrote":                      "Something the guest wrote", // not a word of the program: untouched
	} {
		if got := LangID.T(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if LangID.Word("Total") != "Total" || LangID.Word("Cash") != "Tunai" || LangID.Word("Note: x") != "Note: x" {
		t.Fatal("a table cell is only translated when it is a word of the program")
	}
	if got := LangID.Money(decimal.RequireFromString("1234567.5"), 2); got != "1.234.567,50" {
		t.Fatalf("money: %s", got)
	}
	if got := LangEN.Money(decimal.RequireFromString("1234567.5"), 2); got != "1,234,567.50" {
		t.Fatalf("money: %s", got)
	}
	if LangID.Date(d("2026-08-05")) != "5 Agu 2026" || LangID.Date(d("2026-05-05")) != "5 Mei 2026" || LangEN.Date(d("2026-08-05")) != "5 Aug 2026" {
		t.Fatal("months")
	}
}
