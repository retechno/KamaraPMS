// Package csvlang puts the header of a CSV report in the language the request asked for (`lang=id`). The cells are
// left as they are (dates stay ISO, amounts plain numbers), so a spreadsheet reads them in either language; only the
// names of the columns follow the language. English keeps the stable names an integration can rely on.
package csvlang

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
)

// Indonesian reports whether the request asked for Indonesian.
func Indonesian(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("lang")), "id")
}

// Header returns the column names in the language of the request; a name the language has no word for is kept.
func Header(r *http.Request, names []string) []string {
	if !Indonesian(r) {
		return names
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = Word(r, n)
	}
	return out
}

// Word translates one column name or fixed cell ("Total").
func Word(r *http.Request, s string) string {
	if !Indonesian(r) {
		return s
	}
	if t, ok := id[s]; ok {
		return t
	}
	return s
}

var id = map[string]string{ //nolint:gosec // G101: column names, not credentials
	"above_target":          "Di atas target",
	"age_hours":             "Umur (jam)",
	"approved_on":           "Tanggal disetujui",
	"approver":              "Penyetuju",
	"awaiting":              "Menunggu",
	"bill_cost":             "Biaya tagihan",
	"bill_unit_price":       "Harga tagihan per satuan",
	"bills":                 "Tagihan",
	"book_qty":              "Jumlah buku",
	"change_pct":            "Perubahan %",
	"class":                 "Kelas",
	"cost":                  "Biaya",
	"cost_pct":              "Biaya %",
	"cost_per_cover":        "Biaya per porsi",
	"cost_per_room":         "Biaya per kamar",
	"counted_qty":           "Jumlah ditemukan",
	"covers":                "Porsi",
	"created_by":            "Dibuat oleh",
	"department":            "Departemen",
	"document":              "Dokumen",
	"documents":             "Dokumen",
	"entered_at":            "Masuk level",
	"from_warehouse":        "Dari gudang",
	"gain":                  "Lebih",
	"guests":                "Tamu",
	"has_recipe":            "Punya resep",
	"no_price":              "Bahan tanpa harga",
	"item":                  "Barang",
	"level":                 "Level",
	"lines":                 "Baris",
	"lines_above_tolerance": "Baris di atas toleransi",
	"loss":                  "Kurang",
	"margin":                "Margin",
	"month":                 "Bulan",
	"month_cost":            "Biaya bulan ini",
	"net_price":             "Harga bersih",
	"number":                "Nomor",
	"occupied_rooms":        "Kamar terisi",
	"outlet":                "Outlet",
	"pct":                   "%",
	"prev_cost_per_room":    "Biaya per kamar bulan lalu",
	"product":               "Produk",
	"qty":                   "Jumlah",
	"receipt_unit_price":    "Harga terima per satuan",
	"receipt_value":         "Nilai diterima",
	"requester":             "Peminta",
	"step":                  "Langkah",
	"supplier":              "Pemasok",
	"target_pct":            "Target %",
	"to_warehouse":          "Ke gudang",
	"transfer":              "Transfer",
	"unit":                  "Satuan",
	"variance":              "Selisih",
	"variance_qty":          "Selisih jumlah",
	"variance_value":        "Nilai selisih",
	"warehouse":             "Gudang",
	"active":                "Aktif", "adr": "ADR", "adults": "Dewasa", "amount": "Jumlah", "arrival_date": "Tanggal kedatangan", "arrivals": "Kedatangan",
	"avg_hours_to_resolve": "Rata-rata jam penyelesaian", "avg_task_minutes": "Rata-rata menit tugas", "balance": "Saldo", "base_amount": "Dasar",
	"base_occupancy": "Okupansi dasar", "block": "Blokir", "business_date": "Tanggal bisnis", "cancelled": "Dibatalkan", "category": "Kategori",
	"charge_code": "Kode biaya", "charge_type": "Tipe biaya", "children": "Anak", "closing_credit": "Kredit akhir", "closing_debit": "Debit akhir",
	"code": "Kode", "component_type": "Tipe komponen", "confirmation_number": "Nomor konfirmasi", "count": "Jumlah", "credit": "Kredit",
	"date": "Tanggal", "debit": "Debit", "departure_date": "Tanggal keberangkatan", "departures": "Keberangkatan", "description": "Keterangan",
	"discount_amount": "Diskon", "dnd": "Jangan ganggu", "email": "Email", "floor": "Lantai", "guest": "Tamu", "hours": "Jam", "items": "Item",
	"journal": "Jurnal", "kind": "Jenis", "last_name": "Nama belakang", "line": "Baris", "name": "Nama", "net": "Neto", "net_amount": "Neto",
	"nights": "Malam", "nights_without_rate": "Malam tanpa tarif", "no_shows": "Tidak datang", "occupancy": "Okupansi", "occupancy_kind": "Jenis kamar",
	"occupancy_percent": "Okupansi (%)", "opening_credit": "Kredit awal", "opening_debit": "Debit awal", "payment_method": "Metode pembayaran",
	"payments": "Pembayaran", "priority": "Prioritas", "rate": "Tarif", "rate_plan": "Rate plan", "reason": "Alasan", "refunds": "Pengembalian",
	"reported": "Dilaporkan", "resolved": "Selesai", "revpar": "RevPAR", "room": "Kamar", "room_nights_sold": "Malam kamar terjual", "room_number": "Nomor kamar",
	"room_revenue": "Pendapatan kamar", "room_type": "Tipe kamar", "rooms": "Kamar", "rooms_cleaned": "Kamar dibersihkan", "rooms_complimentary": "Kamar complimentary",
	"rooms_house_use": "Kamar house use", "rooms_inspected": "Kamar diperiksa", "rooms_occupied": "Kamar terisi", "rooms_out_of_order": "Kamar rusak",
	"rooms_out_of_service": "Kamar tidak dipakai", "rooms_sellable": "Kamar dapat dijual", "rooms_total": "Total kamar", "service_charge": "Biaya layanan",
	"since": "Sejak", "source_ref": "Referensi sumber", "source_type": "Tipe sumber", "status": "Status", "stay_number": "Nomor menginap", "still_open": "Masih terbuka",
	"tasks_done": "Tugas selesai", "tasks_skipped": "Tugas dilewati", "tax": "Pajak", "total": "Total", "type": "Tipe", "user": "Pengguna", "value": "Nilai",
	"voided": "Dibatalkan", "voided_count": "Jumlah dibatalkan", "reservation": "Reservasi", "reservation_room_id": "ID kamar reservasi", "stay": "Menginap",
	"Total": "Total", "Opening balance": "Saldo awal",
	"group": "Kelompok", "key": "Kunci", "definite": "Definite", "definite_events": "Acara definite", "tentative": "Tentative", "tentative_events": "Acara tentative",
	"event_type": "Tipe acara", "sales": "Sales", "room_code": "Kode ruang", "available_hours": "Jam tersedia", "occupancy_pct": "Okupansi (%)",
	"posted_sessions": "Sesi diposting", "revenue_per_hour": "Pendapatan per jam", "event_number": "Nomor acara", "event": "Acara", "session": "Sesi", "estimated": "Estimasi",
	"guaranteed": "Dijamin", "actual": "Aktual", "difference": "Selisih", "created": "Dibuat", "lost": "Hilang", "definite_value": "Nilai definite", "lost_value": "Nilai hilang",
	"conversion_pct": "Konversi (%)", "closed_on": "Tanggal ditutup", "reason_code": "Kode alasan", "note": "Catatan", "start": "Mulai", "end": "Selesai", "setup": "Susunan",
	"contact": "Kontak", "phone": "Telepon", "beo_revision": "Revisi BEO", "beo_unread": "BEO belum dibaca", "posting_mode": "Mode posting", "days_late": "Hari terlambat",
	"blocked": "Terblokir", "inactive_code": "Kode tidak aktif", "available": "Tersedia", "revenue": "Pendapatan",
	"avg_price":              "Harga rata-rata",
	"receipts":               "Penerimaan",
	"billed_value":           "Nilai ditagih",
	"receipt":                "Penerimaan",
	"purchase_unit":          "Satuan beli",
	"qty_purchase":           "Jumlah beli",
	"unit_price":             "Harga satuan beli",
	"price_per_stock":        "Harga per satuan stok",
	"last_out_on":            "Terakhir keluar",
	"days_since_out":         "Hari sejak keluar",
	"never_out":              "Belum pernah keluar",
	"age_days":               "Umur stok (hari)",
	"rank":                   "Peringkat",
	"share_pct":              "Porsi (%)",
	"cumulative_pct":         "Kumulatif (%)",
	"opening_qty":            "Jumlah awal",
	"opening_value":          "Nilai awal",
	"closing_qty":            "Jumlah akhir",
	"closing_value":          "Nilai akhir",
	"opening_stock_qty":      "Jumlah saldo awal opn",
	"opening_stock_value":    "Nilai saldo awal opn",
	"receipts_qty":           "Jumlah penerimaan",
	"receipts_value":         "Nilai penerimaan",
	"transfers_in_qty":       "Jumlah transfer masuk",
	"transfers_in_value":     "Nilai transfer masuk",
	"sales_returns_qty":      "Jumlah retur penjualan",
	"sales_returns_value":    "Nilai retur penjualan",
	"pos_sales_qty":          "Jumlah penjualan pos",
	"pos_sales_value":        "Nilai penjualan pos",
	"usage_qty":              "Jumlah pemakaian",
	"usage_value":            "Nilai pemakaian",
	"waste_qty":              "Jumlah Waste",
	"waste_value":            "Nilai Waste",
	"staff_meals_qty":        "Jumlah makan karyawan",
	"staff_meals_value":      "Nilai makan karyawan",
	"entertainment_qty":      "Jumlah Entertainment",
	"entertainment_value":    "Nilai Entertainment",
	"transfers_out_qty":      "Jumlah transfer keluar",
	"transfers_out_value":    "Nilai transfer keluar",
	"supplier_returns_qty":   "Jumlah retur ke pemasok",
	"supplier_returns_value": "Nilai retur ke pemasok",
	"count_variance_qty":     "Jumlah selisih opname",
	"count_variance_value":   "Nilai selisih opname",
	"cost_adjust_qty":        "Jumlah penyesuaian harga",
	"cost_adjust_value":      "Nilai penyesuaian harga",
}

// Write answers rows as a CSV download, the header in the language of the request. Text that a spreadsheet could read as a formula is prefixed with an apostrophe.
func Write(w http.ResponseWriter, r *http.Request, filename string, rows [][]string) error {
	if len(rows) > 0 {
		rows[0] = Header(r, rows[0])
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	out := csv.NewWriter(w)
	for _, row := range rows {
		safe := make([]string, len(row))
		for i, c := range row {
			if c != "" && (strings.ContainsRune("=+@\t\r", rune(c[0])) || strings.HasPrefix(c, "-") && !isNumber(c)) {
				c = "'" + c
			}
			safe[i] = c
		}
		if err := out.Write(safe); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
