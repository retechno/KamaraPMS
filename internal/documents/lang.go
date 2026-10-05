package documents

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Lang is the language of a document: English (the default) or Indonesian. The words the program writes (titles,
// labels, notes, month names, the separators of numbers) follow it; what people typed (names, remarks, account names,
// the descriptions posted to a folio) is printed as it is.
type Lang string

const (
	LangEN Lang = "en"
	LangID Lang = "id"
)

// ParseLang reads the `lang` of a request: empty means English, anything but en and id is invalid.
func ParseLang(v string) (Lang, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "en":
		return LangEN, nil
	case "id":
		return LangID, nil
	}
	return LangEN, apperr.Invalid("the language is invalid", apperr.FieldError{Field: "lang", Code: "INVALID_VALUE", Message: "en or id"})
}

type langKey struct{}

// WithLang carries the language of the request to the document service.
func WithLang(ctx context.Context, l Lang) context.Context {
	return context.WithValue(ctx, langKey{}, l)
}

// langCtx is the context of a request with the language it asked for (`lang=id`; anything else is English).
func langCtx(r *http.Request) context.Context {
	l, _ := ParseLang(r.URL.Query().Get("lang"))
	return WithLang(r.Context(), l)
}

// LangFrom is the language of the request, English when none was given.
func LangFrom(ctx context.Context) Lang {
	if l, ok := ctx.Value(langKey{}).(Lang); ok && l != "" {
		return l
	}
	return LangEN
}

var monthID = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// Date prints a business date as "30 Sep 2026" ("30 Agu 2026" in Indonesian).
func (l Lang) Date(d civil.Date) string {
	if d.IsZero() {
		return "-"
	}
	months := monthShort
	if l == LangID {
		months = monthID
	}
	return itoa(d.Day()) + " " + months[d.Month()-1] + " " + itoa(d.Year())
}

// Time prints an instant in the property's time zone.
func (l Lang) Time(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	if l != LangID {
		return t.Format("02 Jan 2006 15:04")
	}
	return t.Format("02 ") + monthID[t.Month()-1] + t.Format(" 2006 15:04")
}

// Money prints an amount with the property's decimals and thousands separators: 1110000 as 1,110,000 (1.110.000 in
// Indonesian, which also writes the decimals after a comma).
func (l Lang) Money(d decimal.Decimal, decimals int32) string {
	s := money(d, decimals)
	if l != LangID {
		return s
	}
	var b strings.Builder
	for _, c := range s {
		switch c {
		case ',':
			b.WriteByte('.')
		case '.':
			b.WriteByte(',')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

type rule struct {
	re   *regexp.Regexp
	repl string
}

// T translates a text of the program. A text it does not know (what people typed) comes back as it was.
func (l Lang) T(s string) string {
	if l != LangID || s == "" {
		return s
	}
	if t, ok := idExact[s]; ok {
		return t
	}
	for _, r := range idRules {
		if r.re.MatchString(s) {
			s = r.re.ReplaceAllString(s, r.repl)
			break
		}
	}
	for _, p := range idParts {
		if strings.Contains(s, p[0]) {
			s = strings.ReplaceAll(s, p[0], p[1])
		}
	}
	return s
}

// Word translates a table cell: only a word of the documents itself ("Total", "Opening balance"), never a pattern, so
// that what people typed is left alone.
func (l Lang) Word(s string) string {
	if l != LangID {
		return s
	}
	if t, ok := idExact[s]; ok {
		return t
	}
	return s
}

func rx(pattern, repl string) rule { return rule{regexp.MustCompile(pattern), repl} }

// idRules are texts made of words and values: the first that matches is used, with the values put back ($1, $2).
var idRules = []rule{
	rx(`^Charges and payments \((.*)\)$`, "Biaya dan pembayaran ($1)"),
	rx(`^(\d+) adult\(s\), (\d+) child\(ren\)$`, "$1 dewasa, $2 anak"),
	rx(`^(.*) per night$`, "$1 per malam"),
	rx(`^(.*)  \(check-in from (.*)\)$`, "$1  (check-in mulai $2)"),
	rx(`^(.*)  \(check-out by (.*)\)$`, "$1  (check-out sebelum $2)"),
	rx(`^from (.*) / by (.*)$`, "mulai $1 / sebelum $2"),
	rx(`^Note: (.*)$`, "Catatan: $1"),
	rx(`^Estimate \((.*)\)$`, "Perkiraan ($1)"),
	rx(`^Aging as of (.*) \(days since the folio was transferred\)$`, "Umur piutang per $1 (hari sejak folio dipindahkan)"),
	rx(`^Page (\d+) of (.*)$`, "Halaman $1 dari $2"),
	rx(`^Cancelled on (.*)$`, "Dibatalkan pada $1"),
	rx(`^From (.*)$`, "Mulai $1"),
	rx(`^Up to (.*)$`, "Sampai $1"),
	rx(`^(\d{1,2} \S+ \d{4}) to (\d{1,2} \S+ \d{4})$`, "$1 sampai $2"),
	rx(`^(\d{1,2} \S+ \d{4}) - (\d{1,2} \S+ \d{4})$`, "$1 - $2"),
	rx(`^As of (.*)$`, "Per $1"),
	rx(`^Service charge (.*)$`, "Biaya layanan $1"),
	rx(`^VOIDED: (.*)$`, "DIBATALKAN: $1"),
	rx(`^Paid (.*) on (.*) \((.*)\)(.*)$`, "Dibayar $1 pada $2 ($3)$4"),
	rx(`^Outstanding: (.*)$`, "Sisa: $1"),
	rx(`^WARNING: the books are out of balance by (.*)$`, "PERINGATAN: pembukuan tidak seimbang sebesar $1"),
	rx(`^Printed (.*)  ·  business date (.*)$`, "Dicetak $1  ·  tanggal bisnis $2"),
	rx(`^Tax ID: (.*)$`, "ID pajak: $1"),
}

// idParts are pieces of a text that are replaced wherever they are (after the rules).
var idParts = [][2]string{
	{", reference ", ", referensi "},
	{", penalty ", ", denda "},
	{" (voided)", " (dibatalkan)"},
}

// idExact are the labels, titles and sentences of the documents.
var idExact = map[string]string{ //nolint:gosec // G101: the labels of a document, not credentials
	// invoice and guest bill
	"INVOICE": "INVOICE", "GUEST BILL (not final)": "TAGIHAN TAMU (belum final)", "Guest": "Tamu", "Reservation": "Reservasi", "Address": "Alamat",
	"Stay": "Menginap", "Room": "Kamar", "Stay dates": "Tanggal menginap", "Date": "Tanggal", "Description": "Keterangan", "Debit": "Debit", "Credit": "Kredit",
	"Charges (net)": "Biaya (neto)", "Total charges": "Total biaya", "Payments received": "Pembayaran diterima", "Balance due": "Saldo terutang",
	"Balance (credit)": "Saldo (kredit)",
	"This bill is not final: charges can still be posted until check-out. A final invoice is issued when the folio is closed.": "Tagihan ini belum final: biaya masih bisa diposting sampai check-out. Invoice final diterbitkan saat folio ditutup.",
	// registration card
	"Registration card": "Kartu registrasi", "REGISTRATION CARD": "KARTU REGISTRASI", "Arrival": "Kedatangan", "Departure": "Keberangkatan", "Nights": "Malam",
	"Guests": "Tamu", "Rate plan": "Rate plan", "Rate": "Tarif", "Name": "Nama", "Nationality": "Kewarganegaraan", "Date of birth": "Tanggal lahir",
	"ID document": "Dokumen identitas", "Phone": "Telepon", "E-mail": "Email", "Special requests": "Permintaan khusus", "Accompanying guests": "Tamu pendamping",
	"Terms": "Ketentuan", "Guest signature": "Tanda tangan tamu", "Received by (front desk)": "Diterima oleh (resepsionis)",
	registrationTerms: "Saya menyatakan bahwa data di atas benar dan bahwa saya akan melunasi tagihan saya sepenuhnya saat keberangkatan. " +
		"Saya bertanggung jawab atas kamar dan isinya selama menginap, dan memahami bahwa hotel tidak bertanggung jawab atas barang berharga yang ditinggal di kamar. " +
		"Hotel dapat menagih kerugian atau kerusakan.",
	// receipt
	"PAYMENT RECEIPT": "TANDA TERIMA PEMBAYARAN", "REFUND RECEIPT": "TANDA TERIMA PENGEMBALIAN DANA", "Amount received": "Jumlah diterima", "Amount refunded": "Jumlah dikembalikan",
	"VOID - this payment was cancelled": "BATAL - pembayaran ini dibatalkan", "Received from": "Diterima dari", "Refunded to": "Dikembalikan kepada", "Folio": "Folio",
	"Method": "Metode", "Reference": "Referensi", "Refund of payment": "Pengembalian atas pembayaran", "Cashier": "Kasir",
	"Bank transfer": "Transfer bank", "Card": "Kartu", "Cash": "Tunai", "City ledger": "City ledger", "Other": "Lainnya",
	// confirmation
	"Reservation confirmation": "Konfirmasi reservasi", "RESERVATION CONFIRMATION": "KONFIRMASI RESERVASI", "Status": "Status", "Booked on": "Dipesan pada",
	"Check-in / check-out": "Check-in / check-out", "Your rooms": "Kamar Anda", "Room type": "Tipe kamar", "Estimated total": "Perkiraan total", "Your request": "Permintaan Anda",
	"Please quote the confirmation number when you contact us. The estimate is calculated from the rates booked; the final amount is the invoice issued at check-out.": "Mohon sebutkan nomor konfirmasi saat menghubungi kami. Perkiraan dihitung dari tarif yang dipesan; jumlah akhir adalah invoice yang diterbitkan saat check-out.",
	"Draft": "Draf", "Confirmed": "Terkonfirmasi", "Cancelled": "Dibatalkan", "Checked in": "Check-in", "Checked out": "Sudah check-out", "In house": "Sedang menginap",
	"Completed": "Selesai", "No show": "Tidak datang",
	// company statement and invoice
	"STATEMENT OF ACCOUNT": "LAPORAN REKENING", "Company": "Perusahaan", "Period": "Periode", "Payment terms": "Termin pembayaran", "Tax ID": "ID pajak",
	"Currency": "Mata uang", "Number": "Nomor", "Balance": "Saldo", "Opening balance": "Saldo awal", "Total debit": "Total debit", "Total credit": "Total kredit",
	"All movements": "Semua mutasi", "Bill to": "Ditagihkan kepada", "Invoice date": "Tanggal invoice", "Due date": "Jatuh tempo", "Check-out": "Check-out",
	"Amount": "Jumlah", "Total": "Total", "Paid": "Dibayar", "VOID - this invoice was cancelled": "BATAL - invoice ini dibatalkan",
	// accounting
	"Account": "Akun", "Open Dr": "Awal Db", "Open Cr": "Awal Kr", "Close Dr": "Akhir Db", "Close Cr": "Akhir Kr", "TRIAL BALANCE": "NERACA SALDO",
	"INCOME STATEMENT": "LAPORAN LABA RUGI", "USALI layout": "Format USALI", "BALANCE SHEET": "NERACA", "GENERAL LEDGER": "BUKU BESAR", "Journal": "Jurnal",
	"Detail": "Rincian", "Closing balance": "Saldo akhir", "Only the first entries are listed: narrow the range.": "Hanya entri pertama yang ditampilkan: persempit rentangnya.",
	"Equity includes the earnings of all periods to date: there is no year-end closing entry.": "Ekuitas mencakup laba semua periode sampai saat ini: tidak ada jurnal penutup akhir tahun.",
	"CASH FLOW STATEMENT": "LAPORAN ARUS KAS", "Indirect method": "Metode tidak langsung", "Direct method": "Metode langsung",
	"WARNING: the statement differs from the change of the cash accounts by ": "PERINGATAN: laporan berbeda dari perubahan akun kas sebesar ",
	"DEPARTMENT REPORT": "LAPORAN DEPARTEMEN", "Department": "Departemen", "Revenue": "Pendapatan", "Expense": "Biaya", "Profit": "Laba", "Unassigned": "Tanpa departemen",
	// budget
	"BUDGET AGAINST ACTUAL": "ANGGARAN TERHADAP REALISASI", "Fiscal year": "Tahun fiskal", "Budget": "Anggaran", "Actual": "Realisasi", "Variance": "Selisih",
	"YTD actual": "Realisasi s.d. kini", "YTD budget": "Anggaran s.d. kini", "YTD variance": "Selisih s.d. kini",
	"The variance is the actual less the budget. The year to date runs from the first day of the fiscal year to the end of the period.": "Selisih adalah realisasi dikurangi anggaran. Sampai saat ini dihitung dari hari pertama tahun fiskal sampai akhir periode.",
	// tax
	"TAX RETURN": "LAPORAN PAJAK", "TAX WORKSHEET": "LEMBAR KERJA PAJAK", "Tax": "Pajak", "Authority": "Otoritas", "Registration number": "Nomor registrasi",
	"Due": "Jatuh tempo", "Worksheet (not filed)": "Lembar kerja (belum dilaporkan)", "Filed on": "Dilaporkan pada", "Filing reference": "Referensi pelaporan",
	"Charge code": "Kode biaya", "Charged as": "Dibebankan sebagai", "Items": "Item", "Taxable base": "Dasar pengenaan",
	// small words used in rows
	"voided": "dibatalkan", "Printed ": "Dicetak ", "  ·  business date ": "  ·  tanggal bisnis ",
}
