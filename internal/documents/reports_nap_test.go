package documents_test

import (
	"strings"
	"testing"
	"time"

	"kamarapms/internal/documents"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
)

// The five reports of the night audit pack as PDFs, in both languages. The headings are drawn in capitals; the figures come from the report service.
func TestTheNightAuditReportsAsPDFInEnglishAndIndonesian(t *testing.T) {
	f := setup(t)
	date := civil.MustParseDate("2026-09-30")
	f.Clock.Set(date.At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	_, err := f.Audit.Run(f.admin, f.propID, date)
	must(t, err)
	id := documents.WithLang(f.admin, documents.LangID)

	cases := []struct {
		name, file string
		en, idn    []string
		render     func(lang bool) (documents.Document, error)
	}{
		{"cancellations", "cancellations-2026-09-30-2026-09-30.pdf", []string{"CANCELLATIONS AND NO-SHOWS", "Fees posted"}, []string{"PEMBATALAN DAN NO-SHOW", "Periode"}, func(l bool) (documents.Document, error) {
			ctx := f.admin
			if l {
				ctx = id
			}
			return f.Docs.CancellationsPDF(ctx, f.propID, reports.CancellationParams{From: date, To: date, Kind: reports.CancelKindAll})
		}},
		{"guest ledger", "guest-ledger-2026-09-30.pdf", []string{"GUEST LEDGER", "AGING OF THE DEBTS", "CHECKS"}, []string{"UMUR PIUTANG", "PEMERIKSAAN"}, func(l bool) (documents.Document, error) {
			ctx := f.admin
			if l {
				ctx = id
			}
			return f.Docs.GuestLedgerPDF(ctx, f.propID, &date)
		}},
		{"deposit ledger", "deposit-ledger-2026-09-30.pdf", []string{"DEPOSIT LEDGER", "CHECKS"}, []string{"BUKU DEPOSIT", "PEMERIKSAAN"}, func(l bool) (documents.Document, error) {
			ctx := f.admin
			if l {
				ctx = id
			}
			return f.Docs.DepositLedgerPDF(ctx, f.propID, &date)
		}},
		{"corrections", "corrections-2026-09-30-2026-09-30.pdf", []string{"VOIDS, CORRECTIONS AND DISCOUNTS", "Revenue taken back"}, []string{"VOID, KOREKSI DAN DISKON"}, func(l bool) (documents.Document, error) {
			ctx := f.admin
			if l {
				ctx = id
			}
			return f.Docs.CorrectionsPDF(ctx, f.propID, reports.CorrectionParams{From: date, To: date, Kind: reports.CorrAll})
		}},
		{"z reports", "z-reports-2026-09-30.pdf", []string{"Z REPORT", "BY DRAWER", "TAKINGS BY METHOD", "Cash"}, []string{"LAPORAN Z", "PER LACI KAS", "PENERIMAAN PER METODE", "Tunai"}, func(l bool) (documents.Document, error) {
			ctx := f.admin
			if l {
				ctx = id
			}
			return f.Docs.ZReportsPDF(ctx, f.propID, date)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			en, err := c.render(false)
			must(t, err)
			if en.Filename != c.file {
				t.Fatalf("filename %s", en.Filename)
			}
			s := pdfText(t, en)
			for _, want := range c.en {
				if !strings.Contains(s, strings.ToUpper(want)) && !strings.Contains(s, want) {
					t.Errorf("English %s lacks %q", c.name, want)
				}
			}
			again, err := c.render(false)
			must(t, err)
			if string(again.PDF) != string(en.PDF) {
				t.Errorf("%s is not deterministic", c.name)
			}
			in, err := c.render(true)
			must(t, err)
			i := pdfText(t, in)
			for _, want := range c.idn {
				if !strings.Contains(i, want) {
					t.Errorf("Indonesian %s lacks %q", c.name, want)
				}
			}
		})
	}
}
