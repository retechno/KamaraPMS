package documents

// The Indonesian of the daily flash report (38 7.1). The names of the rows that are data (a charge code, an outlet, a room type, a payment method) are printed as they are.

func init() {
	for k, v := range map[string]string{
		"DAILY FLASH REPORT": "LAPORAN FLASH HARIAN", "Daily flash report": "Laporan flash harian", "Business date": "Tanggal bisnis", "Month to date": "Bulan berjalan", "Year to date from": "Tahun berjalan sejak",
		"INTERIM": "SEMENTARA", "Room statistics": "Statistik kamar", "Activity": "Aktivitas", "Covers": "Cover", "Net revenue": "Pendapatan neto", "Revenue by source": "Pendapatan per sumber",
		"Payments received (GTP)": "Pembayaran diterima (GTP)", "Control balance": "Kontrol saldo",
		"Today": "Hari ini", "MTD last month": "MTD bulan lalu", "MTD last year": "MTD tahun lalu", "Budget MTD": "Anggaran MTD", "Variance MTD": "Selisih MTD",
		"Total rooms": "Total kamar", "Out of order": "Rusak (out of order)", "Out of service": "Perbaikan ringan (out of service)", "House use": "Dipakai hotel", "Rooms available": "Kamar tersedia",
		"Rooms sold": "Kamar terjual", "Complimentary": "Komplimen", "Vacant": "Kosong", "Walk-in": "Walk-in", "Guests in house": "Tamu menginap",
		"Occupancy % (with complimentary)": "Okupansi % (dengan komplimen)", "Occupancy % (paid rooms)": "Okupansi % (kamar berbayar)", "Double occupancy %": "Okupansi dobel %",
		"Guests per room": "Tamu per kamar", "ARR": "ARR", "ARR including complimentary": "ARR termasuk komplimen", "RevPAR": "RevPAR", "ALOS (nights)": "ALOS (malam)",
		"Reservations made": "Reservasi dibuat", "Cancelled": "Dibatalkan", "No-show": "No-show", "Early check-out": "Check-out lebih awal", "Late check-out": "Check-out terlambat",
		"Arrivals": "Kedatangan", "Departures": "Keberangkatan", "Arrivals tomorrow": "Kedatangan besok", "Departures tomorrow": "Keberangkatan besok",
		"Total revenue (net)": "Total pendapatan (neto)", "Service charge": "Biaya layanan", "Tax": "Pajak", "Gross total revenue (GTR)": "Total pendapatan kotor (GTR)",
		"Revenue posted by journal, not from a folio": "Pendapatan dari jurnal, bukan dari folio", "Hotel": "Hotel", "POS outlets": "Outlet POS", "Banquet": "Banquet", "Other (journals)": "Lainnya (jurnal)",
		"Total Rooms": "Total Kamar", "Total Food and beverage": "Total Makanan dan minuman", "Total Other": "Total Lainnya", "Rooms": "Kamar", "Food and beverage": "Makanan dan minuman", "Other": "Lainnya",
		"Hotel folios": "Folio hotel", "Unmapped": "Tanpa akun pendapatan", "By cashier": "Per kasir", "By payment method": "Per metode pembayaran", "Refunds": "Refund", "Transferred to the city ledger": "Dipindahkan ke city ledger",
		"GTR": "GTR", "Payments received": "Pembayaran diterima", "Expected change of the guest ledger": "Perubahan guest ledger yang diharapkan", "Change of the guest ledger": "Perubahan guest ledger", "Difference": "Selisih",
		"Prepared by (night audit)": "Disiapkan oleh (night audit)", "Approved by (accounting)": "Disetujui oleh (akuntansi)",
		"The control balance has a difference: a row of the ledger did not follow the usual path.":                                         "Kontrol saldo memiliki selisih: ada baris buku yang tidak mengikuti jalur biasa.",
		"Some charge codes have no revenue account: their revenue is printed under Unmapped in group C. Fix the setup of the charge code.": "Ada kode tagihan tanpa akun pendapatan: pendapatannya dicetak di bawah 'Tanpa akun pendapatan' pada kelompok C. Perbaiki pengaturan kode tagihan itu.",
	} {
		if _, ok := idExact[k]; !ok {
			idExact[k] = v
		}
	}
	idRules = append(idRules,
		rx(`^Front office: (.*)$`, "Front office: $1"),
		rx(`^Outlet: (.*)$`, "Outlet: $1"),
		rx(`^The pax of (\d+) banquet session\(s\) is the guaranteed number: no actual count was entered\.$`, "Pax dari $1 sesi banquet adalah jumlah yang dijamin: jumlah sebenarnya belum diisi."),
		rx(`^(\d+) bill\(s\) of the month have no guest count: their covers are not counted\.$`, "$1 bill bulan ini tidak memiliki jumlah tamu: cover-nya tidak dihitung."),
	)
}
