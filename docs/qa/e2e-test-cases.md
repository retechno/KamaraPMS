# Katalog test case E2E (Playwright)

Semua test case di bawah dijalankan otomatis oleh Playwright di browser sungguhan, melawan API dan database khusus test (`pms_e2e`). Setiap ID sama dengan awal judul test di `web/e2e/`, jadi hasil di report Playwright bisa dicocokkan langsung dengan baris di sini.

## Cara menjalankan

```bash
docker compose up -d db                  # Postgres dev (port 55432); database pms_e2e dibuat terpisah
cd web && npx playwright install chromium && cd ..   # sekali saja: browser untuk Playwright
scripts/e2e.sh                           # semua modul, lalu night audit
scripts/e2e.sh e2e/modules/04-frontdesk.spec.ts      # satu modul (night audit tetap jalan di akhir)
E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh e2e/modules/04-frontdesk.spec.ts   # tanpa menutup hari
scripts/e2e.sh --headed --workers=1      # lihat browsernya
cd web && npx playwright show-report     # report HTML: langkah, screenshot, video, dan trace untuk test yang gagal
```

Di Windows, jalankan dari Git Bash. Kalau `psql` tidak ada di PATH, script membuat database lewat `docker compose exec db psql`.

## Cara kerjanya

| Bagian | File | Isi |
|---|---|---|
| Runner | `scripts/e2e.sh` | Menghapus lalu membuat ulang `pms_e2e`, migrasi, membuat tenant `E2E` beserta admin, build `bin/e2e/` (api, migrate, pms-admin, pms-seed), lalu `npx playwright test` |
| Konfigurasi | `web/playwright.config.ts` | Menyalakan API (port 18090, rate limit 0, SMTP mati) dan vite (port 5174). Project `setup` → `modules` → `final` |
| Seed | `web/e2e/setup/seed.setup.ts` | Properti `E2E` (business day dibuka kemarin), `pms-seed` (50 kamar, tarif RO 60 hari, POS, inventory, banquet, aset, penerima paket night audit), peran dan user per desk |
| Fixtures | `web/e2e/support/fixtures.ts` | `loginAs(desk, path)` login lewat API di dalam context browser, karena refresh token dirotasi dan tidak boleh dipakai bersama. `data` untuk menyiapkan data lewat API. `pageGuard` menggagalkan test kalau ada error JavaScript atau respons API 5xx |
| Data | `web/e2e/support/factories.ts` | Tamu, reservasi, tamu in-house, charge, pembayaran, perusahaan, dan shift kasir. Setiap test membuat datanya sendiri dengan nama unik, jadi test bisa berjalan paralel |
| Teks | `web/e2e/support/i18n.ts` | `tx('kunci.i18n')` mengambil teks Indonesia dari `src/i18n/locales/id.ts`. Label tombol tidak di-hardcode, jadi test tetap jalan kalau kalimatnya diubah |

Aturan menulis test baru:
- Langkah yang diuji selalu dijalankan lewat UI. API hanya untuk menyiapkan data dan memeriksa hasil.
- Urutan pilihan selector: `data-testid`, lalu role/label, lalu teks i18n. Jangan pakai `waitForTimeout`; tunggu lewat `expect` atau `expect.poll`.
- Pembayaran tunai butuh shift kasir. Test lain memakai kartu atau transfer supaya tidak berebut laci.
- Test yang memajukan business date hanya boleh ada di `e2e/final/`.

## Desk (user) yang dipakai

| Kunci | Peran | Untuk apa |
|---|---|---|
| `admin` | Tenant admin | Setup, akuntansi umum, dan approver |
| `frontdesk` | E2E Resepsionis | Reservasi, check-in/out, folio, maintenance, lost & found (tanpa override kamar kotor, tanpa night audit) |
| `supervisor` | E2E Supervisor FO | Koreksi folio, void, refund, dan override |
| `cashier` | E2E Kasir | Shift kasir dan pembayaran |
| `housekeeping` | E2E Housekeeping | Status kamar saja |
| `accounting` | E2E Akuntansi | Akuntansi, payables, bank, pajak, dan budget |
| `pos` | E2E Kasir Outlet | Order, pembayaran outlet, room charge, dan KDS |

## Test case

### 00 Smoke: semua halaman terbuka (`modules/00-smoke.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| SMOKE `<grup>` | Admin login | Buka setiap rute tanpa `:id` dari `src/router/index.ts`, dikelompokkan per awalan alamat (34 grup, sekitar 180 halaman) | Halaman tidak dialihkan (izin/modul aman), tidak ada blok "no access", tidak ada error JS atau 5xx |

### 01 Auth dan hak akses (`01-auth.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| AUTH-01 | Admin aktif | Isi tenant, email, password di /login, lalu Masuk | Masuk ke beranda; menu user menampilkan email admin |
| AUTH-02 | — | Login dengan password salah | Muncul "Kode tenant, email, atau kata sandi salah."; field password kosong; tetap di /login |
| AUTH-03 | Belum login | Buka /reservations, lalu login | Diarahkan ke /login?redirect=…; setelah login kembali ke /reservations |
| AUTH-04 | Resepsionis login | Menu user → Keluar, lalu buka /in-house | Kembali ke /login; sesi tidak berlaku |
| AUTH-05 | Resepsionis login di /arrivals | Muat ulang halaman | Tetap di /arrivals (sesi dipulihkan lewat refresh cookie) |
| AUTH-06 | Housekeeping login | Lihat menu, lalu ketik /accounting/journals dan /setup/users | Tidak ada menu Akuntansi atau Setup; halaman jurnal menyatakan butuh `accounting.view`; /setup/users dikembalikan ke / |
| AUTH-07 | Token resepsionis | POST night-audit/run lewat API | 403 `PERMISSION_DENIED` |
| AUTH-08 | Resepsionis login | Lihat menu | Ada Front Office; tidak ada Setup atau Akuntansi |
| AUTH-09 | Resepsionis login | Menu user → English | `<html lang="en">` |
| AUTH-10 | User baru | Login → refresh (rotasi) → refresh lagi dengan cookie lama (jawaban rotasi "hilang") → logout kedua sesi → refresh cookie lama | Refresh ulang dalam masa tenggang 200 dengan sesi baru; setelah semua sesi logout ditolak 401 |

### 02 Setup properti (`02-setup.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| SET-01 | Admin | /setup/room-types → Baru → isi kode, nama, kapasitas → Simpan | Baris `type-<kode>` tampil; API menyimpan max_occupancy 3 |
| SET-02 | Admin | Tipe kamar dengan kapasitas maksimum melebihi dewasa + anak | Form menandai error; tipe tidak tersimpan |
| SET-03 | Admin | /setup/rooms → Baru → nomor, tipe, lantai, bed → Simpan | Baris `room-<nomor>` tampil dengan tipe yang benar |
| SET-04 | Admin | /setup/charge-codes → Baru → FOOD_BEVERAGE, harga 35.000, akun pendapatan | Baris `code-<kode>` tampil |
| SET-05 | Admin | /setup/rate-plans → Baru → BB, dijual lewat ROOM | Baris `plan-<kode>` tampil; meal_plan BB |
| SET-06 | Admin | /setup/users/new → email, nama, password, properti E2E + peran Resepsionis | User baru bisa login dan punya `frontdesk.checkin` di properti E2E |
| SET-07 | Admin | /setup/roles/new → nama, centang grup "Rooms" → Simpan | Peran tersimpan dengan `housekeeping.update` |

### 03 Reservasi (`03-reservations.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| RES-01 | Tamu ada | /reservations/new → tanggal (H+7, 2 malam) → Cari → Pesan tipe RO → cari tamu (kode tamu) → sumber PHONE → Pesan dan konfirmasi | Pindah ke detail reservasi; API: CONFIRMED, tamu, tanggal, dan tipe sesuai |
| RES-02 | Reservasi draf | Detail → Konfirmasi | Status CONFIRMED; tombol konfirmasi hilang |
| RES-03 | Reservasi ada | /reservations → cari nomor konfirmasi → klik | Baris `res-<nomor>` tampil; membuka detail |
| RES-04 | Reservasi confirmed | Batalkan → isi alasan → Batal; lalu Pulihkan | Tombol Pulihkan muncul; alasan tersimpan; setelah dipulihkan status bukan CANCELLED |
| RES-05 | Reservasi tanpa kamar | Tetapkan kamar → pilih kamar pertama di picker | Line punya `room_id`; nomor kamar tampil |
| RES-06 | Reservasi H+1 dengan kamar | Buka tape chart | Baris kamar dan bar reservasinya tampil |
| RES-07 | — | Buka /availability | Setiap tipe kamar punya baris |
| RES-08 | — | Pencarian dengan keberangkatan sebelum kedatangan | Field tanggal menampilkan masalah; tidak ada hasil |

### 04 Front office (`04-frontdesk.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| FD-01 | Reservasi tiba hari ini, kamar bersih sudah ditetapkan | /arrivals → Check-in → Tamu → Kamar (kamar yang ditetapkan diusulkan) → Deposit: metode Kartu + referensi → Terima deposit dan check-in | Langkah Selesai tampil; tamu in-house di kamar itu; deposit menjadi kredit di folio |
| FD-02 | Kamar reservasi kotor | Check-in → langkah kamar | Peringatan "not ready"; tombol lanjut nonaktif; tidak ada opsi override untuk resepsionis |
| FD-03 | Kamar kosong bersih | /walk-in → tipe, kamar, keberangkatan, nama tamu baru → Check-in | Pindah ke /stays/:id; nama tamu dan kamar tampil |
| FD-04 | Tamu in-house | /in-house → klik baris tamu | Detail stay terbuka |
| FD-05 | Tamu in-house, ada kamar bersih lain di tipenya | Stay → Pindah kamar → pilih kamar pertama yang ditawarkan, alasan → Pindahkan | Segmen stay berisi kamar baru |
| FD-06 | Tamu in-house 1 malam | Stay → Perpanjang → tanggal +1 → Simpan | departure_date maju satu hari |
| FD-07 | Tamu in-house | Admin: Stay → Batalkan check-in → alasan | Stay tidak OPEN; line reservasi kembali CONFIRMED |
| FD-08 | Tamu in-house (tiba hari ini) dengan charge minibar 150.000 | Stay → Check-out → konfirmasi pulang lebih awal → bayar kartu di langkah folio → Check-out | Wizard menampilkan biaya kamar malam ini dan meminta saldo + biaya itu; check-out selesai; stay CHECKED_OUT; saldo folio 0 |
| FD-09 | Tamu pulang besok | /departures → tanggal besok | Baris `departure-<stay>` tampil |

### 05 Folio dan kasir (`05-folio-cashier.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| FOL-01 | Tamu in-house | Folio → tab Charge → kode F&B, qty 2 × 45.000, keterangan → Posting | Baris tampil; saldo ≥ 90.000 |
| FOL-02 | Folio dengan saldo | Tab Pembayaran → 100.000 kartu, referensi → Posting | Saldo berkurang 100.000 |
| FOL-03 | Resepsionis tanpa shift | Tab Pembayaran → metode Tunai | Peringatan "Uang tunai butuh shift kasir yang terbuka." dengan tombol buka shift |
| FOL-04 | Charge hari ini | Supervisor: Koreksi → alasan → Lanjut ke persetujuan → email/password admin | Charge dikoreksi; saldo kembali 0 |
| FOL-05 | Folio ada | /folios → cari nomor folio | Baris `folio-<nomor>` tampil |
| CSH-01 | Kasir tanpa shift | /cashier/shifts → laci baru, float 500.000 → Buka; terima tunai 250.000 di folio; Tutup shift dengan hitungan = expected | Expected 750.000; kartu shift tertutup tampil; tidak ada shift terbuka |
| CSH-02 | Pembayaran kartu hari ini | /cashier → Tampilkan | Total per metode, termasuk CARD |

### 06 Housekeeping dan kamar (`06-housekeeping.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| HK-01 | Kamar kosong kotor | Housekeeping: /housekeeping → filter Kotor → Mulai bersihkan → Bersih → Inspeksi | Status DIRTY → CLEANING → CLEAN → INSPECTED |
| HK-02 | — | /room-status → cari nomor kamar | Hitungan per status tampil; hanya satu tile tersisa |
| HK-03 | Kamar kotor | /housekeeping/tasks → Buat tugas → Mulai → Selesai | Status kamar CLEAN atau INSPECTED |
| MT-01 | — | Resepsionis: /maintenance → kamar, PLUMBING, HIGH, deskripsi → Laporkan; admin (maintenance.manage): detail → Mulai → Selesaikan + catatan | OPEN → IN_PROGRESS → RESOLVED |
| LF-01 | — | Resepsionis: /lost-found → Catat barang → tempat (bukan di kamar), deskripsi, kategori, tempat simpan, calon pemilik → Catat; admin (lostfound.manage): detail → Serahkan → bukti | Nama penerima terisi dari calon pemilik; status RETURNED |
| BLK-01 | Kamar kosong H+25 | /room-blocks → kamar, OOO, tanggal, alasan → Buat blokir | Blokir tersimpan; kamar tidak ditawarkan untuk malam itu |

### 07 Corporate, grup, city ledger (`07-accounts.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| CORP-01 | Admin | /setup/companies → Baru → kode, nama, kontak, hapus centang "tanpa limit", limit 25 juta, termin 30 → Simpan | Baris `company-<kode>` tampil; limit dan termin tersimpan |
| GRP-01 | Perusahaan ada | /groups → Baru → kode, nama, tanggal, perusahaan, kontak → Simpan → buka | Ringkasan grup memuat kodenya |
| GRP-02 | Grup dan reservasi anggota | Buka detail grup | Baris `member-<konfirmasi>` tampil |
| CL-01 | Tamu in-house dengan charge 300.000, perusahaan ada | Folio → tab Transfer → perusahaan, jumlah = saldo → Transfer; City ledger perusahaan → Catat penerimaan transfer bank | Saldo folio 0; saldo city ledger = jumlah, lalu 0 setelah penerimaan |
| CL-02 | — | /city-ledger → centang "masih berutang" | Tidak ada error |

### 08 POS (`08-pos.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| POS-01 | Outlet seed | /pos/floor → Takeaway → nama → produk → Kirim → Bayar → Kartu (pas) → Bayar | Bill CLOSED |
| POS-02 | Tamu in-house, folio dibuka untuk outlet (limit 2 juta) | Takeaway → produk → Kirim → Bayar → Kamar → cari nomor kamar → pilih → pas → Bayar | Bill CLOSED; saldo folio tamu naik |
| POS-03 | Tamu in-house, folio belum dibuka untuk outlet | Bayar → Kamar → cari | Kamar ditandai terkunci (`open=false`); bill lalu dibayar kartu supaya tidak menahan night audit |
| POS-04 | — | /pos/report | Total atau keadaan kosong tampil |

### 09 Banquet (`09-banquet.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| BQ-01 | Perusahaan ada | /banquet/events → Baru → nama, jenis, pemesan perusahaan, kontak → Simpan; tab Sesi → tambah (ruang, tanggal H+21, 09:00–17:00, pax 12/10) | Event INQUIRY untuk perusahaan itu; sesi tampil |
| BQ-02 | Event dengan sesi H+30 | Tentative → batas opsi H+7 → Simpan | Status TENTATIVE; batas opsi tampil |
| BQ-03 | Ruang meeting seed | /banquet | Kalender dan kepala kolom ruang tampil |

### 10 Akuntansi dan back office (`10-accounting.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| ACC-01 | Bagan akun standar | /accounting/journals → Baru → tanggal, referensi, deskripsi; 1120 debit 125.000, 1140 kredit 125.000 → Posting | Jurnal MANUAL tampil di daftar |
| ACC-02 | — | Jurnal debit 100.000, kredit 90.000 | Selisih tampil; jurnal tidak tersimpan |
| ACC-03 | — | /accounting/trial-balance → Terapkan | Total tampil; API: total debit = total kredit |
| ACC-04 | — | Laba rugi, neraca, arus kas, buku besar | Semua terbuka tanpa error |
| ACC-05 | Token resepsionis | GET accounting/journals | 403 |
| AP-01 | — | /payables/suppliers → Baru → kode, nama, kontak, termin 14 → Simpan | Baris `supplier-<kode>` tampil |
| AP-02 | Supplier ada | /payables/bills → Baru → supplier, nomor faktur, tanggal, akun beban 500.000 → Posting; /payables/payments → Baru → supplier, tanggal bayar → Bayar semua → Posting | Tagihan UNPAID lalu PAID |
| BNK-01 | — | /bank/accounts → Baru → akun GL, nama, nomor rekening → Simpan | Rekening tampil di daftar |
| BUD-01 | — | /budget → Baru → tahun buku berjalan, nama, deskripsi → Buat draf | Anggaran tersimpan |
| TAX-01 | — | Halaman status, profil, kewajiban, faktur, SPT | Terbuka tanpa "no access" |

### 11 Inventory dan aset (`11-inventory-assets.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| INV-01 | Seed inventory | /inventory | Dashboard dan nilai stok tampil |
| INV-02 | Kategori dan satuan seed | /inventory/items/new → kode, nama, kategori, satuan, harga acuan 18.500 → Simpan | Pindah ke detail barang; harga acuan tersimpan |
| INV-03 | Barang seed | /inventory/items → cari kode | Baris `item-<kode>` tampil |
| INV-04 | Seed: user terpisah untuk setiap tingkat persetujuan (kepala departemen, approve, cost control, final); kepala departemen diset di semua departemen | /inventory/requests/new → departemen, gudang, tanggal butuh, barang, qty 12 → Ajukan | Pindah ke detail; status bukan DRAFT |
| INV-06 | Departemen baru tanpa kepala (API); draf PR untuk departemen itu oleh admin (API) | /inventory/requests/{id} → lihat peringatan → Ajukan | `approval-check` menjawab NO_APPROVER; peringatan `chain-blocked` menyebut tingkat 1 dan departemen, dengan tautan departemen dan peran; Ajukan tetap ditolak server dan draf tetap DRAFT |
| INV-05 | Gudang seed | /inventory/warehouses, /inventory/counts | Gudang seed tampil; halaman opname terbuka |
| AST-01 | Aset saldo awal seed | /assets → cari nomor aset → buka detail | Aset tampil di tabel; detail memuat nama |
| AST-02 | Aset dengan lokasi | Detail → Pindah → centang lokasi, pilih lokasi lain, alasan → Konfirmasi | location_id berubah |

### 12 Laporan dan audit (`12-reports-audit.spec.ts`)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| RPT-01 | — | /reports → jalankan setiap laporan di `reportDefs.ts` | Setiap laporan menampilkan hasil atau kosong, tanpa error |
| RPT-02 | Tamu in-house | Laporan in-house | Nomor kamar tamu ada di hasil |
| RPT-03 | — | Daily flash, guest ledger, deposit ledger, pembatalan, koreksi, Z-report | Terbuka tanpa error |
| RPT-04 | — | Dashboard, /performance | Terbuka |
| AUD-01 | Tamu baru dibuat | API audit-logs, lalu /audit | Ada entri untuk tamu itu; halaman audit terbuka |

### Final: Night audit (`final/night-audit.spec.ts`, berjalan paling akhir dan serial)

| ID | Prasyarat | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| NA-01 | Semua modul selesai; ada tamu in-house dan kedatangan yang tidak datang; opname demo dari `pms-seed assets` dibatalkan lewat API (opname terbuka membekukan gudang dan menahan penutupan hari) | /night-audit → pilih semua kedatangan → alasan → konfirmasi → Tandai no-show; centang konfirmasi → Jalankan | Line menjadi NO_SHOW; tidak ada blok "cannot run" (kalau ada, alasannya menjadi pesan gagal); tanggal bisnis +1; malam tamu in-house terposting di folio |
| NA-02 | NA-01 | /night-audit/archive | Baris paket untuk hari yang ditutup |
| NA-03 | NA-01 | Posting charge setelah night audit | Charge tercatat di tanggal bisnis baru; top bar menampilkan tanggal bisnis |

## Status

Run keenam (9 Nov 2026): **118 dari 118 lulus** dalam 14,7 menit (3 worker, Chromium), termasuk night audit yang menutup hari. Temuan di aplikasi dari run sebelumnya tercatat di bawah dan belum diperbaiki di kode aplikasi.

## Temuan dari run pertama (9 Nov 2026)

- **[Diperbaiki di `fix-e2e-findings`: wizard menambahkan biaya kamar dari dry run posting]** **Check-out di hari kedatangan selalu gagal sekali.** Wizard check-out menulis "Semua folio seimbang sejauh ini. Biaya kamar malam ini, bila belum diposting, ditambahkan sekarang dan harus dilunasi juga", tetapi formulir bayarnya hanya menawarkan saldo saat itu. Saat Check-out ditekan, biaya kamar malam ini diposting dan check-out ditolak dengan `FOLIO_NOT_BALANCED`. Resepsionis harus memposting biaya kamar dulu di halaman Biaya kamar, atau membayar lebih dari yang ditampilkan. FD-08 sekarang memposting biaya kamar dulu. Usul: wizard menambahkan biaya kamar malam ini ke jumlah yang harus dibayar.
- **`pms-seed assets` meninggalkan opname terbuka** (CNT-000001, Gudang Bar), dan itu menahan night audit. Di DB demo, opname ini perlu disetujui atau dibatalkan sebelum menutup hari.

## Temuan dari run kedua (9 Nov 2026)

- **[Diperbaiki di `fix-pr-approver-warning`: draf menampilkan peringatan dari `GET requests/{id}/approval-check`, tingkat yang tidak punya penyetuju disebut bersama departemennya, dengan tautan ke Departemen dan Peran; diuji di INV-06]** **Purchase request baru bisa diajukan kalau setiap tingkat persetujuan punya orang yang berbeda.** "Ajukan" menyimpan draf lalu menolak dengan `APPROVAL_CHAIN_UNSATISFIABLE` ("Tidak ada yang dapat menyetujui sebuah tingkat permintaan ini"). Aturan di `internal/inventory/approval.go`: tingkat kepala departemen butuh kepala atau wakil departemen yang memegang `inventory.dept_approve`; tingkat lain butuh pemegang izinnya; semuanya bukan peminta dan masing-masing orang yang berbeda. Properti baru tidak punya kepala departemen. Hotel kecil dengan satu admin yang sekaligus meminta tidak akan bisa mengajukan sama sekali. Perlu diputuskan: cukup didokumentasikan, atau halaman permintaan memberi peringatan lebih awal dan menyebut tingkat mana yang belum punya penyetuju.
- **[Diperbaiki di `fix-e2e-findings`: `select()` menyimpan error-nya]** **Request detail properti yang gagal di jaringan menjadi error yang tidak ditangkap.** Di `web/src/stores/property.ts`, `select()` memanggil `GET /properties/{id}` tanpa `try/catch`, padahal `loadProperties()` dan `refreshClock()` menangkap error-nya. Saat request itu gagal (di run kedua: `ERR_CONNECTION_TIMED_OUT` di tengah beban test paralel), router guard menerima promise yang ditolak dan halaman membuang "TypeError: Failed to fetch". Perbaikannya kecil: tangkap error di `select()` dan simpan ke `error`, seperti fungsi lain di store itu.

## Temuan dari run ketiga (9 Nov 2026)

- **`RESOURCE_BUSY` (409, retryable) saat membuat reservasi paralel.** Dengan 3 worker, satu `POST /reservations` tidak mendapat lock dalam `PMS_DB_LOCK_TIMEOUT` (5 detik). Server sudah menandainya `retryable`, dan client test sekarang mengulang seperti aplikasi. Perlu dipantau kalau beberapa resepsionis memesan bersamaan di jam sibuk.

## Temuan dari run keempat (9 Nov 2026)

- **[Diperbaiki di `fix-e2e-findings`: dalam masa tenggang server menerbitkan sesi baru selama ada sesi hidup sejak rotasi]** **Memuat ulang halaman saat sesi sedang di-refresh bisa membuat user ter-logout.** Server merotasi refresh token begitu menerima `POST /auth/refresh`. Kalau jawabannya batal karena halaman dimuat ulang (F5, atau membuka alamat lain di tab yang sama dalam sepersekian detik), browser tidak pernah menerima cookie baru. Refresh berikutnya membawa token lama. Dalam masa tenggang 30 detik, `internal/iam/service.go` menjawab `REFRESH_TOKEN_ROTATED` (401) dengan anggapan "browser sudah memegang cookie baru", lalu aplikasi kembali ke /login. Di smoke test ini terjadi pada /lost-found. Usul: dalam masa tenggang, server menerbitkan ulang sesi penerus untuk token yang sama dan tidak menjawab 401. Test sekarang menunggu refresh selesai sebelum pindah halaman (`settle()` di `support/fixtures.ts`).

## CI

Job `e2e` di `.github/workflows/ci.yml` berjalan setelah `backend` dan `web`. Job ini memakai service Postgres 16, menjalankan `npm run e2e:types`, menginstal Chromium, lalu `scripts/e2e.sh`. Report HTML dan trace diunggah sebagai artifact `playwright-report`.
