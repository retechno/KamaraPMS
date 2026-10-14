package documents

import "testing"

func TestFolioDescriptionFollowsTheLanguageOfTheDocument(t *testing.T) {
	cases := []struct {
		lang Lang
		in   string
		want string
	}{
		{LangEN, "Transfer from FOL000010", "Transfer from FOL000010"},
		{LangEN, "Room 209 - 12 Oct 2026", "Room 209 - 12 Oct 2026"},
		{LangID, "Transfer from FOL000010", "Transfer dari FOL000010"},
		{LangID, "Transfer to FOL000019", "Transfer ke FOL000019"},
		{LangID, "Room 209 - 12 Oct 2026", "Kamar 209 - 12 Okt 2026"},
		{LangID, "Room 5 - 3 May 2026", "Kamar 5 - 3 Mei 2026"},
		{LangID, "Room 5 - 3 Aug 2026", "Kamar 5 - 3 Agu 2026"},
		{LangID, "Payment PAY000001 (BANK_TRANSFER)", "Pembayaran PAY000001 (Transfer bank)"},
		{LangID, "Payment PAY000002 (CASH)", "Pembayaran PAY000002 (Tunai)"},
		{LangID, "Refund PAY000003 (CITY_LEDGER)", "Pengembalian PAY000003 (Piutang perusahaan)"},
		{LangID, "Reversal: Room 209 - 12 Oct 2026", "Pembalikan: Kamar 209 - 12 Okt 2026"},
		{LangID, "Adjustment: wrong rate", "Penyesuaian: wrong rate"},
		{LangID, "Minibar 1", "Minibar 1"},
		{LangID, "Bank transfer by the guest", "Bank transfer by the guest"},
	}
	for _, c := range cases {
		if got := c.lang.FolioDescription(c.in); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.lang, c.in, got, c.want)
		}
	}
}
