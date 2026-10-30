package documents

// The Indonesian of the cancellations, ledgers, corrections and Z reports (38 7.2 to 7.5). Names that are data (a guest, a reason, an outlet, a payment method) are printed as they are.

func init() {
	for k, v := range map[string]string{
		"CANCELLATIONS AND NO-SHOWS": "PEMBATALAN DAN NO-SHOW", "GUEST LEDGER": "GUEST LEDGER", "DEPOSIT LEDGER": "BUKU DEPOSIT", "VOIDS, CORRECTIONS AND DISCOUNTS": "VOID, KOREKSI DAN DISKON", "Z REPORT": "LAPORAN Z",
		"Period": "Periode", "Currency": "Mata uang", "Fees posted": "Biaya diposting", "Deposits held": "Deposit ditahan", "Events": "Kejadian", "By kind": "Per jenis", "By reason": "Per alasan",
		"By source": "Per sumber", "By company": "Per perusahaan", "By user": "Per pengguna", "By outlet": "Per outlet",
		"Date": "Tanggal", "Reservation": "Reservasi", "Guest": "Tamu", "Kind": "Jenis", "Rooms": "Kamar", "Nights": "Malam", "Value lost": "Nilai hilang", "Fee": "Biaya", "Reason": "Alasan",
		"Cancelled": "Dibatalkan", "Room cancelled": "Kamar dibatalkan", "No-show": "No-show", "Reinstated": "Diaktifkan kembali",
		"As of": "Per tanggal", "Deposit release": "Pelepasan deposit", "Balance": "Saldo", "Folios": "Folio", "Folio": "Folio", "Owner": "Pemilik", "Room": "Kamar", "Charges": "Tagihan", "Settlements": "Penyelesaian",
		"Deposit held": "Deposit ditahan", "Days": "Hari", "Total": "Total", "Aging of the debts": "Umur piutang", "Age (days)": "Umur (hari)", "Checks": "Pemeriksaan", "Report": "Laporan",
		"Other source": "Sumber lain", "Difference": "Selisih", "Opening": "Awal", "Received": "Diterima", "Applied": "Dipakai", "Refunded": "Dikembalikan", "Closing": "Akhir",
		"The report does not agree with its control source: see the difference above.": "Laporan tidak sama dengan sumber kontrolnya: lihat selisih di atas.",
		"Revenue taken back": "Pendapatan ditarik kembali", "Share of the net revenue": "Porsi dari pendapatan neto", "Reference": "Referensi", "Description": "Keterangan", "Net": "Neto", "Gross": "Bruto",
		"By": "Oleh", "Count": "Jumlah",
		"Cash outside a shift": "Tunai di luar shift", "Voided after close": "Dibatalkan setelah tutup", "Drawer": "Laci kas", "Cashier": "Kasir", "Opened": "Dibuka", "Closed": "Ditutup",
		"Opening float": "Modal awal", "Cash payments": "Pembayaran tunai", "Cash refunds": "Refund tunai", "Cash receipts": "Penerimaan tunai", "Pay-ins": "Pemasukan kas", "Pay-outs": "Pengeluaran kas",
		"Drops": "Setoran (drop)", "Expected cash": "Tunai seharusnya", "Counted cash": "Tunai dihitung", "Over / short": "Lebih / kurang", "Amount": "Jumlah", "Method": "Metode",
		"By drawer": "Per laci kas", "Shifts": "Shift", "Takings": "Penerimaan", "Property": "Properti", "Takings by method": "Penerimaan per metode", "On a shift": "Pada shift",
		"Outside a shift": "Di luar shift", "Cashier report": "Laporan kasir", "Approved by": "Disetujui oleh",
		"PAYMENT": "Pembayaran", "REFUND": "Refund",
		"VOID": "Void", "ADJUSTMENT": "Penyesuaian", "ALLOWANCE": "Allowance", "DISCOUNT": "Diskon", "COMP": "Komplimen", "POS_VOID": "Void POS",
		"Cash": "Tunai", "Card": "Kartu", "Bank transfer": "Transfer bank", "City ledger": "Piutang perusahaan", "Phone": "Telepon", "E-mail": "E-mail", "Website": "Situs web", "OTA": "OTA", "Agent": "Agen",
		"Every day": "Setiap hari", "On close": "Saat tutup", "Void": "Void", "Adjustment": "Penyesuaian", "Allowance": "Allowance", "Discount": "Diskon", "POS void": "Void POS", "Payment": "Pembayaran", "Refund": "Refund",
		"Item": "Pos", "Totals": "Total", "Service": "Servis", "Revenue reduced": "Pendapatan dikurangi", "Money cancelled or returned": "Uang dibatalkan/dikembalikan",
		"No reason": "Tanpa alasan", "No source": "Tanpa sumber", "No company": "Tanpa perusahaan",
	} {
		if _, ok := idExact[k]; !ok {
			idExact[k] = v
		}
	}
	idRules = append(idRules,
		rx(`^Z report (.*)$`, "Laporan Z $1"),
		rx(`^X report \(shift open\) (.*)$`, "Laporan X (shift terbuka) $1"),
	)
}
