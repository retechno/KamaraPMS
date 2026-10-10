# Referensi desain: UI/UX uplift

File di folder ini adalah ACUAN VISUAL, bukan kode aplikasi. Jangan diimpor dan jangan disalin mentah ke `web/`.
Terjemahkan ke komponen Vue + utility Tailwind + token di `web/src/assets/tailwind.css`, mengikuti `docs/ui-uplift-plan.md`.

| File | Isi | Dipakai di tahap |
|---|---|---|
| `2-warna.dc.html` | Palet merek, token status (warna, latar, kepekatan), tipografi, mode gelap | 1.1, 1.2, 1.5, 2.1 |
| `3-beranda.dc.html` | Sidebar "Disematkan" (item Kinerja hanya untuk manajer), top bar ringkas, beranda "Hari Ini" tanpa angka uang, grafik 14 malam | 2.2, 2.3, 3.1 |
| `4-status-kamar.dc.html` | Tile kamar, chip filter + jumlah, bar aksi massal, tape chart per reservasi | 3.2, 3.3 |
| `5-housekeeping-mobile.dc.html` | Tugas housekeeping di ponsel (390 px) | 3.4 |
| `6-kinerja.dc.html` | Halaman Kinerja manajer (izin `report.view`) | 3.1b |
| `6b-kinerja-tanpa-akses.dc.html` | Tampilan saat `/performance` dibuka tanpa izin | 3.1b |
| `7-panel-kamar.dc.html` | Ctrl K mencari nomor kamar → panel kamar (tamu, folio, aksi cepat, riwayat) | 3.5 |
| `8-check-in.dc.html` | Check-in 4 langkah dalam satu panel samping (interaktif) | 3.6 |
| `9-audit-ulang.dc.html` | Temuan dari 259 screenshot aplikasi yang berjalan (bukti + perbaikan) | 1.6–1.12, 2.2b |
| `1-audit.dc.html` | Ringkasan temuan (konteks, tidak untuk diimplementasikan) | — |

Cara membaca:
- Nilai warna, ukuran font, radius, jarak, dan tinggi tombol ada di atribut `style="…"`. Itu nilai yang dimaksud.
- `<x-dc>`, `<helmet>`, `<sc-for>`, `<sc-if>` adalah format editor desain: `sc-for` = `v-for`, `sc-if` = `v-if`, dan `{{x}}` diisi dari `renderVals()` di bagian bawah file (data contoh).
- Data (nama tamu, angka) hanya contoh. Di aplikasi ambil dari API.
- Teks berbahasa Indonesia; di aplikasi semua teks lewat i18n (en + id).
- Font final: Plus Jakarta Sans. Di mockup dimuat dari Google Fonts; di aplikasi pakai `@fontsource-variable/plus-jakarta-sans` (self-host).
