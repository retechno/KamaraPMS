package documents_test

import (
	"strings"
	"testing"
	"time"

	"kamarapms/internal/documents"
	"kamarapms/internal/platform/civil"
)

func TestTheDailyFlashAsPDFInEnglishAndIndonesian(t *testing.T) {
	f := setup(t)
	date := civil.MustParseDate("2026-09-30")

	// the open day is an interim flash: no control balance and no signatures
	doc, err := f.Docs.DailyFlashPDF(f.admin, f.propID, date)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"DAILY FLASH REPORT", "INTERIM", "ROOM STATISTICS", "NET REVENUE", "PAYMENTS RECEIVED"} {
		if !strings.Contains(s, want) {
			t.Errorf("interim flash lacks %q", want)
		}
	}
	for _, not := range []string{"CONTROL BALANCE", "Approved by"} {
		if strings.Contains(s, not) {
			t.Errorf("interim flash has %q", not)
		}
	}

	// closed, it carries the control balance and the signature block
	f.Clock.Set(date.At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	_, err = f.Audit.Run(f.admin, f.propID, date)
	must(t, err)
	closed, err := f.Docs.DailyFlashPDF(f.admin, f.propID, date)
	must(t, err)
	c := pdfText(t, closed)
	if closed.Filename != "daily-flash-2026-09-30.pdf" {
		t.Fatalf("filename %s", closed.Filename)
	}
	for _, want := range []string{"CONTROL BALANCE", "Prepared by", "Approved by", "122,100"} {
		if !strings.Contains(c, want) {
			t.Errorf("closed flash lacks %q", want)
		}
	}
	// The payment methods are printed by name, never as their codes; the property has no budget for the month, so the table has no budget and no variance column.
	for _, want := range []string{"Cash"} {
		if !strings.Contains(c, want) {
			t.Errorf("closed flash lacks the method %q", want)
		}
	}
	for _, not := range []string{"CASH", "CARD", "BANK_TRANSFER", "Budget MTD", "Variance MTD"} {
		if strings.Contains(c, not) {
			t.Errorf("closed flash has %q", not)
		}
	}

	id, err := f.Docs.DailyFlashPDF(documents.WithLang(f.admin, documents.LangID), f.propID, date)
	must(t, err)
	i := pdfText(t, id)
	for _, want := range []string{"LAPORAN FLASH HARIAN", "STATISTIK KAMAR", "PENDAPATAN NETO", "KONTROL SALDO", "Disetujui oleh"} {
		if !strings.Contains(i, want) {
			t.Errorf("Indonesian flash lacks %q", want)
		}
	}
	for _, want := range []string{"Tunai"} {
		if !strings.Contains(i, want) {
			t.Errorf("Indonesian flash lacks the method %q", want)
		}
	}
	for _, not := range []string{"Arrivals tomorrow", "Cash", "Card", "Anggaran MTD"} {
		if strings.Contains(i, not) {
			t.Errorf("Indonesian flash has %q", not)
		}
	}
}
