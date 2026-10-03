package documents_test

import (
	"context"
	"strings"
	"testing"

	"kamarapms/internal/documents"
)

// The same documents in Indonesian: the words of the program, the months and the separators of the numbers follow
// the language; the names and remarks people typed stay as they are.
func TestDocumentsInIndonesian(t *testing.T) {
	f := setup(t)
	ctx := documents.WithLang(f.admin, documents.LangID)
	bill, err := f.Docs.Invoice(ctx, f.propID, f.stay.Folio.ID)
	must(t, err)
	s := pdfText(t, bill)
	for _, want := range []string{"TAGIHAN TAMU", "Siti Nurhaliza", "Biaya layanan Service 10%", "Total biaya", "122.100", "Pembayaran diterima", "50.000", "72.100", "Saldo terutang", "Halaman 1 dari", "Dicetak", "Tanggal menginap"} {
		if !strings.Contains(s, want) {
			t.Errorf("bill lacks %q", want)
		}
	}
	for _, unwanted := range []string{"GUEST BILL", "Payments received", "122,100"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("bill still says %q", unwanted)
		}
	}
	var payID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM payments WHERE property_id = $1`, f.propID).Scan(&payID))
	rc, err := f.Docs.Receipt(ctx, f.propID, payID)
	must(t, err)
	if r := pdfText(t, rc); !strings.Contains(r, "TANDA TERIMA PEMBAYARAN") || !strings.Contains(r, "Tunai") || !strings.Contains(r, "IDR 50.000") {
		t.Error("receipt in Indonesian")
	}
	card, err := f.Docs.RegistrationCard(ctx, f.propID, f.stay.Stay.ID)
	must(t, err)
	if c := pdfText(t, card); !strings.Contains(c, "KARTU REGISTRASI") || !strings.Contains(c, "IDR 1.000.000 per malam") || !strings.Contains(c, "Quiet room") || !strings.Contains(c, "check-in mulai 14:00") {
		t.Error("registration card in Indonesian (what the guest asked for stays as written)")
	}
	conf, err := f.Docs.Confirmation(ctx, f.propID, f.reservation.ID)
	must(t, err)
	c := pdfText(t, conf)
	for _, want := range []string{"KONFIRMASI RESERVASI", "KAMAR ANDA", "Sedang menginap"} {
		if !strings.Contains(c, want) {
			t.Errorf("confirmation lacks %q", want)
		}
	}
	// without a language the documents stay English
	en, err := f.Docs.Invoice(f.admin, f.propID, f.stay.Folio.ID)
	must(t, err)
	if e := pdfText(t, en); !strings.Contains(e, "GUEST BILL") || !strings.Contains(e, "122,100") {
		t.Error("English by default")
	}
}
