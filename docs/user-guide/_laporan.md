# Laporan penulisan Panduan Pengguna

Laporan untuk pemilik dan tim pengembang, bukan untuk staf hotel. Laporan ini diisi bertahap di setiap batch. Fase 6 menambahkan statistik akhir, hasil pemeriksaan, dan menggabungkan temuan audit Fase 1 (`_inventory.md` bagian 9).

## 1. Statistik per batch

| Batch | Isi | Halaman | Screenshot | Ukuran folder screenshot |
|---|---|---|---|---|
| 1 | 00-memulai, 01-front-desk, panduan-cepat/resepsionis | 23 | 46 | 1,6 MB (rata-rata 35,5 KB, terbesar 137 KB) |
| 2 | 02-housekeeping, 03-kasir-dan-pembayaran, 04-pos | 22 | 44 (1,7 MB) | 3,3 MB untuk 90 gambar (rata-rata 37,1 KB, terbesar 180 KB) |
| 3 | 05-banquet, 06-corporate-group-city-ledger, 07-accounting, 08-keuangan-back-office | 23 | 57 (2,1 MB) | 5,4 MB untuk 147 gambar (rata-rata 37,3 KB, terbesar 179 KB) |

## 2. Tanda PERLU VERIFIKASI

Tanda yang masih terbuka di halaman panduan:

- `03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md` dan `04-pos/bebankan-ke-kamar.md`: membatalkan beban POS di folio tamu setelah bill lunas. Belum ada jalan di layar (lihat 5.1). Panduan meminta staf melapor ke supervisor atau administrator.
- `05-banquet/posting-sesi-dan-penagihan.md`: **Pax aktual** di jendela Posting sesi tidak terbaca (bug, lihat 5.1). Panduan memberi jalan keluar lewat penyesuaian positif di folio acara.
- `06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md`: menagihkan biaya malam terakhir tamu yang pulang lebih awal ke perusahaan. Belum dicoba (lihat 5.1).
- `08-keuangan-back-office/settlement-kartu.md`: kapan pembayaran kartu masuk daftar **Belum dibayar**. Disimpulkan dari aturan akun kontrol, belum ditelusuri di kode jurnal penutupan hari.

Tanda yang sudah diselesaikan:

- `01-front-desk/membatalkan-check-in.md`: batal check-in setelah audit malam. Dibaca dari `internal/frontdesk/service.go` (`ReverseCheckIn`), lalu dicocokkan dengan test `TestReverseNeedsTheSameBusinessDay` dan bagian akhir test batal check-in di `internal/frontdesk/frontdesk_test.go`. Setelah tanggal bisnis berganti, pembatalan ditolak dengan `CHECK_IN_NOT_REVERSIBLE` ("Check-in hanya bisa dibatalkan pada hari yang sama dan sebelum pindah kamar."). Biaya apa pun di folio, termasuk yang sudah dibalik, memberi `CHECK_IN_HAS_CHARGES`. Jalan keluarnya sudah ditulis di halaman itu.
- `01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md`: mengisi pemesan pada draf tanpa pemesan. Dari UI tidak bisa (lihat 5.1). Jalan keluarnya, yaitu membuat ulang lalu membatalkan draf, ditulis sebagai langkah 7 dan 8.

## 3. Teks bahasa Inggris, kunci i18n mentah, dan kode mentah di layar

Halaman panduan menuliskan teks ini persis seperti tampilannya, lalu memberi artinya dalam kurung.

| Tempat | Yang tampil | Catatan |
|---|---|---|
| Pengaturan → Kode biaya, kolom Jenis, kode POS_ROUNDING | `chargeCodes.type_ROUNDING` | Kunci i18n tidak ada, jadi kuncinya tampil mentah |
| Reservasi baru, kartu Rincian, ringkasan reservasi | `PHONE`, `EMAIL`, `WALK_IN`, `WEBSITE`, `OTA`, `AGENT`, `OTHER` | Label Indonesia sudah ada (`reports.source.*`), tetapi tidak dipakai di sini |
| Tamu → Tamu baru, peringatan duplikat | `same email`, `same phone`, `same id document`, `same name and birth date` | Tidak ada teks Indonesia |
| Profil tamu, isian identitas | `id_type and id_number go together` (INCOMPLETE) | Tidak ada `fieldErrors.INCOMPLETE` |
| Akun saya → Ubah kata sandi | `at least 12 characters` (WEAK_PASSWORD) | Tidak ada `fieldErrors.WEAK_PASSWORD` |
| Check-in, isian kamar | pesan ROOM_BED_MISMATCH dari server | Tidak ada `fieldErrors.ROOM_BED_MISMATCH` |
| Profil tamu → Riwayat | Peran `booker`, `occupant`, `primary`, `accompanying`; Status `CONFIRMED`, `OPEN`, … | Kode mentah |
| Profil tamu, catatan baca-saja | menyebut kode izin `guest.write` | Sebaiknya nama izinnya |
| Reservasi → kartu Pembayaran dan folio | status folio `OPEN` | Kode mentah |
| Kotak Check-out, langkah selesai | `DIRTY`, `CLOSED` | Kode mentah |
| Tooltip tanggal bisnis di bilah atas | `Server time` | Teks Inggris tertanam di `TopBar.vue` |
| Pengaturan → Peran | grup `Inventory`, `Banquet`, `Fixed assets` | Tidak ada `permissionGroup.*` Indonesia |
| Ketersediaan | `Tampilkan bed type` | Campuran Inggris |
| Bagan kamar | `OOO`/`OOS`, `(upgrade)`, tombol `Minggu` | "Minggu" bisa dibaca sebagai hari Minggu |
| Daftar pembersihan, tabel beban kerja | `Not assigned` | Nama dari server (`housekeeping/tasks.go:228`). Tugas saya sudah memakai "Belum ditugaskan" |
| Panel kamar, bagian Hari ini | `Cleaning started (task)`, `Cleaning finished (task)` | Catatan bahasa Inggris dari server (`tasks.go:507`, `:551`) |
| Perawatan dan Barang tertinggal, tanpa akses | izin `maintenance.report` / `lostfound.report` di dalam kalimat | Sebaiknya nama izinnya |
| Blokir kamar, baris konflik | "Baris reservasi {id}" | Nomor internal baris, bukan nomor reservasi |
| Blokir kamar dan Perawatan | "Rusak (out of order)" / "Tidak dipakai (out of service)" | Di tempat lain "Rusak (OOO)" / "Tidak dilayani (OOS)": istilahnya tidak seragam |
| Tugas saya, label Dimulai | jam dengan format browser (`toLocaleTimeString`) | Bisa tampil "02:30 PM" di browser berbahasa Inggris |
| Folio, rincian baris | `exclusive`/`inclusive`; tarif `10.0000%` | Mode harga dan tarif mentah |
| Folio, baris void | `Void: Payment PAY000012 (CASH)` | Tidak diterjemahkan oleh `utils/folioDescription.ts` |
| Folio, transfer ditolak | izin `cityledger.read atau reservation.read` | Kode izin mentah |
| Biaya kamar, kolom alasan | `MISSING_NIGHTLY_RATE`, `NO_OPEN_FOLIO`, `STAY_CLOSED`, … | Kode mentah |
| Shift kasir | kode laci `MAIN` di daftar shift dan placeholder; izin `cashier.shift` di teks tanpa akses | Judul shift aktif sudah menerjemahkan ("Laci utama") |
| Folio dan shift, pesan isian | `only on the master folio of a group`, `at most 200 charges in a request`, `no such outlet in this property` | Tidak ada `fieldErrors.*` |
| POS, struk komplimen di browser | `posPay.method_COMP` | Kunci i18n tidak ada |
| Laporan outlet, daftar Diskon | `Discount: <item>` | Deskripsi bahasa Inggris dari server (`folios/outletbill.go:218`) |
| Laporan outlet, CSV | `business_date,bills,net,service,tax,rounding,total` | Judul kolom bahasa Inggris, angka dan tanggal mentah |
| POS, produk | perintah pendaftaran agen bahasa Inggris; label `Code page`, `X`, `Y`; pesan `HOLD_NOT_FOUND`, `TOO_DEEP`, `OPTION_GROUP_LIMIT` ("choose from 1 to 1") | Tidak ada teks Indonesia |
| POS, impor CSV | "Baris {row}: <field> · <CODE> — <detail bahasa Inggris>" | Kode mentah |
| POS, jenis pesanan DIRECT | "Kasir" di denah, "Kasir langsung" di layar dapur | Istilah tidak seragam |
| Akuntansi → Jurnal, keterangan jurnal buatan sistem | `Day close …`, `Reversal of …: …`, `Charges ROOM`; di buku besar juga tanggal mentah `2026-11-24` | Deskripsi bahasa Inggris dari server |
| Akuntansi → Jurnal, jenis jurnal | `journals.t_CASHIER`, `journals.t_ASSET` | Kunci i18n tidak ada |
| Akuntansi → Jurnal, rincian; Tahun buku, konfirmasi tutup | `500000` | Angka tanpa pemisah ribuan |
| Akuntansi → Tahun buku | `FY2026` | Singkatan bahasa Inggris |
| Akuntansi → Akun kontrol, persediaan | "Inventory Persediaan makanan" dan keterangan "The stock ledger, valued by the category of the items, up to the date" | Judul dan keterangan bahasa Inggris |
| Akuntansi → Akun sistem, akun persediaan dan aset | "Goods received not invoiced" dan sejenisnya | Keterangan bahasa Inggris |
| Banquet, Dokumen | pilihan bahasa `English` / `Bahasa Indonesia`, bahasa e-mail `ID`, galat kirim mentah | Teks tertanam di kode (`BanquetDocsTab.vue:157,176-177`) |
| Banquet, Item dan paket; baris paket | kode biaya mentah, misalnya `BQT_FB` | `BanquetCatalogView.vue:144`, `BanquetLinesTab.vue:93` |
| Banquet, Deposit & kebijakan; jendela batal | persen dengan titik desimal, misalnya `30.00` | Tidak diformat (`BanquetDepositTab.vue:54,79`, `BanquetCloseDialog.vue:169`) |
| Banquet, Jadwal harian | penataan mentah (`THEATER`), jam mentah | `BanquetScheduleView.vue:186,218` |
| Banquet, folio acara | `Cancellation fee`, `Deposit forfeited` | Deskripsi bahasa Inggris dari server (`internal/banquet/cancel.go:262-265`) |
| Audit malam, peringatan banquet | kalimat server bahasa Inggris, dengan tautan "Buka denah meja" ke `/pos/floor` | Tidak ada kunci `findings.*` untuk banquet (`NightAuditView.vue:463-464`) |
| Banquet, galat isian | `OPTION_DATE_REQUIRED`, `FEE_REASON_REQUIRED`, `REASON_REQUIRED`, `SETUP_NOT_OFFERED`, `PACKAGE_EMPTY` dan lainnya | Pesan server bahasa Inggris, tidak ada `fieldErrors` |
| Banquet, galat dari modul lain | `ITEM_NOT_FOUND` "Barang tidak ditemukan.", `TOO_MANY_ROWS` "Check-in menyebut terlalu banyak kamar…", `EMAIL_ALREADY_QUEUED` "Konfirmasi sudah menunggu…" | Teks Indonesia milik modul lain, menyesatkan di banquet |
| Pengaturan → Perusahaan, kotak POS | `companies.posBilling` | Teksnya tersimpan di `roomTypes.posBilling` (`CompaniesView.vue:170`, `id.ts:1678`) |
| Piutang perusahaan, rekening koran | `Folio FOL…`, `Receipt (BANK_TRANSFER)`, `Credit note · invoice …`, `Write-off · …` | Deskripsi bahasa Inggris dari server (`internal/cityledger/service.go:260-290`) |
| Piutang perusahaan, Invoice baru | kolom Check-out `2026-11-24` (tanggal UTC) | `CityLedgerAccountView.vue:495` |
| Piutang perusahaan, galat isian | `NOT_REVENUE`, `NOT_EXPENSE`, `INVALID_TARGET`, `EXCEEDS_AMOUNT` | Pesan server bahasa Inggris |
| Folio, tab Transfer; piutang perusahaan, rekening koran | "Jumlah (saldo folio 450000)", kolom Debit/Kredit | Angka tanpa pemisah ribuan |
| Piutang perusahaan dan Invoice jatuh tempo, tanpa akses | "…dibutuhkan izin cityledger.read." | Kode izin mentah |
| Utang usaha, pajak, bank, anggaran: pesan galat | `lines[0].amount: an amount above zero` | Jalur field dan pesan server bahasa Inggris di bawah pesan merah (14 layar) |
| Pajak, lembar kerja SPT; Bank, rekonsiliasi | "The month is not over yet.", "Some statement lines are not matched with the books." | Penghalang bahasa Inggris dari server |
| Jurnal dari utang usaha, pajak, bank | `Bill …`, `Payment … to …`, `Tax payment …`, `Bank statement line …`, `Commission: …` | Deskripsi bahasa Inggris |
| Bank, Mutasi bank dan rekonsiliasi; Anggaran | tanggal mentah (`2026-11-18`, `(2026-01-01)`, "Periode: 2026-01 – 2026-12") | Tidak diformat |
| Pajak, SPT | kolom Dibayar dan Terutang tanpa pemisah ribuan; kode biaya mentah `ROOM` | `TaxReturnsView.vue:268-269,282` |
| Settlement kartu, Settlement yang dibuat | kolom nomor jurnal berlabel "Tanggal" | `cards.settlementDate` dipakai dua kali (`BankCardsView.vue:53-54`) |
| Pajak terutang, keadaan kosong | `emptyState.taxLiability` (diduga) | Kunci i18n tidak ada |
| Utang usaha, pajak | `NUMBER_TAKEN` "Nomor konfirmasi sudah dipakai.", `LEDGER_IMMUTABLE` "Alokasi tanda terima tidak bisa diubah." | Teks Indonesia dari modul lain |

## 4. Perbedaan antara desain (`docs/architecture`) dan kode

Diisi di Fase 6 dari `_inventory.md` bagian 9.3, ditambah temuan batch berikutnya.

## 5. Fitur setengah jadi dan celah UX

### 5.1 Fitur setengah jadi atau tidak tersambung ke UI

- **Pemesan pada reservasi yang sudah tersimpan.** API `PATCH /reservations/{id}` menerima `guest_id` (`PatchReservationRequest`), tetapi tidak ada layar yang memakainya. Jendela **Ubah reservasi** hanya mengubah perusahaan, permintaan khusus, dan catatan, sedangkan formulir Reservasi baru tidak bisa membuat tamu. Akibatnya, draf yang tersimpan tanpa pemesan tidak pernah bisa dikonfirmasi, dan harus dibuat ulang. Pesan `BOOKER_REQUIRED` ("Pemesan wajib diisi: tamu atau perusahaan.") juga menyesatkan, karena konfirmasi hanya memeriksa tamu (`lifecycle.go:140`). Perusahaan tidak dihitung sebagai pemesan.
- **Profil tamu ganda tidak bisa digabung**, baik di UI maupun di API.
- **Tidak ada tombol menambah kamar** ke reservasi yang sudah ada. Reservasi baru dari layar Reservasi selalu satu kamar.
- **Walk-in** tidak punya langkah deposit dan isian identitas (berbeda dengan check-in biasa).
- **Produk habis (sold out)** hanya bisa ditandai lewat API (`PUT /pos/outlets/{id}/sold-out/{productId}`). Tidak ada tombol di layar POS. Panduan menyarankan menghapus centang **Dijual** untuk sementara.
- **Mengembalikan atau memindahkan beban POS ke kamar** (`POST /pos/room-charges/{id}/return|move`) tidak dipanggil layar mana pun. Setelah bill lunas, tidak ada jalan di layar untuk membatalkannya. Baris di folio tamu berkode `TRF`, sehingga:
  - **Reverse** disembunyikan (`FolioView.vue:387-388`), dan server menolak dengan `FOLIO_TRANSFER_INVALID` (`charges.go:310`);
  - **Penyesuaian** dengan kode `TRF` ditolak dengan `TRANSFER_CODE_NOT_MANUAL` (`posting.go:247-249`). Penyesuaian dengan kode outlet hanya bisa kalau kode itu sudah terposting di folio tamu (`ADJUSTMENT_EXCEEDS_POSTED`);
  - **Kembalikan** tidak menampilkannya di folio tamu. Di folio bill, `returnable` tidak melihat cakupannya (`transferviews.go:189`), dan pengembalian gagal dengan `FOLIO_CLOSED` karena folio bill sudah tutup.

  **Buka kembali** bill membuka bill lagi, tetapi layar bill tidak punya tombol pengembalian. Di Batch 2, panduan menyarankan Penyesuaian. Saran itu salah dan sudah dibetulkan di Batch 3.
- **Bug: Pax aktual di jendela Posting sesi (banquet).** Input `type=number` dengan `v-model` memberi angka, lalu `BanquetPostDialog.vue:57` memanggil `actual.value.trim()`. Akibatnya galat JavaScript, **Ditagih** tetap menunjukkan pax garansi, dan sesi ditagih menurut pax garansi walaupun Pax aktual diisi lebih besar. Terlihat di run screenshot: pax aktual 42 dengan garansi 40 ditagih 40 × 439.230 = 17.569.200. Pax aktual juga tidak punya layar lain (`PUT sessions/{sid}/actual` tanpa UI).
- **Banquet:**
  - Tidak ada layar untuk jam operasional ruang, urutan ruang, `pos_product_id` item (tautan stok hanya dari seed), serta tenaga penjual dan grup pada acara.
  - Server mengizinkan CANCEL dari Inquiry atau Tentatif (`cancel.go:174`), tetapi layar hanya menawarkan **Tandai batal**.
  - **Ubah jadwal** deposit dibatasi di UI dengan `banquet.confirm`, sedangkan server meminta `banquet.manage` (`deposits.go:238`).
  - **Terima DP** tidak memeriksa `payment.post` di UI.
  - **Transfer ke folio grup** hanya memeriksa `manage` di UI, sedangkan server meminta persetujuan FOLIO_TRANSFER.
  - **Tandai sudah dibaca** tampil untuk semua pembaca, bukan hanya departemennya.
  - Jenis pemberitahuan `FEE_PROPOSED` punya teks lonceng, tetapi tidak ada yang menulisnya.
  - Tidak ada toast sukses setelah menyimpan acara, sesi, konfirmasi, template, penerimaan DP, jadwal, kebijakan, dan mode posting.
- **Akuntansi:**
  - Tanggal mulai akuntansi dan bulan awal tahun buku tidak punya layar maupun endpoint.
  - Penghalang tutup periode (`blockers`) dikirim server (`periods.go:24`), tetapi tidak ditampilkan. Tombol **Tutup** hanya hilang.
  - Tanggal pembalikan jurnal diterima API, tetapi UI selalu memakai tanggal bisnis.
  - Daftar jurnal paling banyak 200 baris tanpa halaman berikutnya, dan tanpa filter akun.
  - Simpan akun tidak memberi pesan sukses.
- **Piutang perusahaan:**
  - **Catat pembayaran dari perusahaan** tidak pernah dialokasikan ke invoice (`CityLedgerAccountView.vue:118-121`), dan tidak ada layar untuk mengalokasikannya nanti. Saldo turun, tetapi invoice tetap belum dibayar dan tetap muncul di Invoice jatuh tempo.
  - Wizard check-out tidak punya langkah transfer ke piutang perusahaan (`CheckOutWizard.vue:154-156`). Biaya malam terakhir tamu yang pulang lebih awal tidak bisa dipindahkan ke perusahaan sebelum check-out (belum dicoba).
  - Perusahaan grup tidak bisa diubah di layar, padahal API menerimanya.
  - Halaman Perusahaan dijaga `company.manage`, sehingga peran baca saja tidak punya daftar perusahaan.
  - Invoice dan penghapusan piutang tidak punya halaman rincian, penghapusan piutang tidak punya PDF, dan surat peringatan tidak bisa dibatalkan.
  - "Buat invoice" tampil untuk peran yang hanya boleh membuat nota kredit.
  - Pilihan akun nota kredit dan penghapusan piutang menjadi kotak angka ID akun tanpa `accounting.view`.
- **Keuangan back office:**
  - Baris rekening koran hanya bisa diimpor dari CSV, tanpa ubah per baris.
  - Settlement kartu tidak bisa dibatalkan. "batalkan" pada baris yang sudah di-settle hanya melepas pasangan: jurnal tetap ada, dan baris tidak bisa di-settle lagi (`matching.go:40-70`).
  - PPN atas biaya bank lain tidak ditangani.
  - Nota kredit pemasok tidak mendukung refund atau nota tanpa tagihan, dan tidak punya PDF.
  - Aturan fee kartu, pemasok, dan rekening bank tidak bisa dihapus.
  - Daftar pembayaran pajak tidak punya layar.
  - Formulir kredit PPN awal menyembunyikan galat isiannya di jendela persetujuan.
  - Tanpa `accounting.view`, pilihan akun tetap kosong, sehingga tagihan pemasok tidak bisa dicatat sama sekali.
- **Laporan outlet per kasir dan per pelayan:** API mengirimnya (`floor_state.go:374`), tetapi halaman tidak menampilkannya.
- **Izin `folio.reopen` dan `folio.post_after_checkout`** ada di katalog izin, tetapi tidak ada layar atau layanan yang memakainya.
- **Menutup shift kasir lain** (`cashier.shift_manage`) diizinkan API, tetapi halaman Shift kasir tidak punya tombolnya.

### 5.2 Celah UX: angka, tanggal, dan teks yang tidak terformat

- **Toast perpanjang** menampilkan tanggal mentah: "Keberangkatan sekarang 2026-11-26.", bukan format tanggal lokal (misalnya 26 Nov 2026).
- **Pesan deposit** menampilkan saldo tanpa pemisah ribuan: "Deposit PAY000001 diterima; saldo folio -1000000.". Pesan "Biaya {amount} diposting ke folio {folio}." kemungkinan sama (belum dilihat di layar).
- **Kotak persetujuan Ubah tarif** selalu berbunyi "Malam yang sudah ditagih dikoreksi dengan adjustment…", juga saat yang diturunkan hanya malam yang belum ditagih.
- **Halaman tanpa akses:** halaman Reservasi tetap menampilkan tombol **Bagan kamar** kepada pengguna yang tidak boleh melihat reservasi.
- **Folio, Penyesuaian:** petunjuk "Terposting di folio ini: {net}" dan label "Jumlah (saldo folio {balance})" menampilkan angka tanpa pemisah ribuan (misalnya `90000`).
- **Folio, Refund:** jumlah refund terisi dengan angka mentah dari server (misalnya `250000.000`).
- **Shift kasir:** **Modal awal** dan batas selisih terisi dengan angka mentah tanpa pemisah ribuan. Waktu serah terima memakai `closed_at.slice(0, 10)` dari waktu UTC, jadi tanggalnya bisa bergeser sehari di sekitar tengah malam.
- **Daftar pembersihan:** kolom **Tugas** diformat sebagai uang (angka bulat, jadi untuk saat ini tidak terlihat bedanya). Judul kolom **Kamar** muncul dua kali.
- **Shift kasir, Tutup shift:** label **Departemen** tetap tampil tanpa isian kalau daftar departemen kosong untuk pengguna itu (`DepartmentSelect` disembunyikan saat daftarnya kosong, label `FormField` tidak). Terlihat untuk peran kasir E2E.
- **Umur utang usaha, baris Total:** jumlahnya bergeser satu kolom. Total "Belum jatuh tempo" tampil di bawah kolom 31–60 hari (terlihat di run screenshot dengan satu pemasok, 22.500.000).
- **Laporan outlet, daftar Komplimen:** baris tidak punya keterangan apa yang digratiskan ("<bill> · " lalu jumlahnya).

### 5.3 Celah UX: tombol yang tidak mencegah penolakan server

- **Pindah kamar:** tombol **Pindahkan** tetap aktif untuk kamar yang belum siap, walaupun pengguna tidak berhak melewatinya. Server lalu menolak dengan ROOM_NOT_READY.
- **Menetapkan kamar dan check-in:** tipe lain yang ditandai "(naik kelas)" ditawarkan juga kepada pengguna tanpa `reservation.upgrade`. Server lalu menolak dengan PERMISSION_DENIED.
- **Check-out:** folio perusahaan yang belum nol tidak menonaktifkan tombol **Check-out**. Server lalu menolak dengan FOLIO_NOT_BALANCED.
- **Bagan kamar, jendela pindah:** LINE_NOT_CONFIRMED menampilkan teks tentang check-in ("Hanya kamar yang terkonfirmasi yang bisa check-in.").
- **POS, Cetak:** tombol **Cetak** tampil untuk pelayan yang hanya punya `pos.order`, padahal server mewajibkan `pos.cashier`. Server lalu menolak dengan PERMISSION_DENIED.
- **POS, membuka bill meja:** jumlah tamu yang kosong di outlet yang mewajibkannya hanya memberi "Ada isian yang tidak valid.", tanpa pesan di bawah isian.
- **Folio, koreksi:** kesalahan isian pada reverse, penyesuaian, dan refund hanya tampil sebagai "Ada isian yang tidak valid." di jendela persetujuan, tidak di bawah isiannya.
- **Pesan menyesatkan saat reverse biaya hari sebelumnya:** kode `CORRECTION_REQUIRES_ADJUSTMENT` punya satu teks, "Hanya tanda terima pada tanggal bisnis berjalan yang bisa dibatalkan." (`id.ts:8065`). Teks itu juga tampil saat reverse biaya atau penyesuaian folio dari hari sebelumnya (`POST /folio-items/{id}/reverse` → `charges.go:322-324`) dan saat void pembayaran folio dari hari sebelumnya (`payments.go:475`), padahal yang dimaksud bukan tanda terima. Pengguna biasanya bertemu pesan ini di layar yang sudah basi (audit malam berjalan di antaranya), karena tombol Reverse disembunyikan untuk item hari sebelumnya. Butuh pesan terpisah, misalnya kode baru untuk biaya: "Hanya biaya pada tanggal bisnis berjalan yang bisa di-reverse: buat penyesuaian.".
- **Folio, reverse baris TRF:** jawabannya `FOLIO_TRANSFER_INVALID` "Biaya tidak bisa dipindah ke sana: kedua folio harus terbuka dan satu reservasi…", yang tidak menjelaskan bahwa baris transfer dikembalikan lewat Kembalikan.
- **Utang usaha:** **Batalkan…** tampil pada tagihan yang sudah dibayar, punya nota kredit, atau dicocokkan dengan persediaan. Server lalu menolak.
- **Pajak:** **Batalkan SPT…** tampil untuk bulan yang bukan yang terakhir (`TAX_RETURN_NOT_LATEST`). **Void kredit awal…** tampil setelah SPT pertama dilaporkan.
- **Bank:** **Buka kembali…** tampil pada setiap rekening koran yang sudah direkonsiliasi, padahal hanya yang terakhir yang bisa dibuka.
- **Anggaran:** **Aktifkan** tampil walaupun tidak ada penyetuju yang punya `budget.approve`. **Hapus draf** tampil pada draf yang sudah disalin (`BUDGET_IN_USE`).
- **Akuntansi:**
  - **Hapus** akun tampil untuk akun yang sudah punya entri jurnal (`ACCOUNT_HAS_ENTRIES`).
  - Pilihan akun jurnal manual menawarkan akun kontrol (`CONTROL_ACCOUNT`).
  - Pilihan akun sistem untuk beberapa kunci menawarkan semua jenis akun (`WRONG_TYPE`).
  - **Nonaktifkan** departemen yang menjadi default akun ditolak dengan `DEPARTMENT_IN_USE`.

## 6. Catatan screenshot

- **Kode properti `E2E`.** Pemilih properti menampilkan "E2E · Hotel Kamara Ubud", dan riwayat tamu menampilkan `E2E`. Kode itu dibuat oleh setup E2E bersama (`web/e2e/setup/seed.setup.ts`), yang juga dipakai test lain. Kode properti juga tidak bisa diubah setelah dibuat. Jadi kode itu dibiarkan. Hanya nama properti yang diubah oleh `web/e2e/docs/world.ts`.
- **Cara membuat ulang gambar:** `E2E_DOCS=1 E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh docs-screenshots.spec.ts`. Nama file selalu sama, jadi run baru menimpa gambar lama.

## 7. Kandidat video

Diisi di Fase 5.
