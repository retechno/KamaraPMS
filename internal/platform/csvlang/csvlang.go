// Package csvlang puts the header of a CSV report in the language the request asked for (`lang=id`). The cells are
// left as they are (dates stay ISO, amounts plain numbers), so a spreadsheet reads them in either language; only the
// names of the columns follow the language. English keeps the stable names an integration can rely on.
package csvlang

import (
	"net/http"
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
	"active": "Aktif", "adr": "ADR", "adults": "Dewasa", "amount": "Jumlah", "arrival_date": "Tanggal kedatangan", "arrivals": "Kedatangan",
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
}
