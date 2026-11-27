# Inventaris aplikasi untuk Panduan Pengguna (Fase 1: audit)

Dokumen kerja untuk penulis panduan, bukan untuk staf hotel. Isinya dipetakan dari kode pada commit `27fc62da` (24 Nov 2026): router `web/src/router/index.ts`, menu `web/src/navigation.ts`, teks `web/src/i18n/locales/id.ts`, pemeriksaan izin di `internal/*/http.go` dan `service.go`, serta `docs/architecture/`. Kalau desain dan kode berbeda, yang diikuti adalah kode; perbedaannya dicatat di bagian 9.

Label di dokumen ini adalah teks bahasa Indonesia persis dari `id.ts`. Kode permission (`reservation.read`, dan sejenisnya) hanya dipakai di sini; panduan pengguna menyebut nama izinnya dalam bahasa Indonesia.

---

## 1. Gambaran umum

- **Dua aplikasi dalam satu alamat.**
  - Aplikasi hotel untuk staf: masuk di `/login` dengan **Kode tenant**, **Email**, dan **Kata sandi**.
  - Console platform `/platform/*` untuk operator KamaraPMS. Console punya login sendiri dengan kode authenticator (TOTP), dan staf hotel tidak memakainya.
- **Menu hotel** terdiri dari 14 bagian (`nav.sections`): Front Desk, Kamar, Tagihan, Akhir hari, POS, Aset tetap, Persediaan, Banquet, Akuntansi, Utang usaha, Pajak, Bank, Anggaran, dan Pengaturan.
- **Kapan sebuah menu tampil.** Item disembunyikan kalau:
  - pengguna tidak punya izinnya di properti yang sedang dibuka;
  - item khusus administrator tenant, sedangkan pengguna bukan administrator;
  - modulnya (POS, BANQUET, INVENTORY) tidak termasuk paket langganan.

  Bagian yang kosong hilang dari menu.
- **Alamat yang diketik langsung** juga diperiksa oleh router.
  - Halaman yang punya `meta.permission`, `adminOnly`, atau `module` mengembalikan pengguna yang tidak berhak ke **Hari Ini** tanpa pesan.
  - Halaman lain terbuka, tetapi menampilkan paragraf "tidak punya akses" (`data-testid="no-access"`, ada di 73 view) yang menyebut kode izin yang dibutuhkan.
- **Bilah atas (top bar):**
  - breadcrumb dan pencarian cepat "Cari halaman, tamu, reservasi…" (Ctrl+K);
  - pemilih properti;
  - chip **Tanggal bisnis** dengan status audit malam;
  - lonceng banquet dan lonceng persetujuan persediaan;
  - menu pengguna: **Akun saya**, **Bahasa**, **Tema**, **Keluar**.
- **Bahasa.** Bawaannya English. Pilihan bahasa disimpan di browser, bukan per pengguna di server, jadi panduan perlu menjelaskan cara mengganti ke Bahasa Indonesia.

## 2. Daftar route

Kolom **Izin** berisi permission yang membuat item tampil di menu atau yang diminta router. "—" berarti setiap anggota properti bisa membukanya, lalu halaman memeriksa izinnya sendiri. Kolom **Modul** diisi hanya untuk modul opsional paket. Route detail (`:id`) dikelompokkan dengan halaman induknya.

### 2.1 Umum

| Route | Judul (ID) | Izin | Catatan |
|---|---|---|---|
| `/login` | Masuk ke KamaraPMS | publik | Tidak ada reset kata sandi mandiri; "Lupa kata sandi?" meminta pengguna menghubungi administrator |
| `/` | Hari Ini | — | Kartu staf front desk; kartu "Kamar yang perlu dibersihkan" untuk housekeeping; kartu "Belum ada properti untuk Anda" untuk staf tanpa properti |
| `/account` | Akun | — | Tampilan (terang/gelap), ubah kata sandi (minimal 12 karakter) |
| `/*` (tak dikenal) | Halaman tidak ditemukan | — | |

### 2.2 Front Desk (`frontDesk`)

| Route | Menu / judul | Izin | Catatan |
|---|---|---|---|
| `/` | Hari Ini | — | |
| `/reservations` | Reservasi | `reservation.read` | |
| `/reservations/new` | Reservasi baru | (halaman: `reservation.create`) | |
| `/reservations/:id` | Reservasi {nomor} | (halaman) | |
| `/reservations/tape` | Bagan kamar | `reservation.read` | 14 atau 28 hari; drag-and-drop pakai mouse |
| `/availability` | Ketersediaan | `reservation.read` | Hanya baca |
| `/arrivals`, `/in-house`, `/departures` | Tamu hari ini (tab Kedatangan / Menginap / Keberangkatan) | `reservation.read` | Satu item menu dengan tiga tab |
| `/walk-in` | Walk-in | `reservation.read` (halaman: `frontdesk.checkin` + `reservation.create`) | |
| `/stays/:id` | Menginap {nomor} | (halaman) | Dibuka dari daftar Menginap |
| `/guests`, `/guests/:id` | Tamu | `guest.read` | |
| `/groups`, `/groups/:id` | Grup | `reservation.read` | Master folio dan pembatalan grup |

### 2.3 Kamar (`rooms`)

| Route | Menu / judul | Izin | Catatan |
|---|---|---|---|
| `/room-status` | Status kamar | `reservation.read` | Petugas housekeeping tanpa `reservation.read` tidak bisa membukanya |
| `/housekeeping` | Housekeeping | — | Halaman memeriksa `housekeeping.update` |
| `/housekeeping/my` | Tugas saya | `housekeeping.update` | |
| `/housekeeping/tasks` | Daftar pembersihan | — | `housekeeping.assign` untuk membuat dan menugaskan |
| `/maintenance` | Perawatan | `maintenance.report` | |
| `/lost-found` | Barang tertinggal | `lostfound.report` | |
| `/room-blocks` | Blokir kamar | — | `room_block.manage` untuk membuat dan melepas |

### 2.4 Tagihan (`billing`)

| Route | Menu / judul | Izin | Modul |
|---|---|---|---|
| `/folios`, `/folios/:id` | Folio | `folio.read` | |
| `/cashier` | Kasir | `cashier.shift` (halaman membaca `folio.read`) | |
| `/cashier/shifts` | Shift kasir | `cashier.shift` atau `cashier.shift_manage` | |
| `/room-charges` | Biaya kamar | `nightaudit.run` atau `folio.post_charge` | |
| `/city-ledger`, `/city-ledger/:id` | Piutang perusahaan | `cityledger.read` | |
| `/city-ledger/overdue` | Invoice jatuh tempo | `cityledger.read` | |
| `/outlet-charges` | Beban outlet (judul: "Beban outlet hari ini") | `folio.outlet_charge_open` atau `folio.read` | POS |

### 2.5 Akhir hari (`endOfDay`)

| Route | Menu / judul | Izin |
|---|---|---|
| `/night-audit` | Audit malam | `nightaudit.run` |
| `/night-audit/archive` | Arsip audit malam | `nightaudit.pack_view` |
| `/night-audit/recipients` | Penerima paket (judul: "Penerima paket audit malam") | `nightaudit.pack_manage` |
| `/reports` | Laporan | `report.view` |
| `/reports/daily-flash`, `/cancellations`, `/guest-ledger`, `/deposit-ledger`, `/corrections`, `/z-reports` | Laporan harian (dibuka dari hub di `/reports`) | `report.view`; Z semua laci juga butuh `cashier.shift_manage` |
| `/performance` | Kinerja (judul: "Kinerja properti") | `report.view` |

### 2.6 POS (`pos`, modul POS)

| Route | Menu / judul | Izin |
|---|---|---|
| `/pos/floor`, `/pos/bills/:id` | Denah meja dan bill | `pos.order` atau `pos.cashier` |
| `/pos/waiter`, `/pos/waiter/bills/:id` | Pelayan | `pos.order` |
| `/pos/kds` | Layar dapur | `pos.kds` |
| `/pos/report` | Laporan outlet | `pos.report` |
| `/pos/outlets` | Outlet | `pos.manage` |
| `/pos/products` | Produk dan menu (termasuk kartu **Resep & biaya bahan**) | `pos.manage` |
| `/pos/menu` | Kategori, harga, profil | `pos.manage` |
| `/pos/floor-plan` | Denah meja | `pos.manage` |
| `/pos/stations` | Stasiun dan printer (termasuk perangkat agen cetak) | `pos.manage` |

### 2.7 Aset tetap (`assets`, modul INVENTORY)

"Izin aset apa pun" berarti salah satu dari `asset.read`, `.manage`, `.depreciate`, `.dispose`, atau `.setup`.

| Route | Menu / judul | Izin |
|---|---|---|
| `/assets`, `/assets/:id` | Register aset | izin aset apa pun |
| `/assets/waiting` | Daftar tunggu | izin aset apa pun |
| `/assets/disposals` | Pelepasan aset | izin aset apa pun |
| `/assets/depreciation` | Penyusutan | `asset.depreciate` |
| `/assets/reports`, `/assets/reports/*` (recap, fiscal, register, schedule, locations, expiring, audit-differences) | Laporan aset | izin aset apa pun |
| `/assets/audits`, `/assets/audits/:id` | Opname aset | izin aset apa pun |
| `/assets/opening` | Saldo awal aset | `asset.setup` |
| `/assets/labels` | Label QR | izin aset apa pun |
| `/assets/settings` | Pengaturan aset | izin aset apa pun (ubah: `asset.setup`) |

### 2.8 Persediaan (`inventory`, modul INVENTORY)

"Baca" berarti salah satu dari `inventory.view`, `.manage`, atau `.opening`. "Gerak" berarti salah satu izin pergerakan dan persetujuan persediaan (lihat `navigation.ts`).

| Route | Menu / judul | Izin |
|---|---|---|
| `/inventory` | Ringkasan (judul: "Persediaan") | baca |
| `/inventory/items` (+`/new`, `/:id`) | Barang & stok | baca (form: `inventory.manage`) |
| `/inventory/openings` (+`/new`, `/:id`) | Saldo awal | baca (buat: `inventory.opening`) |
| `/inventory/warehouses` (+form) | Gudang | baca (form: manage) |
| `/inventory/categories` (+form) | Kategori & akun | baca (form: manage) |
| `/inventory/units` (+form) | Satuan | baca (form: manage) |
| `/inventory/costing` | Harga pokok (termasuk Kartu stok) | baca |
| `/inventory/purchasing`, `/requests/*`, `/purchase-orders/*`, `/receipts/*`, `/returns/*` | Pembelian | `inventory.view` / request / purchase / receive / bill / approve / cost_control / final_approve |
| `/inventory/bill-drafts` (+`/:id`) | Draf tagihan | `inventory.bill` atau `inventory.view` |
| `/inventory/requisitions`, `/requisitions/*`, `/transfers/*` | Permintaan & transfer | gerak |
| `/inventory/issues` (+`/new`, `/:id`) | Pemakaian & waste | gerak |
| `/inventory/counts` (+`/:id`) | Opname | gerak |
| `/inventory/amenities`, `/amenities/runs/:date` | Amenities otomatis | `inventory.view` atau `inventory.manage` |
| `/inventory/pos-gap` | Penjualan POS tanpa stok | `inventory.view` atau `inventory.manage` |
| `/inventory/approvals` | Perlu saya setujui | izin persetujuan persediaan |
| `/inventory/approval-chains` | Rantai persetujuan | `property.manage` atau `inventory.manage` (mengubah dan menyimpan hanya `property.manage`; `inventory.manage` membaca) |
| `/inventory/reports` dan 23 laporan di `/inventory/reports/*` | Laporan persediaan | baca. **Tidak ada di menu**: dibuka dari tautan "Laporan persediaan" di Ringkasan |

### 2.9 Banquet (`banquet`, modul BANQUET)

| Route | Menu / judul | Izin |
|---|---|---|
| `/banquet` | Kalender (judul: "Kalender banquet") | `banquet.manage` / `confirm` / `read` |
| `/banquet/events`, `/banquet/events/:id` | Acara | manage / confirm / read |
| `/banquet/today` | Acara hari ini | manage / confirm / read |
| `/banquet/unposted` | Sesi belum diposting | manage / confirm |
| `/banquet/reports` dan 6 sub-laporan | Laporan banquet | manage / confirm / read |
| `/banquet/rooms` | Ruang pertemuan | setup / manage / confirm / read (ubah: setup) |
| `/banquet/catalog` | Item dan paket | sama |
| `/banquet/settings` | Pengaturan banquet | `banquet.setup` |

### 2.10 Akuntansi, Utang usaha, Pajak, Bank, Anggaran

Route di kelompok ini tidak punya `meta.permission`. Menu memakai izin di bawah, dan halaman menampilkan teks "tidak punya akses" kalau izin itu tidak ada.

| Bagian | Item menu (route) | Izin baca |
|---|---|---|
| Akuntansi | Bagan akun, Akun sistem, Jurnal, Periode akuntansi, Tahun buku, Departemen, Neraca saldo, Buku besar, Laporan laba rugi, Laporan departemen, Neraca, Laporan arus kas, Akun kontrol (`/accounting/*`) | `accounting.view` |
| Utang usaha | Pemasok, Tagihan pemasok, Nota kredit pemasok, Pembayaran pemasok, Umur utang usaha (`/payables/*`) | `payables.view` |
| Pajak | Status PKP, Profil pelaporan pajak, SPT Masa PPN, Faktur pajak, Pajak terutang (`/tax/*`) | `tax.view` |
| Bank | Rekening bank, Mutasi bank (+`/bank/statements/:id` Rekonsiliasi), Settlement kartu (`/bank/*`) | `bank.view` |
| Anggaran | Anggaran (+`/budget/:id`), Anggaran terhadap realisasi (`/budget/*`) | `budget.view` |

### 2.11 Pengaturan (`setup`)

| Route | Menu / judul | Izin |
|---|---|---|
| `/setup/properties` (+`/new`) | Properti | administrator tenant |
| `/setup/properties/:id` | Properti {kode} | `property.manage`. Tidak ada di menu untuk non-admin, dan setelah disimpan pengguna dialihkan ke halaman khusus admin (lihat 9.2) |
| `/setup/users` (+form) | Pengguna | administrator tenant |
| `/setup/roles` (+form) | Peran | administrator tenant |
| `/setup/system` | Sistem | administrator tenant |
| `/setup/subscription` | Langganan | administrator tenant |
| `/setup/room-types`, `/bed-types`, `/rooms` | Tipe kamar, Tipe tempat tidur, Kamar | `room.manage` |
| `/setup/taxes`, `/charge-codes` | Pajak & biaya layanan, Kode biaya | `billing_config.manage` |
| `/setup/companies` | Perusahaan | `company.manage` |
| `/setup/rate-plans`, `/free-quotas`, `/rates`, `/bed-supplements`, `/restrictions`, `/yield-rules` | Paket tarif, Kuota malam gratis, Grid tarif, Selisih harga bed, Pembatasan penjualan, Aturan yield | `rate.manage` |
| `/setup/approval-rules` | Aturan persetujuan | `property.manage` |
| `/audit` | Jejak audit | `audit.read` (lingkup seluruh tenant hanya untuk admin) |

Pengaturan yang letaknya **di luar** menu Pengaturan:
- **Pengaturan kasir** ada sebagai kartu di **Shift kasir** (`cashier.settings`).
- **Departemen** (kepala dan wakil) ada di **Akuntansi** (`accounting.manage`).
- **Akun sistem** ada di Akuntansi.
- **Penerima paket audit malam** ada di Akhir hari.
- **Pengaturan banquet** dan **Pengaturan aset** ada di modulnya masing-masing.

### 2.12 Console platform (`/platform/*`, terpisah dari aplikasi hotel)

| Route | Judul | Catatan |
|---|---|---|
| `/platform/login` | Console platform | Email + kata sandi, lalu kode authenticator 6 digit; aktivasi pertama memakai kode QR dan kode pemulihan |
| `/platform/tenants`, `/platform/tenants/:code` | Penyewa | Buat penyewa, ubah langganan, terbitkan invoice, tangguhkan / aktifkan kembali |
| `/platform/plans` (+`/new`, `/:code`) | Paket | Modul, kuota, masa percobaan |
| `/platform/invoices` | Invoice | Catat bayar, void |

Akun operator hanya dibuat lewat `pms-admin`, tidak lewat UI.

## 3. Peran dan izin

**Temuan utama: KamaraPMS tidak punya peran bawaan.**
- Kolom `roles.is_system` ada di database, tetapi tidak pernah diisi.
- Pembuatan tenant (`pms-admin create-tenant` atau console) tidak membuat peran apa pun.
- Setiap hotel membuat perannya sendiri di **Pengaturan → Peran**, dengan memilih izin dari katalog berisi 119 izin dalam 20 grup.
- Satu pengguna mendapat satu peran per properti.
- Peran tidak bisa dihapus: tidak ada tombol dan tidak ada API-nya.

**Administrator tenant** (centang "Administrator tenant" di form pengguna) melewati semua pemeriksaan izin di semua properti. Hanya administrator yang bisa:
- mengelola pengguna, peran, dan properti baru;
- melihat Langganan dan Sistem;
- membaca jejak audit seluruh tenant.

Administrator boleh menyetujui koreksinya sendiri walaupun aturan meminta orang lain; hal itu dicatat sebagai `self_approved_by_admin`.

Karena tidak ada peran bawaan, tabel di panduan disusun sebagai **contoh peran**. Contoh di bawah diambil dari peran yang dipakai seed E2E (`web/e2e/support/state.ts`), dan menunjukkan halaman yang tampil di menu untuk setiap peran.

| Contoh peran | Izin inti | Bagian menu yang tampil |
|---|---|---|
| Resepsionis | reservation.read/create/update/cancel/reinstate/upgrade, guest.read/write, frontdesk.checkin/checkout/room_move/rate_change, folio.read, folio.post_charge, payment.post, cashier.shift, maintenance.report, lostfound.report | Front Desk (semua item); Kamar (Status kamar, Housekeeping, Daftar pembersihan, Perawatan, Barang tertinggal, Blokir kamar); Tagihan (Folio, Kasir, Shift kasir, Biaya kamar) |
| Supervisor front office | seperti Resepsionis, ditambah frontdesk.checkin_unready_room, frontdesk.reverse_checkin, folio.reverse/adjust/transfer_any, payment.void/refund, correction.approve, cashier.shift_manage/shift_approve, report.view | seperti Resepsionis, ditambah Akhir hari (Laporan, Kinerja) |
| Kasir | reservation.read, guest.read, folio.read, folio.post_charge, payment.post, payment.refund, cashier.shift, report.view | Front Desk (kecuali menu Tamu kalau tanpa guest.read); Tagihan; Laporan, Kinerja |
| Housekeeping | housekeeping.update, housekeeping.inspect, maintenance.report, lostfound.report | Hari Ini; Kamar (Housekeeping, Tugas saya, Daftar pembersihan, Perawatan, Barang tertinggal, Blokir kamar). **Tanpa Status kamar** |
| Supervisor housekeeping | seperti Housekeeping, ditambah housekeeping.assign, maintenance.manage, room_block.manage, lostfound.manage | sama, dengan tombol menugaskan, menangani perawatan, dan membuat blokir |
| Akuntansi | accounting.view/post/manage, payables.view/manage/post, bank.view/manage/reconcile, tax.view, budget.view/manage, report.view | Akuntansi, Utang usaha, Pajak, Bank, Anggaran, Laporan |
| Night auditor (usulan; tidak ada di seed) | nightaudit.run, nightaudit.no_show, nightaudit.pack_view, report.view, ditambah izin front desk | Akhir hari |
| Kasir outlet / pelayan | pos.order, pos.cashier, pos.charge_room, pos.kds, pos.report | POS (Denah meja dan bill, Pelayan, Layar dapur, Laporan outlet) |
| Manajer outlet | pos.manage, pos.approve, pos.discount, pos.comp, pos.void | POS (semua item) |
| Penyetuju persediaan (4 tingkat) | inventory.view ditambah salah satu dari dept_approve / approve / cost_control / final_approve / sr_final_approve | Persediaan (Perlu saya setujui, dan lainnya) |
| Sales banquet / manajer banquet | banquet.read/manage, ditambah banquet.confirm dan banquet.setup | Banquet |
| Staf aset | asset.read/manage/depreciate/dispose/setup | Aset tetap |

Catatan untuk tabel peran:
- Izin modul Persediaan, Banquet, dan Aset tidak diberikan ke peran mana pun oleh migrasi.
- `folio.master_manage`, `folio.master_close`, dan `group.cancel` diberikan otomatis (sekali) ke peran yang sudah memegang `correction.approve` saat migrasinya berjalan.
- Item **Hari Ini**, **Housekeeping**, **Daftar pembersihan**, dan **Blokir kamar** tampil untuk semua anggota properti. Halamannya sendiri yang menentukan tombol apa yang tersedia.

## 4. Alur bisnis utama

### 4.1 Reservasi sampai check-out

1. **Reservasi baru**:
   1. Isi Datang, Pulang, dan Dewasa, lalu klik **Cari**.
   2. Klik **Pesan** pada satu baris.
   3. Pilih **Pemesan**. Hanya bisa mencari tamu yang sudah ada; tamu baru dibuat di menu Tamu, di panel check-in, atau di Walk-in.
   4. Isi Sumber, permintaan bed, alasan (wajib untuk paket komplimen atau pemakaian hotel), dan tarif khusus bila perlu.
   5. Klik **Pesan dan konfirmasi** atau **Simpan draf**.
   6. Persetujuan muncul untuk tiga hal: tarif diubah (RATE_OVERRIDE), kamar gratis (FREE_ROOM), dan pengesampingan pembatasan penjualan (SALES_RESTRICTION).
2. **Deposit**: di detail reservasi, klik **Terima deposit** (Tunai / Kartu / Transfer bank / Lainnya / QRIS). Deposit tunai butuh shift kasir terbuka.
3. **Menetapkan kamar**: dengan **Tetapkan kamar**, **Tetapkan otomatis**, atau dari Bagan kamar. Kamar dari tipe lain dihitung upgrade dan butuh `reservation.upgrade`.
4. **Check-in** di Tamu hari ini → Kedatangan → **Check-in**. Panelnya punya empat langkah: Tamu → Kamar → Deposit → Selesai.
   - Kamar yang belum siap hanya bisa dipakai dengan **Tetap check-in** dan alasan, dan butuh `frontdesk.checkin_unready_room`.
   - Deposit tidak pernah diwajibkan oleh sistem.
   - Langkah Selesai menyediakan kartu registrasi, kuitansi, dan e-mail konfirmasi.
5. **Walk-in** membuat reservasi, mengonfirmasi, dan langsung check-in dalam satu langkah. Tidak ada langkah deposit.
6. **Selama menginap** (detail menginap / StayActions):
   - **Pindah kamar** (alasan wajib; kamar lama menjadi Kotor);
   - **Perpanjang / persingkat**;
   - **Tambah tamu**;
   - **Ubah tarif** (persetujuan STAY_RATE, penyetuju memegang `correction.approve`).
7. **Posting charge**: Folio → tab **Biaya**. Kode biaya kamar tidak bisa diposting manual.
8. **Check-out**: klik **Check-out** atau **Bayar & check-out**.
   - Pulang lebih awal harus dikonfirmasi.
   - Biaya kamar yang belum diposting ikut diposting saat check-out.
   - Semua folio menginap harus bersaldo nol.
   - Setelah check-out, kamar menjadi Kotor.
9. **Batalkan check-in**: hanya pada hari yang sama, sebelum pindah kamar, dan selama belum ada biaya di folio. Alasan wajib.
10. **Batal / tidak datang**:
    - Batal butuh alasan.
    - Biaya pembatalan atau no-show tidak dihitung otomatis. Petugas memasukkannya sendiri dengan **Posting biaya pembatalan** atau **Posting biaya no-show**.
    - **Aktifkan kembali** hanya memulihkan kamar yang dibatalkan bersama reservasinya.
11. **Audit malam**: lihat 4.10.

### 4.2 Status kamar dan tugas housekeeping

- **Status kebersihan:** Kotor → Dibersihkan → Bersih → Sudah diperiksa. Untuk Sudah diperiksa butuh `housekeeping.inspect`.
- **Perubahan yang diizinkan:** DIRTY ke CLEANING atau CLEAN; CLEANING ke CLEAN atau DIRTY; CLEAN ke INSPECTED atau DIRTY; INSPECTED ke DIRTY.
- **Perubahan otomatis ke Kotor:** setelah check-out, pindah kamar, dan pembatalan check-in. Audit malam juga menandai kamar terisi sebagai Kotor, kalau pengaturan properti menyala.
- **Hunian** (Terisi / Dipesan / Kosong / Diblokir) dihitung oleh sistem, bukan diisi petugas.
- **Daftar pembersihan:**
  1. Supervisor klik **Buat daftar hari ini** (tugas Check-out, Menginap lanjut, Kedatangan, Kamar kotor), lalu menugaskannya ke petugas.
  2. Petugas mengerjakannya di **Tugas saya**: **Mulai bersihkan**, lalu **Selesai: siap diperiksa** (kamar menjadi Bersih).
  3. **Lewati** butuh alasan.
- **Ubah massal** di Status kamar: paling banyak 100 kamar sekali ubah. Konfirmasi diminta kalau lebih dari 10 kamar, atau kalau kamar yang sudah diperiksa kembali ke Kotor.
- **Blokir kamar:** Rusak (OOO) atau Tidak dilayani (OOS), dengan alasan wajib. Melepas blokir juga butuh alasan.
- **Perawatan:** Laporkan → Mulai dikerjakan → Selesaikan atau Batalkan permintaan → Buka kembali. Dari permintaan perawatan, kamar bisa langsung diblokir.
- **Barang tertinggal:** Catat barang → Serahkan atau Musnahkan.

### 4.3 Folio, pembayaran, dan koreksi

- **Folio hanya bisa ditambah (append-only).** Tidak ada baris yang dihapus. Koreksinya:
  - **Reverse**: hanya untuk biaya pada tanggal bisnis berjalan;
  - **Penyesuaian**: untuk tanggal yang lewat;
  - **Void** pembayaran: hanya pada hari yang sama;
  - **Refund**: kapan saja, sebagian atau seluruhnya.

  Semuanya butuh alasan dan persetujuan (email dan kata sandi penyetuju yang memegang `correction.approve`).
- **Grup A–D** (transaction group) hanya membagi tampilan tagihan saat dicetak. Saldo selalu saldo seluruh folio.
- **Transfer:** satu baris dipindahkan ke folio lain dengan **Pindah**, banyak baris atau saldo dengan **Transfer…** / **Transfer saldo…**. Keduanya dengan persetujuan. Baris yang ditransfer bisa dikembalikan dengan **Kembalikan**.
- **Transfer ke piutang perusahaan:** tab **Transfer**, tanpa persetujuan. Batas kredit perusahaan berlaku.

### 4.4 Shift kasir

1. **Buka shift**: pilih Laci dan isi Modal awal.
2. Pembayaran, refund, dan penerimaan perusahaan secara tunai tercatat di shift itu.
3. **Pindahkan kas**: Setor ke brankas, Kas masuk, atau Kas keluar.
4. **Laporan X** bisa dicetak kapan saja.
5. **Tutup shift…**: isi kas yang dihitung. Selisih di atas batas butuh alasan dan persetujuan (`cashier.shift_approve`).
6. Laci bisa diserahkan ke kasir lain.
7. Setelah ditutup, cetak **Laporan Z**.

Shift yang masih terbuka menahan audit malam kalau pengaturan kasir menyala (bawaan: menyala).

### 4.5 POS

1. **Penyiapan** (`pos.manage`), berurutan: departemen dan kode biaya → Kategori, harga, profil → Outlet → Stasiun dan printer (termasuk mendaftarkan agen cetak) → Produk dan menu → Denah meja.
2. **Order**:
   1. Buka bill dari denah meja, atau dari **Bawa pulang / bill tanpa meja**.
   2. Tambahkan item.
   3. Klik **Kirim ke dapur dan bar**; tiket muncul di Layar dapur (Baru → Dimasak → Siap diambil).
3. **Bayar** (`pos.cashier`): metodenya Tunai, Kartu, QRIS, Transfer bank, Lainnya, **Bebankan ke kamar**, dan **Perusahaan**.
   - Bill bisa dibayar sebagian, dipisah, atau digabung.
   - Tunai butuh shift kasir terbuka dan outlet yang punya laci uang.
4. **Bebankan ke kamar** hanya bisa kalau folio tamu sudah **dibuka untuk outlet**: resepsionis mengatur batas dan masa berlakunya di kartu "Beban dari outlet" pada folio, atau saat check-in. Penolakannya tercatat di **Beban outlet hari ini**.
5. **Void** item yang sudah dikirim, **Diskon** di atas batas, **Komplimen**, dan **Buka kembali** bill butuh persetujuan (`pos.approve`).
6. **Pengaruh ke audit malam:** bill terbuka dengan item terjual hanya menjadi peringatan, kecuali outlet mengatur "Bill terbuka menghambat audit malam". Baris yang belum dikirim akan tercatat di tanggal besok.

### 4.6 Banquet

1. **Penyiapan** (`banquet.setup`): ruang dan ruang gabungan, item dan paket, pengaturan, template kebijakan, jenis acara, alasan, dan pembaca BEO.
2. **Status acara:**
   1. **Pemesanan baru** (Inquiry). Sesi boleh belum punya ruang.
   2. **Tahan sebagai tentatif**: tanggal opsi wajib, dan ruang ditahan sebagai opsi.
   3. **Konfirmasi** (Definit; butuh `banquet.confirm`): folio acara dibuka dan BEO menjadi revisi 1.
   4. Selesai dengan **Selesaikan acara**, **Tandai batal** (hilang), atau **Batalkan acara** (dengan biaya pembatalan).
3. **Uang:**
   - **Terima DP** (deposit) masuk ke folio acara.
   - Item atau paket per sesi; diskon di atas batas butuh persetujuan.
   - **Posting sesi** dilakukan otomatis saat tutup hari atau manual. Pax yang ditagih adalah yang lebih besar antara pax garansi dan pax aktual.
   - Tagihan bisa di-**Transfer ke folio grup** (master folio grup).
4. **Penagihan:** lewat folio acara (pembayaran biasa) atau master folio grup, lalu ke piutang perusahaan kalau grupnya ditagih ke perusahaan.

### 4.7 Perusahaan, grup, dan piutang perusahaan

1. **Perusahaan:** isi termin dan batas kredit. Perusahaan yang masih bersaldo tidak bisa dinonaktifkan.
2. **Grup:**
   - **Buka master folio**, lalu atur apa yang masuk ke master: semua biaya kamar, biaya tanpa folio, atau kode tertentu. Kamar tertentu bisa dikecualikan.
   - Biaya yang sudah diposting dipindahkan dengan **Pindahkan ke master folio…** (dengan persetujuan).
   - Tersedia juga Deposit grup, Batalkan kamar grup (`group.cancel`), dan **Tutup master folio**.
3. **Piutang perusahaan:**
   1. Transfer dari folio.
   2. **Invoice baru**, hanya untuk tamu yang sudah check-out.
   3. **Catat pembayaran dari perusahaan**.
   4. **Nota kredit…** atau **Penghapusan piutang…**, dengan persetujuan.
   5. **Kirim surat peringatan…** (tingkat 1–3) dan atur denda keterlambatan. Denda hanya ditampilkan, tidak pernah diposting.
   6. **Rekening koran** perusahaan.

### 4.8 Persediaan

1. **Penyiapan:**
   1. Harga pokok: Rata-rata bergerak atau FIFO. Metode terkunci setelah mutasi stok pertama.
   2. Satuan.
   3. Kategori & akun (akun persediaan wajib).
   4. Gudang.
   5. Barang.
   6. Saldo awal. Ditolak kalau akun persediaan di buku besar sudah bersaldo.
   7. Akun sistem persediaan.
   8. Kepala dan wakil departemen (di Akuntansi → Departemen).
   9. Rantai persetujuan.
   10. Izin di peran.
2. **Pembelian:**
   - Permintaan pembelian (PR): Draf → Menunggu persetujuan → Disetujui → Sudah dipesan. Pesanan (PO) dibuat dari PR yang disetujui.
   - Penerimaan barang, yang bisa sekaligus ditagih.
   - Draf tagihan dengan pencocokan tiga arah. Selisih harga di atas toleransi butuh persetujuan.
   - Retur ke pemasok.
   - Dengan pengaturan bawaan (tidak bisa diubah di UI), PO hanya bisa dibuat dari PR, dan penerimaan tanpa pesanan dimatikan.
3. **Rantai persetujuan PR:** tingkat 1 Kepala departemen → 2 Cost control → 3 Persetujuan akhir, kalau total di atas 5.000.000.
   - Setiap tingkat diputuskan orang yang berbeda.
   - Tidak seorang pun menyetujui dokumennya sendiri, termasuk administrator.
   - Kalau satu tingkat tidak punya penyetuju, pengajuan ditolak (`APPROVAL_CHAIN_UNSATISFIABLE`), dan draf menampilkan peringatan sebelum diajukan.
4. **Pergerakan:**
   - Permintaan barang gudang (SR), dijawab dengan transfer.
   - Transfer bebas.
   - Pemakaian, waste, makan karyawan, dan entertainment.
   - Pembalikan hanya bisa pada hari posting.
5. **Resep** diatur di POS → Produk dan menu (kartu "Resep & biaya bahan"). Penjualan POS memotong stok sesuai resep.
6. **Opname:**
   1. **Mulai dan bekukan** gudang.
   2. Hitung. Hitungan buta: jumlah buku tidak ditampilkan.
   3. **Kirim untuk disetujui**.
   4. **Setujui opname** (`inventory.approve`) atau **Batalkan opname**.

   Opname yang terbuka atau menunggu persetujuan **menahan audit malam**. Pesannya: "Opname {number} di {warehouse} masih terbuka dan membekukan gudang. Setujui atau batalkan sebelum hari ditutup."
7. **Amenities otomatis** dibuat setiap tutup hari.
8. **Laporan:** 23 laporan dalam lima kelompok (Stok, Pembelian, Biaya F&B, Pemakaian, Kontrol).

### 4.9 Aset tetap

1. **Register:**
   - Aset dibuat dari **Daftar tunggu**, yaitu baris tagihan, kasir, bank, atau jurnal yang mungkin aset: **Jadikan aset** atau **Bukan aset**.
   - Aset lama masuk lewat **Saldo awal aset** (impor CSV).
2. **Mutasi:** **Pindahkan** lokasi atau departemen (tanpa jurnal); **Ubah umur / nilai sisa / metode**; **Tambah nilai**; **Ganti kategori** atau **Selesai dibangun**.
3. **Penyusutan bulanan** berjalan berurutan per bulan. **Balik penyusutan** hanya bisa untuk run terakhir di bulan yang masih terbuka.
4. **Pelepasan** (Dijual, atau Dihapus karena rusak / hilang / usang / dihibahkan). Aturannya di **Aturan persetujuan → ASSET_DISPOSAL**:
   - Bawaan "Siapa pun yang boleh menyetujui": tombolnya **Setujui sekarang** dan jurnal langsung diposting.
   - "Selalu orang lain", atau "Orang lain di atas batas" ketika nilainya di atas batas: tombolnya **Ajukan pelepasan**. Pengaju memegang `asset.dispose`; penyetuju adalah orang lain yang juga memegang `asset.dispose`, dan memutuskan di **Pelepasan aset → Menunggu persetujuan** (**Setujui** / **Tolak**).
   - **Balik pelepasan** selalu meminta email dan kata sandi penyetuju.
5. **Opname aset:** pindai QR atau ketik kode, lalu **Tutup opname**. Aset yang tidak terpindai menjadi Hilang, dan sistem membuat usulan yang bisa **Terapkan** atau **Tolak**.
6. **Permintaan perbaikan:** formulir Perawatan punya pilihan **Aset**, dan riwayatnya tampil di tab **Perbaikan** pada halaman aset.

### 4.10 Audit malam (night audit)

Halaman **Audit malam** menampilkan langkah-langkah berikut. Setiap langkah bisa menjadi penghalang:

1. **Waktu.** Hari boleh ditutup setelah "Jam paling awal audit malam" (bawaan 20:00) pada tanggal bisnis, atau setelah tanggal lokal properti melewatinya.
2. **Kedatangan yang belum selesai:** check-in, atau **Tandai {n} sebagai no-show** (butuh `nightaudit.no_show` dan centang konfirmasi).
3. **Keberangkatan yang belum selesai:** check-out, atau perpanjang.
4. **Biaya kamar:** malam yang tidak bisa dibebankan, atau malam terposting yang seharusnya tidak ada.
5. **Shift kasir** yang masih terbuka (kalau pengaturannya menyala).

Penghalang lain muncul sebagai temuan:
- bill outlet terbuka (kalau outlet mengaturnya);
- temuan persediaan, termasuk opname terbuka;
- akuntansi belum siap, misalnya departemen wajib yang tidak ada.

Ada juga daftar **Peringatan** yang tidak menghentikan audit: draf lama, folio reservasi batal yang masih bersaldo, master folio, blokir yang berakhir, sesi banquet, stok minus, paket audit gagal.

**Jalankan:** centang "Tutup {date} dan buka tanggal bisnis berikutnya", lalu klik **Jalankan audit malam**. Semua langkah berikut terjadi dalam satu kali proses; kalau satu gagal, semuanya dibatalkan:
1. kamar terisi ditandai Kotor;
2. biaya kamar diposting;
3. sesi banquet diposting;
4. stok ditutup;
5. jurnal tutup hari dibuat;
6. hari menjadi **CLOSED** dan hari berikutnya dibuka;
7. paket audit malam dibangun dan dikirim lewat e-mail.

Setelah itu, posting ke hari yang sudah ditutup ditolak ("Hari bisnis sudah ditutup.").

### 4.11 Akuntansi dan back office

- **Jurnal manual:** harus seimbang, dan tidak boleh ke akun kontrol. Pembaliknya (**Balik…**) butuh persetujuan.
- **Jurnalkan hari yang hilang:** membuat jurnal tutup hari untuk hari yang terlewat (`accounting.close`).
- **Periode akuntansi:**
  - **Tutup** butuh bulan sebelumnya tertutup, bulannya sudah berakhir, dan semua harinya sudah dijurnal. Setelah ditutup, tidak ada posting yang bisa masuk ke bulan itu.
  - **Buka kembali** hanya untuk bulan tertutup terakhir, dengan alasan, **tanpa persetujuan**. Penyusutan aset bulan itu ikut dibalik.
- **Tahun buku:** **Tutup tahun** setelah 12 bulan ditutup, dan sistem memposting jurnal penutup ke laba ditahan. Membuka kembali butuh persetujuan.
- **Utang usaha:** Pemasok → Catat tagihan → Bayar pemasok. Pembatalannya butuh persetujuan. Nota kredit pemasok diterapkan otomatis ke tagihannya.
- **Pajak:** Status PKP → Profil pelaporan → SPT Masa PPN (Laporkan SPT, Catat pembayaran) → Faktur pajak (terbitkan dari folio tertutup atau invoice perusahaan, catat nomor resmi, ekspor CSV e-Faktur).
- **Bank:** Rekening bank → impor rekening koran → Cocokkan otomatis atau manual → posting baris yang belum ada di buku → **Rekonsiliasi** → **Buka kembali…** (dengan persetujuan). Settlement kartu dibuat dari baris uang masuk; settlement tidak bisa dibatalkan.
- **Anggaran:** Anggaran baru → grid / Sebarkan / Isi dari realisasi / Impor CSV → **Aktifkan** (persetujuan, penyetuju memegang `budget.approve`) → Anggaran terhadap realisasi.

## 5. Integrasi yang terlihat oleh pengguna

| Integrasi | Ada di UI? | Keterangan |
|---|---|---|
| Kunci pintu (TTLock / kartu kunci) | **Tidak ada** | Tidak ada vendor kunci di kode. Bagian ini tidak ditulis |
| Agen cetak POS | Ya | **Stasiun dan printer → Perangkat**: Buat kode pendaftaran, Online/Offline, Ganti token, Cabut. Pemasangan agennya tugas IT (`docs/pos-agent.md`) |
| Dokumen PDF | Ya | Tagihan atau invoice folio (per grup), kuitansi, kartu registrasi, konfirmasi reservasi, rekening koran dan invoice perusahaan, nota kredit, surat peringatan, laporan shift X dan Z, laporan harian, laporan akuntansi, faktur pajak, label QR aset, proposal, kontrak, dan BEO banquet. Bahasanya mengikuti bahasa layar; **paket audit malam selalu berbahasa Inggris** |
| E-mail | Ya (kalau server e-mail diatur) | Konfirmasi reservasi (kirim ulang), proposal dan kontrak banquet, paket audit malam. **Tidak ada** e-mail tagihan folio atau invoice perusahaan. Kalau server e-mail belum diatur, layar menampilkan "E-mail belum diatur di server ini…" |
| Paket laporan audit malam | Ya | Penerima (pilih bagian; bagian yang berisi nama tamu dan kas diberi tanda), arsip, bangun ulang, kirim ulang. Ada 15 bagian |
| QRIS | Sebagai metode bayar saja | Tidak ada pembuatan kode QR atau payment gateway |
| Channel manager, payment gateway, WhatsApp, SMS | Tidak ada | |

## 6. Master data sebelum hotel beroperasi (urutan)

**Dibuat otomatis saat properti dibuat:**
- hari bisnis pertama (OPEN) dan nomor dokumen;
- 18 kode biaya, semuanya Eksklusif dan **belum punya pajak atau servis**;
- bagan akun (199 akun) dan peta akun sistem;
- 8 departemen;
- 5 tipe tempat tidur;
- kategori aset;
- pengaturan pajak (belum PKP);
- pengaturan kasir.

Nama-nama bawaan ini ditulis dalam "Bahasa nama bawaan" yang dipilih saat properti dibuat.

**Tidak dibuat otomatis:** pajak dan biaya servis, tipe kamar, kamar, paket tarif, tarif, peran, pengguna selain admin, dan perusahaan.

Urutan yang disarankan:

1. Administrator tenant dibuat oleh operator (console platform atau `pms-admin`).
2. **Pengaturan → Properti → Properti baru**. Hal-hal yang terkunci setelah transaksi pertama harus benar sejak awal:
   - Mata uang dan desimal;
   - Pelepasan deposit;
   - Tanggal bisnis pembukaan (hanya hari ini atau kemarin).

   Atur juga jam check-in/out, jam paling awal audit malam, wajib inspeksi, deposit insidental, dan metode refund.
3. **Peran**, lalu **Pengguna**, dengan satu peran per properti.
4. **Pajak & biaya layanan** (contohnya PB1/PBJT 10% dan servis 10%).
5. **Kode biaya**: pasang aturan servis dan pajak secara berurutan, periksa akun pendapatan dan departemen, dan **putuskan Inklusif atau Eksklusif untuk kode Kamar**. Mode ini terkunci begitu paket tarif memakainya.
6. **Tipe kamar** → (periksa **Tipe tempat tidur**) → **Kamar**. Kamar butuh tipe kamar dan tipe tempat tidur, dan kamar aktif dihitung dalam kuota paket langganan.
7. **Paket tarif**, yang butuh kode biaya jenis Kamar yang aktif.
8. **Grid tarif**. Opsional: Selisih harga bed, Pembatasan penjualan, Aturan yield, Kuota malam gratis.
9. Opsional sesuai kebutuhan:
   - Aturan persetujuan;
   - Pengaturan kasir (di Shift kasir);
   - Perusahaan;
   - Penerima paket audit malam;
   - Status PKP dan profil pajak;
   - Akun sistem;
   - kepala departemen;
   - modul POS, Banquet, Persediaan, dan Aset sesuai urutan masing-masing (4.5, 4.6, 4.8, 4.9).

## 7. Aturan bisnis penting yang memengaruhi pengguna

- **Tanggal bisnis bukan tanggal kalender.**
  - Semua transaksi dicatat pada hari bisnis yang OPEN.
  - Hari itu tetap OPEN sampai audit malam dijalankan, walaupun jam sudah lewat tengah malam.
  - Kalau tanggal lokal berbeda, chip di bilah atas menampilkan "Tanggal bisnis {date} · lokal {date time}".
  - Hari yang sudah CLOSED tidak menerima posting (`BUSINESS_DAY_CLOSED`).
  - Kalau tanggal berganti saat layar masih terbuka: "Tanggal bisnis sudah berganti: muat ulang lalu coba lagi."
- **Koreksi, bukan hapus:**
  - folio append-only (reverse, penyesuaian, void, refund);
  - jurnal manual dikoreksi dengan pembalikan;
  - dokumen persediaan dikoreksi dengan pembalikan pada hari posting;
  - bill POS dikoreksi dengan void atau buka kembali.
- **Persetujuan:**
  - Dialog bersama meminta email dan kata sandi penyetuju.
  - Aturan per jenis koreksi diatur di **Aturan persetujuan** (27 jenis): "Siapa pun yang boleh menyetujui" (bawaan), "Selalu orang lain", atau "Orang lain di atas batas".
  - Kata sandi yang salah dihitung sebagai percobaan login gagal.
  - Persediaan (PR/SR) memakai rantai bertingkat sendiri.
- **Waktu terkunci:**
  - mata uang, desimal, dan pelepasan deposit sejak transaksi pertama;
  - mode harga kode Kamar sejak dipakai paket tarif;
  - metode harga pokok persediaan sejak mutasi pertama;
  - periode akuntansi yang sudah ditutup;
  - tahun buku yang sudah ditutup;
  - rekening koran yang sudah direkonsiliasi.
- **Langganan:**
  - Masa tenggang (GRACE) menampilkan banner.
  - Status hanya baca (READ_ONLY) menolak setiap perubahan dengan "Langganan sudah berakhir: data bisa dibaca tetapi tidak bisa diubah." Tombol-tombol tetap aktif, tetapi server menolaknya.
- **Pesan izin:**
  - `PERMISSION_DENIED` "Peran Anda tidak mengizinkan tindakan ini.";
  - `PROPERTY_NOT_FOUND` "Properti tidak ditemukan atau tidak bisa diakses.", juga untuk properti yang tidak ditugaskan ke pengguna;
  - halaman yang butuh izin khusus mengembalikan pengguna ke Hari Ini tanpa pesan.
- **Katalog pesan error:**
  - 667 kode `errors`, 98 `fieldErrors`, dan 21 `errorActions` (langkah berikutnya) di `id.ts`;
  - error yang terkait isian tampil sebagai banner merah di atas form; error lain tampil sebagai toast selama 10 detik;
  - kalau kode tidak punya teks Indonesia, pesan bahasa Inggris dari server yang tampil.

## 8. Daftar isi yang disetujui

Disetujui pemilik pada 24 Nov 2026. Nama file di bawah ini mengikat: halaman ditulis dengan nama persis seperti ini, dan tautan memakai nama yang sama.

| Folder | Halaman (`.md`) |
|---|---|
| `00-memulai/` | login-dan-navigasi, mengganti-bahasa-dan-tampilan, mengubah-kata-sandi, setup-awal-hotel, memahami-tanggal-bisnis, peran-dan-hak-akses, meminta-persetujuan |
| `01-front-desk/` | membuat-reservasi, mengonfirmasi-dan-mengubah-reservasi, menetapkan-kamar, menerima-deposit, membatalkan-reservasi-dan-no-show, check-in, walk-in, pindah-kamar, perpanjang-atau-persingkat, mengubah-tarif-tamu-menginap, check-out, membatalkan-check-in, mengelola-data-tamu, bagan-kamar-dan-ketersediaan |
| `02-housekeeping/` | status-kamar, mengubah-status-kebersihan, daftar-pembersihan-dan-tugas-saya, blokir-kamar-ooo-oos, laporan-perawatan, barang-tertinggal |
| `03-kasir-dan-pembayaran/` | posting-biaya-ke-folio, menerima-pembayaran, reverse-dan-penyesuaian, void-dan-refund, transfer-antar-folio, grup-tagihan-dan-cetak, shift-kasir, posting-biaya-kamar, membuka-folio-untuk-outlet |
| `04-pos/` | menyiapkan-outlet, mengambil-pesanan, layar-dapur, membayar-bill, bebankan-ke-kamar, void-diskon-komplimen, laporan-outlet |
| `05-banquet/` | menyiapkan-banquet, membuat-acara-dan-sesi, tentatif-dan-konfirmasi, item-paket-dan-deposit, posting-sesi-dan-penagihan, beo-dan-dokumen, membatalkan-acara |
| `06-corporate-group-city-ledger/` | perusahaan, grup-dan-master-folio, transfer-ke-piutang-perusahaan, invoice-dan-penerimaan, nota-kredit-dan-penghapusan-piutang, surat-peringatan |
| `07-accounting/` | bagan-akun-dan-akun-sistem, jurnal-manual-dan-pembalikan, periode-dan-tahun-buku, departemen, laporan-keuangan |
| `08-keuangan-back-office/` | utang-usaha, pajak-dan-faktur, rekonsiliasi-bank, settlement-kartu, anggaran |
| `09-inventory/` | menyiapkan-persediaan, barang-gudang-dan-saldo-awal, permintaan-dan-pesanan-pembelian, penerimaan-tagihan-dan-retur, rantai-persetujuan, permintaan-barang-dan-transfer, pemakaian-dan-waste, opname, resep, amenities-otomatis, laporan-persediaan |
| `10-aset-tetap/` | daftar-tunggu-dan-register, saldo-awal-aset, memindahkan-dan-mengubah-aset, penyusutan-bulanan, pelepasan-aset, opname-aset-dan-label-qr, laporan-aset |
| `11-audit-malam/` | menjalankan-audit-malam, menangani-penghalang, menandai-no-show, paket-audit-malam |
| `12-laporan/` | laporan-front-office, laporan-harian, kinerja-dan-dasbor, jejak-audit |
| `13-pengaturan/` | properti, pengguna, peran, tipe-kamar-dan-kamar, pajak-dan-kode-biaya, paket-tarif-dan-grid-tarif, pembatasan-yield-dan-kuota, aturan-persetujuan, langganan-dan-sistem |
| `14-admin-platform/` | console-platform |
| `panduan-cepat/` | resepsionis, supervisor-fo, kasir, housekeeping, supervisor-housekeeping, night-auditor, kasir-outlet, staf-banquet, akuntan, staf-gudang, manajer, administrator |
| Akar | README, alur-harian, faq, troubleshooting, glosarium, _laporan, screenshots/ |

`mengubah-kata-sandi` ditambahkan di Batch 1 (halaman pendek, tidak ada di usulan awal).

### Tautan menunggu halaman

Tautan dari halaman yang sudah ditulis ke halaman yang belum ada. Halaman yang ditulis di batch berikutnya memakai nama persis seperti di kolom pertama. Setiap batch, baris yang halamannya sudah dibuat dihapus. Di Fase 6 daftar ini harus kosong.

<!-- PENDING-LINKS:START -->
| Halaman tujuan (belum ada) | Ditautkan dari |
|---|---|
| (kosong) | |
<!-- PENDING-LINKS:END -->

## 9. Temuan untuk `_laporan.md`

### 9.1 Teks yang tampil dalam bahasa Inggris atau sebagai kode mentah

**Izin dan peran**
- 20 izin tidak punya `permissionLabel` Indonesia: semua `asset.*`, semua `banquet.*`, dan 11 izin `inventory.*` (request, purchase, receive, bill, approve, cost_control, final_approve, move, count, dept_approve, sr_final_approve).
- Grup izin "Inventory", "Fixed assets", dan "Banquet" tampil dalam bahasa Inggris di form Peran.

**Error dan temuan tanpa teks Indonesia**
- `errors`: TOO_MANY_ITEMS, HOLD_NOT_FOUND, LINE_NOT_FOUND, REQUEST_NOT_FOUND, PO_NOT_FOUND, BILL_DRAFT_NOT_FOUND, RETURN_NOT_FOUND, APPROVAL_CHAIN_NOT_FOUND, DISPOSAL_NOT_FOUND, DISPOSAL_REQUEST_NOT_FOUND, DEPRECIATION_RUN_NOT_FOUND, DISMISSAL_NOT_FOUND, FISCAL_GROUP_NOT_FOUND.
- `fieldErrors`: OPTION_GROUP_LIMIT, WEAK_PASSWORD, INVALID_GRANT, UNKNOWN_PERMISSION, TOO_SMALL, EXCEEDS_RECEIPT, EXCEEDS_REQUEST, CATEGORY_ACCOUNT_MISSING. ROOM_BED_MISMATCH dan BED_LOCK_NEEDS_BED_TYPE belum dipastikan.
- `findings`: BANQUET_SESSION_NOT_POSTED, BANQUET_CHARGE_CODE_INACTIVE, BANQUET_SESSION_NO_GUESTS, COSTING_METHOD_NOT_CHOSEN.
- Nama jenis jurnal: `journals.t_RECEIVABLES`, `t_CASHIER`, `t_ASSET` (tidak ada di en maupun id, jadi kuncinya tampil mentah). Baris kontrol persediaan di Akun kontrol juga tidak punya label.

**Kode mentah dan teks server bahasa Inggris di layar**
- Kode alasan biaya kamar di Biaya kamar dan Audit malam.
- Pesan "akuntansi belum siap" di Audit malam.
- Penghalang lembar kerja SPT dan penghalang rekonsiliasi bank.
- Baris galat isian di banner keuangan (`field: message`).
- Hasil check-out (DIRTY, CLOSED).
- Sumber reservasi (PHONE, EMAIL, WALK_IN).
- Jenis blokir "OOO sampai …".
- Alasan duplikat tamu ("same email").
- Kolom peran di riwayat tamu.
- Toast cadangan "Something went wrong. Try again.".

**Teks dengan arti keliru**
- `errors.CORRECTION_REQUIRES_ADJUSTMENT` berbicara tentang "tanda terima", padahal juga muncul saat me-reverse biaya tanggal lampau atau me-void pembayaran tanggal lampau.
- `ROOM_BLOCK_CONFLICT` dipakai untuk dua arti, padahal teksnya hanya menyebut blokir yang tumpang tindih.
- `RECEIPT_NOT_FOUND` diterjemahkan dalam arti kasir.
- `BILL_HAS_PAYMENTS` saat menggabung bill POS.

**String yang sama dengan bahasa Inggris** (`scratchpad/i18n-same`: 312 dari 10.296, sebagian besar kata serapan yang disengaja). Yang tampak belum diterjemahkan: "Daily flash report", "Control balance", "Out of service", "Average room rate (ARR)", "Entertainment", "Printer offline", "Business center".

### 9.2 Fitur setengah jadi atau tidak tersambung ke UI

**Front desk dan kamar**
- Mengubah tanggal blokir kamar ada di backend, tetapi tidak ada di layar.
- Walk-in menampilkan isian tamu baru tanpa memeriksa `guest.write`.

**Folio dan kasir**
- Folio atau master folio tidak bisa dibuka kembali, walaupun teks UI menyebut "dibuka kembali".
- `folio.post_after_checkout` tidak dipakai di mana pun.
- Penghitungan kas per pecahan hanya ada di backend. (Menutup shift kasir lain kini ada di layar: KD-21.)
- Tampilan baca-saja Perusahaan tidak terjangkau, karena route butuh `company.manage`. Hal yang sama berlaku untuk catatan "baca saja" di halaman Pengaturan tarif dan persetujuan.
- `/setup/properties/:id` untuk non-admin: tidak ada menunya, dan setelah disimpan pengguna dialihkan ke halaman khusus admin.

**POS**
- Mengembalikan atau memindahkan beban kamar POS.
- Menandai produk habis.
- Kamar tamu untuk bill room service.
- Komplimen sebagian bill.
- Laporan per kasir dan per pelayan.

**Banquet**
- Jam operasional banquet tidak punya isian.
- "Ubah jadwal" deposit tampil dengan `banquet.confirm`, padahal backend meminta `banquet.manage`.

**Persediaan**
- Pengaturan persediaan (toleransi, PO wajib PR, batas penerimaan tanpa pesanan, opname menahan audit, hitung buta) tidak punya layar.
- *Diperbaiki (KD-16):* rantai persetujuan terbuka untuk `property.manage` dan `inventory.manage`; hanya `property.manage` yang bisa mengubah dan menyimpan, sesuai desain 35 §5.8.
- Membuang draf tagihan tidak punya tombol.
- Lingkup opname "Daftar" tidak tersedia di UI.
- Teks "Menambah nilai … menyusul di rilis berikutnya" masih tampil, padahal fiturnya sudah ada.

**Aset tetap**
- Dialog pelepasan: dengan aturan "Selalu orang lain", tombol "Setujui sekarang" nonaktif dan tidak ada isian penyetuju.
- Usulan penghapusan dari opname aset langsung diposting kalau aturannya bawaan.

**Akuntansi dan bank**
- Bulan awal tahun buku dan tanggal mulai akuntansi tidak punya layar (belum dipastikan).
- Settlement kartu tidak bisa dibatalkan.
- Kolom nomor jurnal di Settlement kartu berjudul "Tanggal".
- Catatan ekuitas di Neraca selalu menyatakan "tidak ada jurnal penutup akhir tahun", padahal fitur tutup tahun ada.

**Lainnya**
- Akun: tidak ada daftar sesi.
- Login: tidak ada reset kata sandi mandiri.
- Peran tidak bisa dihapus.
- Console platform: peringatan sisa kode pemulihan tidak tampil.

### 9.3 Perbedaan antara `docs/architecture` dan kode

**POS** (`34-pos.md`)
- Void bill (desain: butuh persetujuan; kode: hanya draf, tanpa persetujuan).
- Tandai habis.
- Laporan per jam dan kategori.
- Ringkasan outlet di penutupan hari.
- Mode offline.

**Banquet** (`36-banquet.md`)
- Notifikasi FEE_PROPOSED tidak pernah ditulis.
- Kalender tetap 06:00–24:00.

**Aset** (`37-fixed-assets.md`)
- §18 dialog persetujuan pelepasan.
- Usulan penghapusan dari opname "selalu permintaan".

**Lainnya**
- `19-post-implementation-audit.md:215` masih menyebut biaya pembatalan dan no-show belum ada.
- `38-…` menyebut arsip "no recipients"; UI menulis "Tidak dikirim" / "E-mail mati".
- `39-…` §P1 dan §P6.
- `11-…`: anggaran memakai `year_start`, dan statistik anggaran sudah ada.
- Label `budget.approve` menyebut "sebagai orang kedua", tetapi aturan bawaan FREE mengizinkan pengaktif menyetujui sendiri.
- Tutup dan buka kembali periode tidak butuh persetujuan, berbeda dengan tahun buku dan rekening koran.

## 10. Catatan teknis untuk Fase 3 (screenshot)

- **Spec di akar `web/e2e/` tidak dijalankan.** `web/playwright.config.ts` hanya punya project `setup` (`e2e/setup/*.setup.ts`), `modules` (`e2e/modules/*.spec.ts`), dan `final` (`e2e/final/*.spec.ts`). File `web/e2e/docs-screenshots.spec.ts` tidak cocok dengan project mana pun, sehingga `scripts/e2e.sh docs-screenshots.spec.ts` akan menjawab "No tests found". Perlu keputusan.
- **`npm run type-check` tidak memeriksa `web/e2e/`.** Pemeriksanya `npm run e2e:types` (`tsc -p e2e/tsconfig.json`).
- **`.gitignore` baris 36 (`screenshots/`) mengabaikan semua folder bernama `screenshots`**, termasuk `docs/user-guide/screenshots/`. Agar screenshot panduan bisa di-commit, perlu pengecualian `!docs/user-guide/screenshots/`. Ini juga berbeda dengan kesepakatan sebelumnya bahwa screenshot tahap pembangunan (`docs/screenshots/`) tidak pernah di-commit.
- **Project `final` (audit malam) adalah teardown project `setup`,** sehingga ikut berjalan di setiap run. `E2E_SKIP_NIGHT_AUDIT=1` melewatinya.
- **Data yang sudah ada:**
  - Seed E2E sudah membuat properti `E2E` ("Hotel E2E Ubud"), 50 kamar, tarif RO, POS, persediaan (tanpa opname terbuka), banquet, aset, dan penerima paket.
  - Seed juga membuat pengguna per peran (Rina Resepsionis, Sari Supervisor, Kevin Kasir, Hana Housekeeping, Andi Akuntansi, Putu Pelayan, dan empat penyetuju persediaan).
  - `loginAs()` sudah mengatur bahasa `id`. Viewport 1440×900 sudah menjadi bawaan project `modules`.
