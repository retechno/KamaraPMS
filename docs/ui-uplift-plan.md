# UI/UX uplift: KamaraPMS web

Hasil audit UI/UX (kanvas "KamaraPMS — Audit UI/UX"). Simpan file ini di `docs/ui-uplift-plan.md`.
Kerjakan per tahap; setiap tahap = satu branch + satu PR.
Acuan visual ada di `docs/design/` (baca `docs/design/README.md`). Untuk Tahap 2 dan 3, buka artboard yang disebut di sana sebelum menulis kode, lalu samakan warna, ukuran, dan tata letaknya.

## Aturan yang tidak boleh dilanggar (dari CLAUDE.md)

- Redesign hanya mengubah template dan style. Jangan ubah `data-testid`, atribut `name` pada input, atau teks heading yang dibaca test.
- `web/src/assets/tailwind.css` tetap satu-satunya stylesheet. Tanpa preflight.
- Semua teks baru lewat `src/i18n` (en + id). Jangan menulis string langsung di template.
- Sebelum selesai: `cd web && npm test && npm run type-check && npm run build`.

---

## Tahap 1: perbaikan cepat (1–2 hari)

### 1.1 Token teks yang kontrasnya cukup (WCAG AA 4,5:1)
Saat ini `--ok #2e9d6a` = 3,42:1 dan `--warning #c98a10` = 2,95:1 di atas putih.
- Tambahkan di `:root` (terang): `--ok-text: #1b7a4b; --warn-text: #8a5300; --danger: #b4332c;`
- Mode gelap: `--ok-text: #5fd0a0; --warn-text: #f0a35e;`
- Di `@theme inline`: `--color-success-text: var(--ok-text); --color-warning-text: var(--warn-text);`
- Ganti `text-success` → `text-success-text` dan `text-warning` → `text-warning-text` di semua tempat yang mewarnai TEKS (KpiCard `toneClass`, jumlah kamar kotor di ManagerDashboard, delta ▲/▼, dll.). `bg-success/15` dan sejenisnya tetap.

### 1.2 Font yang benar-benar dimuat + angka tabular
Keputusan final: **Plus Jakarta Sans** (bukan Inter).
- `npm i @fontsource-variable/plus-jakarta-sans`, lalu import di `main.ts`.
- Ganti `'Inter'` dengan `'Plus Jakarta Sans Variable'` di `html, body` dan `--font-sans`.
- Tambahkan di base: `body { font-variant-numeric: tabular-nums; }`.

### 1.3 Semua uang lewat `$money`
`views/dashboard/ManagerDashboard.vue`: KPI ADR, RevPAR, Revenue today, City ledger, dan kolom "This month" (`r.now`) masih tampil sebagai string mentah. Bungkus dengan `$money(...)`; okupansi tetap persen. Bisa ditambah varian ringkas (Rp 1,25 jt) untuk KPI.

### 1.4 Pesan error untuk manusia
Pola `{{ error.message }} <code>{{ error.code }}</code>` berulang di banyak view. Buat komponen `ErrorNotice` (sudah ada di `components/app`) yang menampilkan pesan, langkah berikutnya (jika ada di i18n `errors.<CODE>.action`), dan kode di `<details>`. Ganti pemakaian langsung itu secara bertahap.

### 1.5 Pilihan tema
- `@custom-variant dark (&:where([data-theme=dark], [data-theme=dark] *));`
- Blok `@media (prefers-color-scheme: dark)` hanya berlaku saat `:root:not([data-theme=light])`; tambah blok `:root[data-theme=dark]` dengan nilai yang sama.
- Composable `useTheme()` (light | dark | system, disimpan di localStorage dengan try/catch) dan pilihannya di TopBar.

### Tambahan dari audit screenshot (lihat `docs/design/9-audit-ulang.dc.html`)

1.6 **Satu modul format** `src/utils/format.ts`: `formatMoney` (pemisah ribuan id-ID, tanpa desimal untuk IDR, opsi ringkas "Rp 1,25 jt", negatif ditandai dan diberi label "kredit"), `formatPercent` (koma desimal: 67,35%), `formatDate` ("10 Okt 2026"), `formatDateTime` (zona waktu properti, tanpa detik, bukan UTC). Ganti semua angka/tanggal mentah: dasbor (kolom bulan ini, "terutang 220000"), ringkasan kasir, grid tarif, limit kredit, laporan keuangan, audit log, `2026-10-10 sampai …` di panel check-in. Input tanggal: `<input type="date">` bawaan tetap dipakai (formatnya mengikuti locale browser); tanggal terpilih ditampilkan terformat ("Sab, 10 Okt 2026") sebagai teks bantu di bawah input (`$weekday`, dipasang di audit log, kasir, kedatangan, keberangkatan, laporan, dan laporan keuangan). **Custom date input (dd/mm/yyyy) ditunda.**
1.7 **Label untuk setiap enum**: metode bayar (CARD → Kartu, BANK_TRANSFER → Transfer bank, CASH → Tunai, OTHER → Lainnya), status housekeeping di teks/opsi select ("403 · Bersih", bukan "403 · CLEAN"), status stay/folio (OPEN → Menginap), ALREADY_POSTED/READY, ACTIVE, nama aksi audit log (`payment.posted` → "Pembayaran dicatat"), "room #154" → "Kamar 305", "laci MAIN" → "Laci utama", "#26" → nomor folio. Kode error API tidak ditampilkan di banner (pindah ke `<details>`, lihat 1.4). Tambahkan tes yang memindai template/i18n untuk pola `[A-Z]+_[A-Z]+` yang tampil sebagai teks.
1.8 **Tombol tanpa garis bawah**: tambahkan `no-underline` ke kelas dasar `buttonVariants` (`components/ui/button/index.ts`); `<a>` di dalam `Button as-child` saat ini mewarisi underline karena tidak ada preflight.
1.9 **Overlay untuk Sheet**: `components/ui/sheet/SheetContent.vue` belum punya `DialogOverlay` seperti `DialogContent.vue`; tambahkan `bg-black/40`.
1.10 **Overflow di ponsel**: baris tab Front desk (Kedatangan/In-house/Keberangkatan) bisa digeser (`overflow-x-auto`, tanpa melebarkan halaman); kolom aksi berupa deretan link (Departemen, dll.) jadi menu ⋯; tabel lebar diberi `overflow-x-auto` di dalam kartunya. Uji: tidak ada screenshot ponsel lebih lebar dari 390 px.
1.11 **Terjemahan**: lengkapi id untuk label menu (Front desk, In-house, Walk-in, Night audit, City ledger → pilih istilah baku dan catat di glosarium `docs/glossary.md`), laporan keuangan, akun sistem, daftar izin peran, 404, breadcrumb "Account"/"Stay". Perbaiki judul SPT: "Pengembalian pajak" → "SPT Masa PPN" (pengembalian = restitusi). Samakan judul halaman dengan label menu (Kedatangan, bukan "Front desk"; Tahun buku vs Tahun fiskal; Paket tarif vs Rate plan).
1.12 **Dasbor sebelum hari ditutup**: ADR/RevPAR/pendapatan hari ini menampilkan "Tersedia setelah night audit", bukan "0"; grafik tren minimal 7 hari (tampilkan batang kosong); kartu "Status sistem" hanya untuk admin.

---

## Tahap 2: sistem status & shell (±1 minggu)

### 2.1 Token status terpisah dari warna aksi
Teal `primary` hanya untuk tombol, link, dan menu aktif. Tambahkan:

| Token | Teks/isi | Latar | Pemakaian |
|---|---|---|---|
| `--status-dirty` | #a8460a | #fdebdc | DIRTY |
| `--status-cleaning` | #5b3bb0 | #efe9fb | CLEANING, IN_PROGRESS |
| `--status-clean` | #1b7a4b | #e3f3ea | CLEAN |
| `--status-inspected` | #fff on #1b7a4b | — | INSPECTED (isi pekat = siap jual) |
| `--status-booked` | #2348a8 | #e4ebfb | CONFIRMED, RESERVED |
| `--status-inhouse` | #fff on #2552c4 | — | IN_HOUSE, CHECKED_IN, OCCUPIED |
| `--status-closed` | #4a5361 | #eef0f2 | CHECKED_OUT, CLOSED, COMPLETED |
| `--status-ooo` | #fff on #4a5361 + arsir | — | BLOCKED, out of order |

- Tambahkan varian badge baru di `components/ui/badge/index.ts` (dirty, cleaning, clean, inspected, booked, inhouse, closed, ooo).
- Perbarui `components/app/statusMap.ts` agar memakai varian itu. Tidak ada status yang memakai `default` (teal).
- Ekspor juga `statusSwatch(domain, status)` untuk legend dan tile, lalu pakai di `RoomStatusView` (`tileClass`, `legend`) dan `TapeChartView` (legend + warna sel), supaya RESERVED/BLOCKED sama di semua halaman.
- Selalu tampilkan label/ikon bersama warna.

### 2.2 Top bar ringkas
- Gabungkan tanggal bisnis + status night audit jadi satu chip (link ke /night-audit).
- Pencarian jadi input lebar yang membuka CommandPalette.
- Bahasa, tema, akun, dan keluar pindah ke menu avatar (reka-ui DropdownMenu/Popover).
- `data-testid` lama tetap ada (property-switcher, business-date, night-audit-status, open-search, language-switcher, user-name, sign-out), sekarang di dalam menu bila perlu.

### 2.2b Pola halaman yang seragam
- Semua halaman memakai `PageHeader` (judul + deskripsi kiri, aksi kanan); tombol "Tambah" tidak lagi di bawah deskripsi.
- Daftar panjang (reservasi, tamu, folio, audit log, bagan akun) memakai paginasi 50 baris; baris bisa diklik untuk membuka detail; aksi sekunder masuk menu ⋯ (daftar reservasi saat ini 3 tombol per baris).
- Tabel di bawah `md` tampil sebagai kartu (pola Kedatangan ponsel yang sudah ada), terutama Housekeeping, Folio, Kasir, Jurnal, Akun.
- Empty state selalu punya tombol aksi; halaman 404 punya tautan "Kembali ke Hari Ini".
- Tombol nonaktif: teks lebih gelap (juga di mode gelap).
- Halaman peran: grup izin bisa dilipat, status centang "sebagian", label izin dalam bahasa Indonesia, bar Simpan menempel di bawah.

### 2.3 Navigasi
- Bagian "Disematkan" di atas (pin dari judul halaman, disimpan di localStorage) dan "Terakhir dibuka" (5 item).
- Bagian `finance` dipecah per grup (accounting, payables, tax, bank, budget) dan defaultnya tertutup.

### 2.4 Jejak audit menyimpan label entitas
Jejak audit sekarang menamai entitas dari nomor yang kebetulan ada di datanya ("Stay STY000035"); bila tidak ada, yang tampil "Kamar #154". Perbaikan yang benar ada di backend, bukan di layar:
- Saat mencatat, `audit.Writer.Write` ikut menyimpan `entity_label` (mis. "Kamar 305", "Reservasi RES000012", "Folio FOL000026"), dibentuk oleh modul yang menulis entri itu (ia yang tahu nomornya). Migrasi menambah kolom `entity_label` (boleh null untuk entri lama).
- API audit mengembalikan `entity_label` dan menyaring pencarian nomor dokumen lewat parameter `q` (cocok dengan `entity_label`), sehingga pencarian "Nomor dokumen" di layar tidak lagi membaca halaman demi halaman (sementara ini: RES... dicari lewat reservasi, nomor lain dicari di isi entri, maksimal 10 halaman).
- Layar: kolom entitas memakai `entity_label`, dan pencarian nomor dokumen memanggil `q`.
Ditunda dari Tahap 1 atas permintaan: backend tidak diubah sekarang.

### 2.5 Data bawaan berbahasa Indonesia
Nama departemen dan bagan akun yang dibuat otomatis untuk properti baru masih berbahasa Inggris ("Front Office", "Room revenue - transient", "Cash on hand - front desk"), sehingga laporan keuangan di layar Indonesia bercampur bahasa.
- Saat membuat properti, pilih bahasa template data bawaan (Indonesia atau Inggris; bawaan mengikuti bahasa halaman). Template departemen dan bagan akun disediakan dalam dua bahasa di backend; `pms-admin` dan `pms-seed` menerima pilihan yang sama.
- Hanya properti baru yang memakainya. Data yang sudah ada tidak diubah, dan nama akun tetap data yang bisa diubah pengguna.
- Kode akun dan `map_key` tidak berubah di kedua template, jadi laporan, pemetaan akun sistem, dan anggaran bekerja sama.

### 2.6 Laporan keuangan: filter tanggal terisi
Filter tanggal laporan keuangan (laba rugi, neraca, arus kas, neraca saldo, buku besar, laporan departemen) kosong ("dd/mm/yyyy") walaupun laporan yang tampil memiliki periode. Isi filter dengan periode yang sedang ditampilkan: bulan berjalan sampai tanggal bisnis untuk laporan berentang, tanggal bisnis untuk neraca. Pilihan cepat (bulan ini, bulan lalu, tahun buku berjalan) di samping filter. Berlaku juga bagi filter tanggal di ReportsView.

---

## Tahap 3: alur kerja baru (2–3 minggu)

1. **Beranda "Hari Ini"** (`HomeView`, untuk semua staf): baris aksi (Reservasi baru, Walk-in), strip operasional TANPA angka uang (okupansi malam ini + meter, kedatangan, keberangkatan, kamar siap jual), lalu tiga kolom: Kedatangan (kesiapan kamar + tombol Check-in), Keberangkatan (saldo + Check-out), Perlu perhatian. Grafik 14 malam dengan garis 50/100%. `ManagerDashboard` DIKELUARKAN dari beranda (lihat poin 1b).
1b. **Halaman Kinerja manajer** (acuan: `docs/design/6-kinerja.dc.html` dan `6b-kinerja-tanpa-akses.dc.html`):
   - Rute baru `/performance` (name `performance`, view baru `views/dashboard/PerformanceView.vue`) yang memuat isi `ManagerDashboard` dengan tata letak baru: 4 kartu KPI (okupansi, ADR, RevPAR, pendapatan kamar) dengan delta vs bulan lalu + sparkline dari `trend`; grafik okupansi 14 hari (`trend`) dan 14 malam ke depan (`forecast`, batang ≥ 90% berwarna pekat); tabel bulan berjalan vs bulan lalu; "Diterima hari ini" per metode (`payments_by_method`); kartu city ledger dan saldo tamu menginap. Semua data dari endpoint `/api/v1/properties/{propertyId}/dashboard` yang sudah ada; tidak perlu perubahan backend.
   - Akses ditentukan oleh izin `report.view` pada properti aktif (`auth.can('report.view', property.currentId)`), bukan oleh `isAdmin`.
   - Navigasi: tambahkan field `permission?: string` pada `NavItem` dan item `{ id: 'performance', to: '/performance', permission: 'report.view' }` di bagian `endOfDay`. Ubah `visibleNavigation` agar menerima fungsi `can(permission)` dan menyembunyikan item tanpa izin; perbarui `navigation.test.ts`, `SidebarNav`, `TopBar`, dan `CommandPalette` yang memanggilnya.
   - Membuka `/performance` tanpa izin: tampilkan layar "Halaman Kinerja khusus manajer" (`EmptyState` dengan ikon gembok + tombol "Kembali ke Hari Ini"), bukan halaman kosong atau error API.
   - Pemilih periode (Hari ini / Bulan berjalan / 30 hari): kerjakan "Hari ini" dan "Bulan berjalan" dulu dengan data yang ada; "30 hari" menunggu dukungan API.
   - Teks baru lewat i18n (`nav.items.performance`, `performance.*`) dalam en dan id. Pindahkan/perbarui test `ManagerDashboard.test.ts` dan `HomeView.test.ts` sesuai pemindahan ini, dengan `data-testid` lama (kpi-occupancy, kpi-adr, dll.) dipertahankan di halaman baru.
2. **Status kamar**: tile berwarna menurut kebersihan, hunian sebagai ikon/label; chip filter dengan jumlah; pilih banyak + bar aksi (Tandai bersih / diperiksa / tugaskan) memakai endpoint status yang ada, satu per kamar dalam loop atau endpoint batch bila ditambahkan.
3. **Tape chart**: satu elemen per reservasi (CSS grid `grid-column: start / span n`), teks minimal 13px, drag untuk pindah kamar (konfirmasi dulu), tarik ujung untuk ubah malam.
4. **Housekeeping di ponsel**: tampilan `HousekeepingTasksView` di layar < 640px dengan kartu besar, tombol 52px "Mulai" / "Selesai", progres shift.
5. **Panel kamar** (acuan `7-panel-kamar.dc.html`): `CommandPalette` mengenali nomor kamar, no. konfirmasi, nama tamu, dan perintah ("+ 305" = tambah tagihan). Enter membuka `RoomPanel` (Sheet kanan 560px): nomor kamar besar, chip status (hunian, kebersihan, saldo), tamu + malam ke-n, permintaan khusus, ringkasan folio (tagihan/dibayar/saldo), 6 aksi cepat, riwayat hari ini dari audit log. Komponen `RoomLink` dipakai di semua tempat nomor kamar muncul (tabel, tape chart, folio) dan membuka panel yang sama. Pintasan P/T/O hanya aktif saat panel fokus (bukan keydown global).
6. **Check-in satu panel** (acuan `8-check-in.dc.html`): `ArrivalDrawer`/`CheckInPanel` menjadi stepper 4 langkah: Tamu (diisi dari kunjungan terakhir) → Kamar (hanya tipe yang dipesan, urut: Diperiksa, Bersih, Dibersihkan, Kotor; pemilihan kamar kotor memakai alur override yang sudah ada) → Deposit (perkiraan malam × harga + insidental; metode Kartu/QRIS/Transfer/Tunai; city ledger membebaskan deposit) → Selesai (cetak kartu registrasi, e-mail, tombol "check-in berikutnya" ke kedatangan berikutnya). Enter = lanjut. Target < 60 detik untuk tamu repeat. QRIS butuh nilai baru di `PaymentMethod` (perubahan API + migrasi): putuskan dulu, atau sementara catat sebagai OTHER dengan referensi.
