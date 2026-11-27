# Laporan penulisan Panduan Pengguna

Laporan untuk pemilik dan tim pengembang, bukan untuk staf hotel. Laporan ini diisi bertahap di setiap batch. Fase 6 menambahkan statistik akhir, hasil pemeriksaan, dan menggabungkan temuan audit Fase 1 (`_inventory.md` bagian 9).

## Prioritas perbaikan sebelum go-live

Disusun dari kendala yang sudah dikonfirmasi lewat kode atau run screenshot. Urutannya: uang lebih dulu, lalu penghalang kerja staf, lalu kosmetik. Nomor KD merujuk ke bagian 2a. Kendala yang sudah diperbaiki tetap di tabel, ditandai **Diperbaiki** dengan commit-nya.

| Prioritas | Temuan | Lokasi kode | Dampak | Usulan perbaikan singkat |
|---|---|---|---|---|
| 1 (uang) | **Diperbaiki (commit 84d9e2e)** **Check-out tamu pulang lebih awal menghitung malam ini** (KD-06, terbukti di run) | `web/src/components/CheckOutWizard.vue:39-66,89-95`; server benar: `internal/frontdesk/stayops.go:546-580` | Skenario harian front desk, dan berisiko menagih tamu satu malam lebih: staf tidak bisa check-out dari layar, dan kalau tamu membayar yang diminta, folio kelebihan bayar satu malam dan check-out ditolak | Saat **Konfirmasi keberangkatan lebih awal** dicentang dan tamu tiba sebelum hari ini, jangan tambahkan malam tanggal bisnis dari pratinjau biaya kamar |
| 1 (uang) | **Diperbaiki (commit 5e805db)** **Pax aktual banquet diabaikan** (KD-03) | `web/src/components/banquet/BanquetPostDialog.vue:57` | Tamu yang hadir di atas pax garansi tidak tertagih | Kirim pax aktual yang diketik ke permintaan posting sesi, lalu tampilkan di **Ditagih** |
| 1 (uang) | **Diperbaiki (commit 7a2cf30)** **Pembayaran perusahaan tanpa alokasi ke invoice** (KD-04) | `web/src/views/accounts/CityLedgerAccountView.vue:118-121` | Saldo piutang turun, tetapi invoice tetap **Belum dibayar** dan masuk daftar jatuh tempo, sehingga perusahaan bisa ditagih dua kali | Tambah layar alokasi penerimaan ke invoice, atau minta pilihan invoice di formulir **Catat pembayaran dari perusahaan** |
| 1 (uang) | **Diperbaiki (commit 20faae8)** **Beban POS di folio tamu tidak bisa dibatalkan** (KD-01, KD-02) | `internal/folios/charges.go:310` (`FOLIO_TRANSFER_INVALID`), `internal/folios/posting.go:247-249` (`TRANSFER_CODE_NOT_MANUAL`); layanan pengembalian beban kamar POS ada di server, tanpa tombol di layar bill | Beban yang salah tetap ada di tagihan tamu; **Buka kembali** bill tidak menariknya | Tombol **Kembalikan beban kamar** di bill POS (setelah dibuka kembali), memakai layanan pengembalian yang sudah ada |
| 1 (uang) | **Diperbaiki (commit dd0763d)** **Settlement kartu tidak bisa dibatalkan** (KD-05) | `internal/bankrec/matching.go:40-70` | Settlement yang salah tetap di buku besar; baris rekening koran tidak bisa diselesaikan lagi setelah dilepas | Void settlement yang membalik jurnalnya dan membebaskan barisnya |
| 1 (uang) | **Diperbaiki (commit ddda0fa)** **Aturan mulai penyusutan mengabaikan pengaturan properti** (KD-10) | `web/src/views/assets/AssetMakeDialog.vue:30,123` | Aset bisa disusutkan sebulan lebih awal | Isi pilihan awal dari pengaturan aset properti |
| 1 (uang) | **Diperbaiki (commit 5b3fead)** **Penerimaan perusahaan tanpa alokasi tidak bisa dialokasikan belakangan** (KD-27) | `internal/cityledger/http.go:28-29` (penerimaan hanya dicatat dengan alokasinya, atau dibatalkan); `internal/cityledger/service.go` `allocate` hanya dipanggil saat mencatat | Penerimaan yang melewati audit malam tanpa dibagi ke invoice: invoice tetap **Belum dibayar** dan jatuh tempo, perusahaan bisa ditagih ulang | Endpoint dan layar untuk mengalokasikan sisa penerimaan yang sudah tercatat ke invoice terbuka, di bawah lock perusahaan yang sama dengan penerimaan. Dibangun: **Alokasikan** dan **Lepas** di rekening koran (migration 00102) |
| 2 (uang) | **Selesai dibangun mengabaikan aturan mulai penyusutan properti** (KD-28) | `web/src/views/assets/AssetReviseDialog.vue:28,39` (`start_rule: 'IN_SERVICE_MONTH'`) | Aset hasil konstruksi bisa disusutkan sebulan lebih awal dari aturan hotel | Isi pilihan awal dari pengaturan aset properti, seperti **Jadikan aset** (KD-10) |
| 2 (uang) | **Beban POS ke kamar yang salah tidak bisa dikembalikan setelah tamu check-out** (KD-26) | `internal/folios/outletbill.go:598-599` (`requireOpen` pada folio tamu: `FOLIO_CLOSED`); tidak ada layar lain untuk beban POS di folio yang tertutup | Tamu tetap tertagih beban outlet orang lain, dan selisihnya hanya bisa dibereskan di luar sistem | Kredit ke folio atau invoice tamu setelah check-out, atau kembalikan bebannya ke piutang (city ledger), keduanya dengan alasan dan persetujuan |
| 2 (penghalang) | **Shift kasir lain tidak bisa ditutup dari layar** (KD-21) | Halaman Shift kasir (lihat 5.1); izin `cashier.shift_manage` sudah ada di server | Audit malam tertahan kalau kasir pulang tanpa menutup shift | Tombol **Tutup shift** untuk pemegang `cashier.shift_manage` di daftar shift terbuka |
| 2 (penghalang) | **Daftar pengguna tidak menampilkan akses** (KD-12) | `internal/iam/service.go:451` (`ListUsers`) dan `:984-986` (`toUser` mengisi `Grants: []Grant{}`) | Administrator tidak bisa memeriksa siapa memegang peran apa | Muat akses per pengguna di `ListUsers` |
| 2 (penghalang) | **Simpan rantai persetujuan butuh izin pengaturan properti** (KD-16) | `internal/inventory/approval.go:172` | Manajer persediaan mengisi rantai lalu ditolak | Samakan izin halaman dan server (pakai `inventory.manage`, atau sembunyikan tombol) |
| 2 (penghalang) | **Penyetuju tanpa izin melihat persediaan dialihkan keluar** (KD-17) | `web/src/router/index.ts:138,140` | Permintaan tertahan di tingkat kepala departemen atau penyetuju akhir | Tambahkan `inventory.dept_approve` dan `inventory.sr_final_approve` ke izin rute dan menu |
| 2 (penghalang) | **Kode biaya pembulatan POS tidak bisa diubah** (KD-15) | `internal/billingconfig/model.go:41,286`; kunci i18n `chargeCodes.type_ROUNDING` tidak ada | Akun atau departemen pembulatan tidak bisa dibetulkan | Tambahkan `ROUNDING` ke daftar jenis yang diterima saat ubah, dan labelnya ke `id.ts` |
| 2 (penghalang) | **Draf reservasi tanpa pemesan tidak bisa dikonfirmasi** (5.1) | `internal/reservations/lifecycle.go:140`; layar **Ubah reservasi** tidak punya isian pemesan | Draf harus dibuat ulang | Isian pemesan di jendela **Ubah reservasi** |
| 2 (penghalang) | **Baris akses yang belum lengkap dibuang** (KD-20) | `web/src/views/setup/UserFormView.vue:49-51` | Staf tersimpan tanpa akses yang dimaksud | Tolak simpan dengan pesan di bawah barisnya |
| 2 (penghalang) | **Kategori persediaan terkunci walaupun belum punya stok** (KD-19) | `web/src/views/inventory/InventoryCategoryFormView.vue:73` | Akun kategori yang salah tidak bisa dibetulkan | Kunci hanya kalau kategori punya stok, sama seperti server (`CATEGORY_HAS_STOCK`) |
| 2 (penghalang) | **Kesiapan akuntansi tanpa layar**: `ACCOUNTING_NOT_SET_UP`, `ACCOUNTING_NOT_STARTED`, `JOURNAL_SEQUENCE_MISSING` menahan audit malam | `internal/accounting/http.go:33`; `web/src/views/nightaudit/NightAuditView.vue:433` | Audit malam tertahan, staf tidak punya layar untuk menyelesaikannya | Layar kesiapan akuntansi dengan tautan ke setiap perbaikan |
| 2 (penghalang) | **Opname terbuka selalu menahan audit malam** (`count_blocks_night_audit` tanpa kontrol) | `web/src/views/inventory/InventoryCostingView.vue:146` | Hotel tidak bisa memilih "hanya peringatan" | Tambah centangnya di pengaturan persediaan |
| 2 (penghalang) | **Simpan pengguna bisa setengah tersimpan** (profil tersimpan, akses tidak) | `web/src/views/setup/UserFormView.vue:63-68` | Staf bisa tidak bisa masuk ke propertinya | Satu permintaan, atau pesan yang menyebut bagian yang sudah tersimpan |
| 2 (penghalang) | **11 izin persediaan tanpa label Indonesia** | `web/src/i18n/locales/id.ts:4799` | Administrator menyusun peran pembelian dari kode Inggris; salah pilih membuat permintaan tertahan | Tambahkan labelnya |
| 2 (penghalang) | **Register mulai aset terkunci setelah disimpan** (KD-24) | `web/src/views/assets/AssetSettingsView.vue:304-305` | Bulan awal register yang salah tidak bisa dibetulkan dari layar walaupun register masih kosong | Kunci hanya kalau register sudah berisi aset, sama seperti server |
| 2 (penghalang) | **Lokasi aset tidak bisa dimatikan** (KD-23) | Kartu **Lokasi** di **Pengaturan aset** | Lokasi yang tidak dipakai tetap muncul di pilihan | Kirim nama lokasi bersama saklar aktif, atau terima perubahan tanpa nama di server |
| 3 (kosmetik) | **Alasan biaya kamar dan `INVENTORY_BALANCE_DRIFT` mentah** | `NightAuditView.vue:406,414`; `internal/inventory/checks.go:243-249` | Staf harus mencari arti kode, dan ID barang alih-alih namanya | Teks Indonesia per alasan; sebutkan nama barang dan gudang |
| 3 (angka) | **Diperbaiki (commit 2c564f5)** **Penjumlahan float di Laporan Z dan Pembatalan** | `web/src/views/reports/day/ZReportsView.vue:66`; `CancellationsView.vue:37` | Total bisa punya sisa pembulatan | Jumlahkan dengan desimal tetap |
| 3 (kosmetik) | Label "Simpan dan ajukan" di kotak pesanan (KD-18), bulatan **Bawaan beli/resep** (KD-13), tautan alasan audit malam (KD-14), baris Total umur utang (KD-07), pesan `CORRECTION_REQUIRES_ADJUSTMENT` (KD-08), riwayat aset `[object Object]` (KD-09), template CSV aset `FF&E` (KD-11), label **Ajukan pelepasan** untuk administrator (KD-22), metode `cashier.COMP` di Kasir (KD-25) | Lihat bagian 2a | Staf salah membaca layar atau berkas | Lihat bagian 2a dan bagian 3, 5.2, 5.3 |

## Harus diisi sebelum panduan dibagikan

| Tempat | Isian | Keterangan | Status |
|---|---|---|---|
| `README.md`, bagian **Bantuan dan kontak** | `[ISI: email dukungan]` | Alamat email tim dukungan KamaraPMS | Belum diisi: nilainya belum diberikan pemilik (2026-11-26) |
| `README.md`, bagian **Bantuan dan kontak** | `[ISI: WhatsApp dukungan]` | Nomor WhatsApp tim dukungan KamaraPMS | Belum diisi: nilainya belum diberikan pemilik (2026-11-26) |

Semua halaman yang menyuruh staf menghubungi tim KamaraPMS merujuk ke bagian itu, jadi cukup diisi di satu tempat. Cari sisa placeholder dengan `grep -rn "\[ISI:" docs/user-guide`.

## 1. Statistik per batch

| Batch | Isi | Halaman | Screenshot | Ukuran folder screenshot |
|---|---|---|---|---|
| 1 | 00-memulai, 01-front-desk, panduan-cepat/resepsionis | 23 | 46 | 1,6 MB (rata-rata 35,5 KB, terbesar 137 KB) |
| 2 | 02-housekeeping, 03-kasir-dan-pembayaran, 04-pos | 22 | 44 (1,7 MB) | 3,3 MB untuk 90 gambar (rata-rata 37,1 KB, terbesar 180 KB) |
| 3 | 05-banquet, 06-corporate-group-city-ledger, 07-accounting, 08-keuangan-back-office | 23 | 57 (2,1 MB) | 5,4 MB untuk 147 gambar (rata-rata 37,3 KB, terbesar 179 KB) |
| 4 | 09-inventory, 10-aset-tetap, 11-audit-malam, 12-laporan, 13-pengaturan, 14-admin-platform, 11 halaman panduan-cepat | 47 | 98 (5,7 MB) | 11,4 MB untuk 245 gambar (rata-rata 45,2 KB, terbesar 179 KB) |
| 5 | faq (81 pertanyaan), troubleshooting (196 baris), glosarium (150 istilah), alur-harian, README (daftar isi lengkap, Bantuan dan kontak), 4 halaman celah route: daftar-kasir, pelayan, laporan-banquet, pengaturan-aset | 8 | 11 (0,55 MB); run pertama dihentikan sistem karena memori, enam gambar sisanya diambil di run kedua | 12,0 MB untuk 256 gambar (rata-rata 45,5 KB, terbesar 180 KB) |

## 2. Tanda PERLU VERIFIKASI

Tanda yang masih terbuka di halaman panduan: tidak ada.

Tanda yang sudah diselesaikan:

- `03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md` dan `04-pos/bebankan-ke-kamar.md`: membatalkan beban POS di folio tamu. Dikonfirmasi dari kode, sekarang KD-01 dan KD-02.
- `05-banquet/posting-sesi-dan-penagihan.md`: **Pax aktual**. Dikonfirmasi dari kode, sekarang KD-03. Jalan keluar penyesuaian positif diperiksa di kode posting folio dan banquet: lolos selama folio acara terbuka, dengan persetujuan `FOLIO_ADJUSTMENT`. Tidak lolos kalau folio acara sudah ditutup, dan harus dibuat di master folio grup kalau biaya sesi diarahkan ke sana.
- `06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md`: tamu pulang lebih awal. Dari kode dan test `TestCheckOutCatchesUpMissingNights` (`internal/frontdesk/stayops.go:546-580`): pada siang hari, tamu yang tiba sebelum hari ini tidak ditagih malam ini, jadi yang dipindahkan cukup saldo yang ada. Tamu yang tiba hari ini ditagih saat check-out, dan biaya itu tidak bisa dipindahkan ke perusahaan karena pemindahan hanya sebelum check-out dan tidak boleh melebihi saldo. Run screenshot (`[PULANGAWAL]`) menemukan bug wizard, sekarang KD-06.
- `08-keuangan-back-office/settlement-kartu.md`: kapan pembayaran kartu masuk **Belum dibayar**. Dari jurnal penutupan hari: setelah audit malam menutup hari bisnisnya (jurnal tutup hari mendebit akun kliring kartu). Pembayaran hari berjalan belum tampil, void tidak pernah tampil, refund kartu tampil sebagai baris minus. Pembayaran kartu dari POS, penerimaan piutang perusahaan, dan deposit banquet ikut.
- `11-audit-malam/menangani-penghalang.md`: tautan alasan yang tidak menggulir. Dikonfirmasi dari kode, sekarang KD-14.
- `01-front-desk/membatalkan-check-in.md`: batal check-in setelah audit malam. Dibaca dari `internal/frontdesk/service.go` (`ReverseCheckIn`), lalu dicocokkan dengan test `TestReverseNeedsTheSameBusinessDay` dan bagian akhir test batal check-in di `internal/frontdesk/frontdesk_test.go`. Setelah tanggal bisnis berganti, pembatalan ditolak dengan `CHECK_IN_NOT_REVERSIBLE` ("Check-in hanya bisa dibatalkan pada hari yang sama dan sebelum pindah kamar."). Biaya apa pun di folio, termasuk yang sudah dibalik, memberi `CHECK_IN_HAS_CHARGES`. Jalan keluarnya sudah ditulis di halaman itu.
- `01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md`: mengisi pemesan pada draf tanpa pemesan. Dari UI tidak bisa (lihat 5.1). Jalan keluarnya, yaitu membuat ulang lalu membatalkan draf, ditulis sebagai langkah 7 dan 8.

**Istilah teknis di halaman panduan.** Semua halaman Batch 1–4 diperiksa untuk "API", path endpoint, nama berkas kode, dan kode galat mentah. Yang tersisa hanya kode yang memang tampil di layar dan dikutip agar staf mengenalinya (misalnya `MISSING_NIGHTLY_RATE` di kotak biaya kamar, `PHONE`/`WALK_IN` di ringkasan reservasi, `BQT_FB`, `TRF`, `POS_ROUNDING`, `FB_CLASS_REVENUE_MISMATCH`). Kode seperti itu selalu disertai artinya.

## 2a. Kendala yang diketahui di panduan

Halaman panduan menandai perilaku yang sudah dikonfirmasi salah atau belum ada di layar dengan `> 🐞 Kendala yang diketahui (KD-xx):`. Setiap blok berisi apa yang terjadi, dampaknya bagi staf, dan jalan keluar yang benar-benar bisa dilakukan dari layar, atau siapa yang dihubungi. Tanda PERLU VERIFIKASI hanya dipakai untuk hal yang benar-benar belum pasti.

| ID | Halaman | Ringkasan | Lokasi kode | Dampak uang? (ya/tidak) | Catatan |
|---|---|---|---|---|---|
| KD-01 | `03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md` | **Diperbaiki (commit 20faae8).** Beban POS di folio tamu tidak bisa dibatalkan dari layar setelah bill lunas (baris `TRF` tidak bisa di-reverse atau disesuaikan) | `internal/folios/charges.go:310`, `internal/folios/posting.go:247-249`; pengembalian beban kamar POS tanpa tombol | ya | Perbaikan: bill POS yang sudah **Buka kembali** punya tombol **Kembalikan beban kamar** di baris "Dibebankan ke kamar" (izin kasir + `folio.reverse`, alasan, persetujuan FOLIO_TRANSFER) yang memanggil layanan pengembalian server (`POST /pos/room-charges/{id}/return`). Beban dibatalkan di folio tamu, batas outlet pulih, bill dibayar cara lain, diaudit. Panduan baru: `04-pos/mengembalikan-beban-kamar.md`. Test: `PosBill.test.ts`, `TestAChargeToTheWrongRoomIsReturnedFromTheReopenedBillAndPaidInCash`, E2E POS-05. |
| KD-02 | `04-pos/bebankan-ke-kamar.md` | **Diperbaiki (commit 20faae8).** **Buka kembali** bill tidak menarik beban kamarnya dari folio tamu | Layar bill POS: tidak ada tombol pengembalian beban kamar | ya | Perbaikan: bill POS yang sudah **Buka kembali** punya tombol **Kembalikan beban kamar** di baris "Dibebankan ke kamar" (izin kasir + `folio.reverse`, alasan, persetujuan FOLIO_TRANSFER) yang memanggil layanan pengembalian server (`POST /pos/room-charges/{id}/return`). Beban dibatalkan di folio tamu, batas outlet pulih, bill dibayar cara lain, diaudit. Panduan baru: `04-pos/mengembalikan-beban-kamar.md`. Test: `PosBill.test.ts`, `TestAChargeToTheWrongRoomIsReturnedFromTheReopenedBillAndPaidInCash`, E2E POS-05. |
| KD-03 | `05-banquet/posting-sesi-dan-penagihan.md` | **Diperbaiki (commit 5e805db).** **Pax aktual** diabaikan; sesi ditagih menurut pax garansi. Jalan keluar lewat penyesuaian positif di folio acara sudah diperiksa di kode: berlaku selama folio acara terbuka, butuh persetujuan, tidak masuk laporan pendapatan banquet dan tidak mengurangi stok | `web/src/components/banquet/BanquetPostDialog.vue:57` | ya | Perbaikan: dialog membaca angka dari input bertipe number (model-nya angka, bukan teks; `.trim()` melempar error sehingga pax aktual hilang), mengirimnya bersama posting, dan **Ditagih** menunjukkan max(garansi, aktual). Aturan server (`BilledPax`, desain 36 §10.5) sudah benar dan kini dikunci test. Test: `web/src/components/banquet/BanquetPostDialog.test.ts`, `TestThePostingBillsTheGuestsThatCameAboveTheGuaranteeAndTheGuaranteeAboveThem`, E2E BQ-04. |
| KD-04 | `06-corporate-group-city-ledger/invoice-dan-penerimaan.md` | **Diperbaiki (commit 7a2cf30).** **Catat pembayaran dari perusahaan** tidak pernah terhubung ke invoice; invoice tetap **Belum dibayar** dan jatuh tempo | `web/src/views/accounts/CityLedgerAccountView.vue:118-121` | ya | Perbaikan: formulir menampilkan invoice terbuka perusahaan dengan sisanya, mengisi **Dibayar sekarang** dari invoice tertua (bisa diubah, dengan **Bagi dari invoice tertua**), menampilkan **Belum dialokasikan**, dan mengirim `allocations` yang sudah diterima server (`ReceiptAllocationRequest`). Uang dihitung dalam ribuan (bigint), bukan float. Penerimaan lama tanpa alokasi: KD-27. Test: `CityLedgerAccountView.test.ts`, E2E NA-05. |
| KD-05 | `08-keuangan-back-office/settlement-kartu.md` | **Diperbaiki (commit dd0763d).** Settlement kartu tidak bisa dibatalkan; melepas pasangan baris meninggalkan jurnalnya, dan baris itu tidak bisa diselesaikan lagi | `internal/bankrec/matching.go:40-70` | ya | |
| KD-06 | `01-front-desk/check-out.md` (dirujuk dari `06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md`) | **Diperbaiki (commit 84d9e2e).** Wizard check-out menghitung biaya malam ini untuk tamu yang pulang lebih awal pada siang hari, padahal server tidak menagihnya. Terbukti di run: tombol mati walaupun saldo nol; membayar jumlah yang diminta membuat folio kredit −605.000 dan check-out ditolak. | `web/src/components/CheckOutWizard.vue:39-66,89-95`; `internal/frontdesk/stayops.go:546-580` | ya | Perbaikan: wizard tidak lagi menghitung sendiri; ia membaca pratinjau check-out server (`GET /stays/{id}/check-out/preview`, izin `frontdesk.checkout`), yang memakai fungsi keberangkatan yang sama dengan check-out (`checkOutDeparture`) dan dry run biaya kamar untuk keberangkatan itu. Jumlahnya sama untuk semua peran, jadi jalan keluar lewat peran terbatas tidak diperlukan lagi. Test: `internal/frontdesk/checkout_preview_test.go`, `web/src/components/CheckOutWizard.test.ts`, E2E NA-04 di `web/e2e/final/night-audit.spec.ts`. |
| KD-07 | `08-keuangan-back-office/utang-usaha.md` | Baris **Total** umur utang usaha bergeser satu kolom | `web/src/views/payables/AgingView.vue:110-113` | tidak | |
| KD-08 | `03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md` | Pesan `CORRECTION_REQUIRES_ADJUSTMENT` menyebut tanda terima untuk biaya dan pembayaran hari sebelumnya | `web/src/i18n/locales/id.ts:8065`; `internal/folios/charges.go:322-324`; `internal/folios/payments.go:475` | tidak | |
| KD-09 | `10-aset-tetap/daftar-tunggu-dan-register.md` | Riwayat register aset menampilkan `[object Object] → [object Object]` | `web/src/views/assets/AssetView.vue:150-152` | tidak | |
| KD-10 | `10-aset-tetap/daftar-tunggu-dan-register.md` | **Diperbaiki (commit ddda0fa).** **Mulai disusutkan** di **Jadikan aset** selalu **Bulan mulai pakai**, walaupun pengaturan properti berbeda | `web/src/views/assets/AssetMakeDialog.vue:30,123` | ya | Perbaikan: **Daftar tunggu** membaca pengaturan aset (`GET /assets/settings`) dan meneruskan `start_rule`-nya ke **Jadikan aset**. Itu menjadi pilihan awal, dan tulisan "bawaan properti" menempel pada pilihan itu, bukan selalu pada **Bulan mulai pakai**. Desain 37 §4.2.4 dan A8. Test: `AssetsAcquisition.test.ts` untuk kedua pengaturan. Gambar `aset-tunggu-04` diambil ulang dan tidak berubah (hotel contoh memakai **Bulan mulai pakai**), jadi gambar lama dipertahankan. |
| KD-11 | `10-aset-tetap/saldo-awal-aset.md` | Template CSV saldo awal memakai kategori `FF&E` yang ditolak | `web/src/views/assets/AssetOpeningView.vue:33` | tidak | |
| KD-12 | `13-pengaturan/pengguna.md` | Daftar pengguna menulis "Tanpa akses properti" untuk semua staf | `internal/iam/service.go:451,984-986` | tidak | |
| KD-13 | `09-inventory/barang-gudang-dan-saldo-awal.md` | Bulatan **Bawaan beli** dan **Bawaan resep** tidak tampil terisi setelah diklik (`@click.prevent` pada radio dengan `:checked`); pilihannya tetap tercatat | `web/src/views/inventory/InventoryItemFormView.vue:214-215,232-233` | tidak | |
| KD-14 | `11-audit-malam/menangani-penghalang.md` | Tautan di "Hari belum bisa ditutup karena:" tidak menggulir halaman (gulir hanya sekali saat halaman dibuka; router tanpa `scrollBehavior`) | `web/src/views/nightaudit/NightAuditView.vue:216-224,484` | tidak | |
| KD-15 | `13-pengaturan/pajak-dan-kode-biaya.md` | Kode biaya pembulatan POS tidak bisa diubah, dan jenisnya tampil sebagai kunci mentah | `internal/billingconfig/model.go:41,286` | tidak | |
| KD-16 | `09-inventory/rantai-persetujuan.md` | **Simpan rantai** butuh izin mengubah pengaturan properti, padahal halamannya terbuka dengan izin persediaan | `internal/inventory/approval.go:172` | tidak | |
| KD-17 | `09-inventory/rantai-persetujuan.md` | Penyetuju tanpa izin melihat persediaan dialihkan keluar dari permintaan yang harus diputuskannya | `web/src/router/index.ts:138,140` | tidak | |
| KD-18 | `09-inventory/permintaan-dan-pesanan-pembelian.md` | Kotak "Simpan dan ajukan" di kotak pesanan berarti "kirim ke pemasok sekarang" | `web/src/views/inventory/purchasing/PurchaseRequestView.vue:559` | tidak | |
| KD-19 | `09-inventory/menyiapkan-persediaan.md` | Formulir kategori terkunci begitu kategori berisi barang, walaupun belum punya stok | `web/src/views/inventory/InventoryCategoryFormView.vue:73` | tidak | |
| KD-20 | `13-pengaturan/pengguna.md` | Baris akses yang belum lengkap dibuang tanpa peringatan | `web/src/views/setup/UserFormView.vue:49-51` | tidak | |
| KD-21 | `11-audit-malam/menangani-penghalang.md` | Shift kasir milik orang lain tidak bisa ditutup dari layar; shift yang ditinggal terbuka menahan audit malam | Halaman Shift kasir (lihat 5.1) | tidak | |
| KD-23 | `10-aset-tetap/pengaturan-aset.md` | **Matikan**/**Aktifkan** lokasi aset selalu gagal ("Ada isian yang tidak valid."): layar hanya mengirim saklar aktif, server menolak perubahan tanpa nama | Layar **Pengaturan aset**, kartu **Lokasi**; validasi ubah lokasi di `internal/assets` | tidak | Terbukti di run (`[KD-23]`): setelah **Matikan** pada KOLAM, baris tetap **Aktif** dan muncul "Ada isian yang tidak valid. Perbaiki isian yang ditandai, lalu coba lagi." |
| KD-24 | `10-aset-tetap/pengaturan-aset.md` | **Register mulai** terkunci begitu disimpan, padahal keterangannya "Terkunci setelah ada aset yang dimasukkan." | `web/src/views/assets/AssetSettingsView.vue:304-305` (`settings.started`); `internal/assets/setup.go:23` (`Started: r.RegisterFrom != nil`) | tidak | Diperiksa di kode: kunci layar mengikuti "register mulai sudah diisi", bukan "register sudah berisi aset" |
| KD-25 | `03-kasir-dan-pembayaran/daftar-kasir.md` | Bill POS komplimen tampil dengan metode mentah `cashier.COMP` di kolom **Metode** dan kotak totalnya | `web/src/views/billing/CashierView.vue:44`; kunci `cashier.COMP` tidak ada di `web/src/i18n/locales/id.ts` (sekitar baris 959-964) | tidak | Diperiksa di kode; run hotel contoh tidak memuat bill komplimen di tanggal itu |
| KD-26 | `04-pos/mengembalikan-beban-kamar.md` | Beban POS ke kamar yang salah yang baru ketahuan setelah tamu check-out (folio tamu tertutup) tidak bisa dikembalikan dari layar mana pun: **Kembalikan beban kamar** ditolak, dan folio tamu tidak menerima reverse atau penyesuaian untuk baris `TRF` | `internal/folios/outletbill.go:598-599` (`requireOpen`, `FOLIO_CLOSED`); `internal/folios/service.go:152-155` | ya | Ditemukan saat review PR #106 (KD-01, KD-02). Prioritas 2. Belum diperbaiki; panduan menyuruh melapor ke supervisor atau administrator |
| KD-27 | `06-corporate-group-city-ledger/invoice-dan-penerimaan.md` | **Diperbaiki (commit 5b3fead).** Penerimaan perusahaan yang dicatat tanpa alokasi ke invoice tidak bisa dialokasikan belakangan; setelah audit malam juga tidak bisa dibatalkan, sehingga invoicenya tetap **Belum dibayar** dan jatuh tempo | `internal/cityledger/http.go:28-29`: server hanya menerima `allocations` saat penerimaan dicatat; pembatalan hanya pada tanggal bisnis berjalan (`CORRECTION_REQUIRES_ADJUSTMENT`) | ya | Ditemukan saat memperbaiki KD-04. Perbaikan (desain `docs/architecture/41-receipt-allocation.md`, migration 00102): `POST {P}/city-ledger/receipts/{id}/allocations` mengalokasikan sisa penerimaan dari hari mana pun ke invoice terbuka (izin `cityledger.receive`, tanggal alokasi = tanggal bisnis berjalan, tanpa jurnal, di bawah lock perusahaan); `POST {P}/city-ledger/receipt-allocations/{id}/release` melepas alokasi yang salah dengan alasan dan persetujuan `CITY_LEDGER_VOID` sebesar alokasinya. Layar: **Uang muka belum dialokasikan** di kepala akun, **Alokasikan** dan **Lepas** di rekening koran. Blok 🐞 di panduan dihapus; bagian baru "Mengalokasikan penerimaan yang sudah tercatat" dan "Melepas alokasi yang salah". Test: `allocate_later_test.go` (termasuk 4 race), `schema_test.sql`, `db-test.sh` (back-fill dan Down 00102), `AllocationTable.test.ts`, `CityLedgerAccountView.test.ts`, E2E NA-07. |
| KD-28 | `10-aset-tetap/memindahkan-dan-mengubah-aset.md` | Jendela **Selesai dibangun** (aset dalam pembangunan ke kategori jadi) selalu mengisi **Mulai disusutkan** dengan **Bulan mulai dipakai**, walaupun pengaturan properti memakai bulan berikutnya | `web/src/views/assets/AssetReviseDialog.vue:28,39`; desain 37 §10 (contoh "construction finished": *the start rule of the property*) | ya | Ditemukan saat memperbaiki KD-10, belum diperbaiki (di luar daftar PR B) |
| KD-22 | `10-aset-tetap/pelepasan-aset.md` | Untuk administrator tenant, pratinjau dan tombol (**Ajukan pelepasan**) menyebut penyetuju lain, padahal pelepasan langsung diposting (pengecualian administrator yang memang dirancang, `docs/architecture/32-distinct-approver.md` butir D) | `internal/iam/approval.go:135-137`; dialog pelepasan aset | tidak | |

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
| Pengaturan → Peran, izin persediaan | 11 dari 14 izin persediaan tanpa label Indonesia: request, purchase, receive, bill, move, count, approve, dept_approve, cost_control, final_approve, sr_final_approve | Hanya view, manage, dan opening yang punya label (`id.ts:4799`, blok `inventory_*`) |
| Persediaan, galat isian di baris retur, tagihan, transfer, dan pesanan PR | `TOO_SMALL`, `EXCEEDS_REQUEST`, `LINE_NOT_ORDERED`, `EXCEEDS_RECEIPT`, `NOT_MATCHED`, `NO_DRAFT` / `OTHER_SUPPLIER` / `REVERSED`, `*_NOT_FOUND` | Kalimat server bahasa Inggris, tidak ada `fieldErrors.*` |
| Persediaan → Harga pokok, Cek pengaturan | lencana kode mentah di setiap temuan | `InventoryCostingView.vue:218` |
| Persediaan → Harga pokok, Cek pengaturan | kalimat server bahasa Inggris untuk `COSTING_METHOD_NOT_CHOSEN` | Tidak ada teks Indonesia (`internal/inventory/setupcheck.go:57`) |
| Persediaan → Harga pokok, Cek pengaturan | `APPROVAL_CHAIN_EMPTY` dengan nilai `{doc}` mentah `PR` / `SR` | Teks kunci cek pengaturan |
| Persediaan → Harga pokok, Cek pengaturan | `FB_CLASS_REVENUE_MISMATCH` dengan `BEVERAGE` / `RESTAURANT` di tengah kalimat Indonesia | Terlihat di gambar inventaris-siapkan-02 |
| Persediaan → Amenities otomatis, kotak Malam ini | pesan server mentah (`preview-invalid`) | `AmenitiesPlanView.vue:223` |
| Persediaan → Draf tagihan | pesan server bahasa Inggris (`preview-error`) | `BillDraftView.vue:307` |
| POS → Produk, ringkasan resep | `Food cost` | `id.ts:9359` (`pos.recipe.foodCost`) |
| Persediaan → Penjualan POS tanpa potongan stok | "modul inventory" | `id.ts:5045` (`posGap.intro`), juga `id.ts:9598` (`recipeNote`) |
| Persediaan → Penjualan POS tanpa potongan stok | menu "Penjualan POS tanpa stok", kartu "Penjualan POS tanpa potongan", judul "Penjualan POS tanpa potongan stok" | Tiga nama untuk satu halaman |
| Persediaan, beberapa layar | `Entertainment`, `waste`, `Cost control`, `PR` ("Buat PR"), `house use`, `out-of-order` | Campuran Inggris |
| Persediaan → Rantai persetujuan, tingkat 2 PR bawaan | `Cost control` | Nama bawaan dari server (`internal/inventory/approval.go:92`) |
| Pengaturan → Aturan persetujuan, baris "Penyetuju: …" | `inventory.approve`, `banquet.confirm`, `asset.dispose` | Kode izin tanpa terjemahan tampil apa adanya (`ApprovalRulesView.vue:133`) |
| Pengaturan → Properti, galat Zona waktu | `an IANA time zone such as Asia/Jakarta` (INVALID_TIMEZONE) | Pesan server (`internal/tenancy/model.go:171`) |
| Pengaturan → Properti, isian deposit | `more decimals than the property currency has` (TOO_MANY_DECIMALS) | Pesan server (`internal/tenancy/model.go:196`) |
| Pengaturan → Properti, petunjuk isian | "… statement bank …" | Campuran Inggris (gambar pengaturan-properti-08) |
| Pengaturan → Pembatasan penjualan, Terapkan tanpa pilihan | `set or clear at least one attribute` (NOTHING_TO_CHANGE) | Pesan server (`internal/rates/restrictions.go:132`) |
| Pengaturan → Peran dan Pengguna, galat izin | pesan server bahasa Inggris untuk `UNKNOWN_PERMISSION` / `INVALID_GRANT` | Galat isian dari `internal/iam` |
| Pengaturan → Peran, izin transfer di grup Tagihan | "(approver juga harus memegangnya)" | Kata Inggris (gambar pengaturan-peran-03) |
| Pengaturan → Kode biaya dan Pajak, isian Akun buku besar | pesan "tidak cocok" dengan jenis akun bahasa Inggris huruf kecil, misalnya `revenue` | `GlAccountInput.vue:41` (`a.account_type.toLowerCase()`) |
| Pengaturan → Aturan yield, daftar | `(500000 to 2000000)` | Kata " to " tertanam di kode (`YieldRulesView.vue:332`) |
| Pengaturan → Paket tarif, petunjuk Jenis | "butuh izin reservation.komplimen" | Kode izin mentah, dan izin itu tidak ada (`id.ts:4692`, `occupancy.kindHint`) |
| Pengaturan, catatan baca-saja paket tarif, pajak, dan lainnya | "Setup → Peran", "Setup, Paket tarif", "Setup → Pajak" | Menu Indonesia bernama **Pengaturan** (`id.ts:1708`, `:1739`, `:3381`) |
| Pengaturan → Tipe kamar, tipe tempat tidur dan paket bawaan | `King`, `Twin`, `Room only`, `RO · Room Only` | Data seed, bukan i18n |
| Pengaturan → Sistem, baris galat | kode galat mentah di `<code>` | `SystemView.vue:45` |
| Semua layar, toast tanpa pesan server | `Something went wrong. Try again.` | Cadangan bahasa Inggris (`useToast.ts:72`) |
| Console platform | `Password`, `User`, `Void` | `platform/login.id.ts:7-8,13`; `plans.id.ts:14`; `tenant.id.ts:54,103`; `tenants.id.ts:41`; `invoices.id.ts:22-23,57,61` |
| Console platform dan Pengaturan → Langganan, nama paket bawaan | `Trial (all modules)` | Nama paket dari seed |
| Audit malam, langkah 4, "{n} malam tidak bisa dibebankan:" | `NO_ROOM_FOR_NIGHT`, `MISSING_NIGHTLY_RATE`, `INVALID_CHARGE_CODE`, `ROUTING_TARGET_CLOSED`, `NO_OPEN_FOLIO` | Tidak ada i18n untuk alasannya (`NightAuditView.vue:406`). Sama dengan Biaya kamar di atas |
| Audit malam, langkah 4, biaya tidak valid | `OUTSIDE_STAY`, `STAY_CANCELLED` | `NightAuditView.vue:414` |
| Audit malam, "Akuntansi belum siap…" | kode di `<code>` dan kalimat server bahasa Inggris ("the system account … is not mapped; …") | `NightAuditView.vue:433`, teks dari `internal/accounting/readiness.go:84-194` |
| Audit malam, Peringatan, blokir yang berakhir | "Blokir OOO/OOS pada kamar …" | Jenis blokir mentah. Kunci `blockType.*` ada, tetapi tidak dipakai (`NightAuditView.vue:475`) |
| Audit malam, penghalang `INVENTORY_BALANCE_DRIFT` | ID barang dan gudang (angka), bukan namanya | `internal/inventory/checks.go:243-249` |
| Arsip audit malam, Riwayat pengiriman | galat server e-mail bahasa Inggris | `NightAuditArchiveView.vue:136-138`, `:308` |
| Arsip audit malam, bagian CSV | judul kolom bahasa Inggris | Tidak mengirim `lang` (`NightAuditArchiveView.vue:157`, `utils/documents.ts:68-69`) |
| Laporan → Kedatangan / Keberangkatan, kolom Status | `CONFIRMED`, `CHECKED_IN`, `COMPLETED`, `NO_SHOW`, `CANCELLED` / `OPEN`, `CHECKED_OUT` | `reportDefs.ts:100`, `:107` |
| Laporan → Kamar yang belum bersih | status, okupansi, dan prioritas mentah | `reportDefs.ts:152` |
| Laporan → Ringkasan harian | Status `OPEN` / `CLOSED`; "Pembayaran {method} (bersih)" dengan kode metode mentah | `reportDefs.ts:48-58` |
| Laporan → Pendapatan, kolom Jenis | `ROOM` dan kode jenis lain | `reportDefs.ts:67` |
| Laporan → Kasir, kolom Metode | `CASH`, `CARD`, … | Kunci `dayReports.method.*` sudah ada, tetapi tidak dipakai (`reportDefs.ts:84`) |
| Laporan dan Jejak audit, tanpa akses | "(izin report.view)", "(izin audit.read)", "(butuh izin report.view dan cashier.shift_manage)" | Kode izin mentah (`ReportsView.vue:118`, `AuditTrailView.vue:174`, `DailyFlashView.vue` sekitar :44, `ZReportsView.vue` sekitar :27) |
| Laporan harian, judul tab browser | `Daily flash report`, `Cancellations and no-shows`, `Guest ledger`, `Deposit ledger`, `Voids, corrections and discounts`, `Z report of all drawers` | Rutenya tanpa `titleKey` (`router/index.ts:269-274`) |
| Jejak audit, Detail | JSON mentah di Sebelum / Sesudah (kunci Inggris, kode mentah); tooltip aksi berupa kode | `AuditTrailView.vue:249`, `:256-257` |
| Aset tetap, galat | `ACCOUNT_INACTIVE`, `NOT_A_DEBIT`, `PROCEEDS_ACCOUNT_INVALID`, `DISPOSAL_NOT_FOUND`, `DEPRECIATION_RUN_NOT_FOUND`, `DISMISSAL_NOT_FOUND`, `BELOW_CAPITALIZATION_THRESHOLD`, `OPERATING_EQUIPMENT_EXPENSED`, `NOTHING_DUE` | Pesan server bahasa Inggris, tidak ada `errors.*` (daftar lengkap di codes.txt) |
| Aset tetap, galat dari modul lain | `CATEGORY_ACCOUNT_MISSING` "Kategori barang tidak punya akun pemakaian…", galat isian `CONTROL_ACCOUNT` "Hanya tutup hari…", `TOO_MANY_ROWS` tentang check-in | Teks Indonesia milik modul lain, menyesatkan di aset |
| Aset tetap → Saldo awal aset | `Dr` / `Cr` di pratinjau jurnal; kolom `#`; kode kategori dan kode akun mentah di cek buku besar; placeholder `G1` | `AssetOpeningView.vue:274`, `:244`, `:280`, `:225` (`fiscal_group` teks bebas) |
| Aset tetap → Laporan register | status `ACTIVE` | `AssetReportView.vue:170` |
| Aset tetap → Laporan fiskal | `SL` / `DB` | `AssetFiscalView.vue:180` |
| Aset tetap → Opname, tempat | kode mentah `FL:xxxx` | `AssetAuditView.vue:162` |
| Aset tetap → halaman aset dan daftar tunggu | jenis aset, teks riwayat, dan teks sumber jatuh ke kode mentah kalau kuncinya tidak ada | `AssetView.vue:160`, `:169` |

## 4. Perbedaan antara desain (`docs/architecture`) dan kode

Dari `_inventory.md` bagian 9.3, ditambah temuan saat menulis Batch 1-5. Panduan mengikuti **kode** (yang tampil di layar), bukan desain. Kolom "Tindakan" menyebut apa yang perlu diputuskan: desain diperbarui, atau kode menyusul.

| Area | Desain | Kode (yang ditulis di panduan) | Tindakan |
|---|---|---|---|
| POS (`34-pos.md`) | Void bill butuh persetujuan | Void bill hanya untuk draf, tanpa persetujuan | Putuskan; perbarui desain atau kode |
| POS (`34-pos.md`) | Tandai menu habis, laporan per jam dan per kategori, ringkasan outlet di penutupan hari, mode offline | Belum ada di layar; panduan tidak menyebutnya | Tandai sebagai "belum dibangun" di desain |
| Banquet (`36-banquet.md`) | Notifikasi `FEE_PROPOSED` | Tidak pernah ditulis | Kode atau desain |
| Banquet (`36-banquet.md`) | Jam kalender dapat diatur | Kalender tetap 06:00–24:00 | Desain |
| Aset (`37-fixed-assets.md`) §18 | Dialog persetujuan pelepasan | Administrator penyewa memposting langsung (sesuai `32-distinct-approver.md` butir D); labelnya tetap **Ajukan pelepasan** (KD-22) | Label di kode |
| Aset (`37-fixed-assets.md`) | Usulan penghapusan dari opname "selalu permintaan" | Berbeda di kode | Desain |
| Audit pasca implementasi (`19-post-implementation-audit.md:215`) | Biaya pembatalan dan no-show belum ada | Sudah ada di layar dan didokumentasikan | Perbarui dokumen desain |
| Paket audit malam (`38-…`) | Arsip "no recipients" | Layar menulis "Tidak dikirim" / "E-mail mati" | Desain |
| `39-…` §P1 dan §P6 | Lihat dokumennya | Berbeda dengan kode | Periksa bersama pemilik |
| Anggaran (`11-…`) | Tahun anggaran dan statistik dijelaskan berbeda | Kode memakai `year_start`, dan statistik anggaran sudah ada | Perbarui desain |
| Anggaran | Label `budget.approve` menyebut "sebagai orang kedua" | Aturan bawaan FREE mengizinkan pengaktif menyetujui sendiri | Label atau aturan bawaan |
| Periode akuntansi | Tutup/buka kembali dengan persetujuan, seperti tahun buku dan rekening koran | Tutup dan buka kembali periode tidak butuh persetujuan | Putuskan |
| Check-out (`03-financial-engines.md`, alur check-out) | Server tidak menagih malam ini untuk tamu yang pulang lebih awal pada siang hari | Server benar; wizard di layar tetap menghitung malam ini (KD-06) | Diperbaiki (KD-06): wizard memakai pratinjau check-out server |

## 5. Fitur setengah jadi dan celah UX

### 5.1 Fitur setengah jadi atau tidak tersambung ke UI

- **Pemesan pada reservasi yang sudah tersimpan.** API `PATCH /reservations/{id}` menerima `guest_id` (`PatchReservationRequest`), tetapi tidak ada layar yang memakainya. Jendela **Ubah reservasi** hanya mengubah perusahaan, permintaan khusus, dan catatan, sedangkan formulir Reservasi baru tidak bisa membuat tamu. Akibatnya, draf yang tersimpan tanpa pemesan tidak pernah bisa dikonfirmasi, dan harus dibuat ulang. Pesan `BOOKER_REQUIRED` ("Pemesan wajib diisi: tamu atau perusahaan.") juga menyesatkan, karena konfirmasi hanya memeriksa tamu (`lifecycle.go:140`). Perusahaan tidak dihitung sebagai pemesan.
- **Profil tamu ganda tidak bisa digabung**, baik di UI maupun di API.
- **Tidak ada tombol menambah kamar** ke reservasi yang sudah ada. Reservasi baru dari layar Reservasi selalu satu kamar.
- **Walk-in** tidak punya langkah deposit dan isian identitas (berbeda dengan check-in biasa).
- **Produk habis (sold out)** hanya bisa ditandai lewat API (`PUT /pos/outlets/{id}/sold-out/{productId}`). Tidak ada tombol di layar POS. Panduan menyarankan menghapus centang **Dijual** untuk sementara.
- **Mengembalikan atau memindahkan beban POS ke kamar** (`POST /pos/room-charges/{id}/return|move`) tidak dipanggil layar mana pun. *Pengembalian diperbaiki (KD-01, KD-02, commit 20faae8): tombol **Kembalikan beban kamar** di bill yang dibuka kembali. Pemindahan ke kamar lain (`move`) masih tanpa layar; untuk sementara kembalikan bebannya, lalu bebankan bill ke kamar yang benar.* Sebelum perbaikan, setelah bill lunas tidak ada jalan di layar untuk membatalkannya. Baris di folio tamu berkode `TRF`, sehingga:
  - **Reverse** disembunyikan (`FolioView.vue:387-388`), dan server menolak dengan `FOLIO_TRANSFER_INVALID` (`charges.go:310`);
  - **Penyesuaian** dengan kode `TRF` ditolak dengan `TRANSFER_CODE_NOT_MANUAL` (`posting.go:247-249`). Penyesuaian dengan kode outlet hanya bisa kalau kode itu sudah terposting di folio tamu (`ADJUSTMENT_EXCEEDS_POSTED`);
  - **Kembalikan** tidak menampilkannya di folio tamu. Di folio bill, `returnable` tidak melihat cakupannya (`transferviews.go:189`), dan pengembalian gagal dengan `FOLIO_CLOSED` karena folio bill sudah tutup.

  **Buka kembali** bill membuka bill lagi, tetapi layar bill tidak punya tombol pengembalian. Di Batch 2, panduan menyarankan Penyesuaian. Saran itu salah dan sudah dibetulkan di Batch 3.
- **Bug: Pax aktual di jendela Posting sesi (banquet).** **Diperbaiki (KD-03, commit 5e805db).** Input `type=number` dengan `v-model` memberi angka, lalu `BanquetPostDialog.vue:57` memanggil `actual.value.trim()`. Akibatnya galat JavaScript, **Ditagih** tetap menunjukkan pax garansi, dan sesi ditagih menurut pax garansi walaupun Pax aktual diisi lebih besar. Terlihat di run screenshot: pax aktual 42 dengan garansi 40 ditagih 40 × 439.230 = 17.569.200. Pax aktual juga tidak punya layar lain (`PUT sessions/{sid}/actual` tanpa UI).
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
  - *Diperbaiki (KD-04, commit 7a2cf30):* **Catat pembayaran dari perusahaan** tidak pernah dialokasikan ke invoice. Formulir sekarang membagi jumlahnya ke invoice terbuka dari yang tertua. Penerimaan yang sudah tercatat sekarang bisa dialokasikan belakangan, dan alokasi yang salah bisa dilepas (KD-27).
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

- **Persediaan, pengaturan tanpa layar:** "pesan tanpa permintaan" dan "terima tanpa pesanan" tidak bisa dinyalakan di UI. Akibatnya **Pesanan baru** (PO tanpa PR) tersembunyi, dan **Terima tanpa pesanan** ditolak dengan "Menerima tanpa pesanan tidak diizinkan di properti ini.".
- **Persediaan, `count_blocks_night_audit`:** pilihan apakah opname terbuka menahan atau hanya memperingatkan audit malam tidak punya kontrol di UI. `InventoryCostingView.vue:146` hanya meneruskannya.
- **Persediaan, draf tagihan:** status "Dibuang" ada, tetapi tidak ada tombol untuk membuang draf.
- **Persediaan, penerimaan:** `delivery_note_date` ada di API, tetapi tidak punya isian (`ReceiptView.vue:66`, `:187`).
- **Persediaan, saldo awal:** posting saldo awal langsung jalan tanpa jendela konfirmasi, padahal hasilnya final. Kunci teks konfirmasinya ada, tetapi tidak dipakai.
- **Persediaan, izin baca master:** peran yang hanya punya `inventory.move` atau `inventory.count` tidak bisa membaca barang dan gudang. Formulir transfer, pemakaian, dan opname tidak bisa memuat daftarnya.
- **Persediaan, nota kredit pemasok:** menautkan nota kredit ke retur butuh `payables.view`. Tanpa izin itu daftar **Nota kredit** kosong.
- **Persediaan, kunci i18n tak terpakai:** `inventory.warehouses.noOutlet`, `inventory.reports.lowStock.created`.
- **Pengaturan, suplemen tempat tidur:** tidak bisa diubah, hanya ditambah. Baris baru dengan tanggal mulai lebih akhir menggantikan yang lama.
- **Pengaturan, fasilitas kamar:** tidak ada layar fitur atau amenities kamar, juga tidak ada isiannya di formulir kamar.
- **Pengaturan, tanpa hapus:** properti, pengguna, peran, tipe kamar, kamar, pajak, biaya servis, kode biaya, dan paket tarif hanya bisa dinonaktifkan. Sebagai aturan ini wajar.
- **Pengaturan, masuk dua langkah:** staf hotel tidak punya autentikator. Hanya console platform yang punya.
- **Pengaturan, catatan baca-saja:** catatan "Peran Anda … hanya dapat melihat …" sulit dicapai. Menu disembunyikan tanpa izin, jadi pengguna hanya melihatnya kalau mengetik alamatnya.
- **Pengaturan, `DepartmentSelect` kosong:** peran yang boleh mengubah kode biaya, tetapi tidak boleh membaca departemen, melihat isian departemen kosong. Isiannya tampak rusak.
- **Console platform, akun operator:** operator tidak bisa mengganti kata sandinya sendiri atau mereset autentikatornya dari console. Hanya operator lain di server yang bisa.
- **Console platform, console mati:** tanpa rahasia platform, halaman masuk menampilkan "console is disabled", tanpa langkah untuk pembaca.
- **Audit malam, kesiapan akuntansi:** `GET {P}/accounting/readiness` tidak punya layar (`internal/accounting/http.go:33`). Audit malam menampilkan penghalangnya mentah. `ACCOUNTING_NOT_SET_UP`, `ACCOUNTING_NOT_STARTED`, dan `JOURNAL_SEQUENCE_MISSING` tidak bisa dibereskan staf hotel dari layar mana pun. Lihat juga butir Akuntansi (tanggal mulai tanpa layar).
- **Audit malam, kirim ulang paket:** server menjawab penerima mana yang dilewati (`auditpack/mail.go:85-96`), tetapi layar mengabaikannya. Kunci `nightAuditArchive.resendSkipped` tidak dipakai.
- **Audit malam, no-show tidak bisa dibatalkan:** tidak ada pemulihan untuk baris NO_SHOW. Pemulihan hanya untuk reservasi batal (`internal/reservations/lifecycle.go:361`). Mungkin memang begitu desainnya, tetapi tidak ada layar yang menyebutnya.
- **Audit malam, kunci i18n tak terpakai:** `nightAudit.packLoading`, `nightAudit.needsAttention`, `nightAudit.done`, `nightAuditArchive.onClose`, `audit.more`, `dashboard.manager.title`.
- **Laporan, filter server tanpa UI:** pembatalan `company_id` (`reports/cancellations.go:151-158`), koreksi `user_id` / `outlet_id` (`corrections.go:140-149`), jejak audit `entity_id` (`audit/auditlog.go:267`).
- **Laporan, forecast dan pickup:** belum ada di halaman Laporan. Desainnya tahap 2 (doc 38 bagian 11).
- **Aset tetap, fitur tanpa layar:** pindah banyak aset sekaligus (`POST {P}/moves`), `rebuild-state`, jadwal penyusutan satu aset (`asset_id` hanya dari query URL, tanpa pemilih), dan `lookup` (hanya dipakai Perawatan).

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

- **Persediaan, Harga pokok, antrean penjualan POS:** jumlah tampil mentah, `× {{ r.quantity }}` (teks desimal dari API, bukan `formatQty`) (`InventoryCostingView.vue:243`).
- **Persediaan, Terima barang dan Transfer baru:** kotak baca-saja **Tanggal bisnis** menampilkan tanggal ISO `2026-11-25` (gambar inventaris-terima-03 dan inventaris-sr-07).
- **Persediaan, Transfer baru:** kolom **Stok tujuan** tanpa satuan (`18,4`, `60`, `5`), sedangkan **Stok asal** bersatuan (gambar inventaris-sr-07).
- **Persediaan, penerimaan yang dibalik:** alasannya tampil di bawah teks instruksi panjang `inventory.receipt.reverseReason`, bukan label pendek "Alasan". Kunci yang sama dipakai sebagai keterangan jendela (`ReceiptView.vue:343`).
- **Persediaan, pesanan pembelian:** lencana baris yang ditutup berisi kalimat panjang, jadi sempit dan sulit dibaca (`PurchaseOrderView.vue`).
- **Pengaturan, Pajak & biaya layanan:** kolom **Tarif** memakai titik desimal, `12.5%`, bukan `12,5%` (`RateItemsSection.vue:182`).
- **Pengaturan, Kode biaya:** ringkasan, baris aturan, dan pilihan menampilkan `SC 10%`, `PB1 · 12.5%` dengan titik (`ChargeCodesView.vue:75`, `:103`, `:137`, `:141`, `:287`, `:310`).
- **Pengaturan, Kalkulator kode biaya:** baris "Jumlah yang ditawarkan" menampilkan jumlah dan harga mentah, misalnya `1 × 350000` (`ChargeCalculator.vue:96`).
- **Pengaturan, Aturan yield:** tanggal tampil `2026-12-24` dan jumlah `+50000` tanpa pemisah ribuan (`YieldRulesView.vue:93`, `:103`).
- **Pengaturan, Aturan yield, Cek harga:** kolom **Terisi** tampil `2.00%` (titik desimal, dua angka di belakang koma) (gambar pengaturan-yield-07).
- **Pengaturan, Suplemen tempat tidur:** **Berlaku mulai** berupa tanggal ISO, jumlah `+50000` / `+10%` tanpa format (`BedSupplementsView.vue:47`, `:52-53`).
- **Pengaturan, Grid tarif dan grid pembatasan:** judul kolom `dd/mm` dirakit sendiri, bukan lewat format tanggal (`RateGridView.vue:52`, `RestrictionsView.vue:55`).
- **Pengaturan, Sistem:** "Terakhir diperiksa" memakai jam format browser (`toLocaleTimeString()`), bukan bahasa aplikasi (`SystemView.vue:48`).
- **Audit malam, Arsip:** pemberitahuan setelah **Bangun ulang** berbunyi "Paket 2026-11-23 dibangun ulang." (`NightAuditArchiveView.vue:180`). Toast setelah **Kirim ulang** juga memakai tanggal ISO, "{n} e-mail diantre untuk 2026-11-23." (`:125`).
- **Laporan, Ringkasan harian:** nilai "Tanggal bisnis" tampil ISO `2026-11-23`, karena kolom nilainya diformat sebagai uang (`reportDefs.ts:48`, `ReportsView.vue:167`).
- **Laporan, Okupansi dan statistik:** "Okup. %" diformat sebagai uang, tanpa tanda `%`. Jumlah kamar juga lewat format uang (`reportDefs.ts:91-93`, `ReportsView.vue:167`).
- **Laporan, Kamar yang belum bersih:** kolom "Sejak (UTC)" berupa cap waktu UTC mentah (`reportDefs.ts:153`).
- *Diperbaiki (commit 2c564f5):* **Laporan, Pembatalan dan no-show; Laporan Z:** "Nilai booking" dan "Pay-in/out" (beserta baris kakinya) dijumlahkan sebagai float JavaScript, jadi bisa muncul sisa pembulatan (`CancellationsView.vue:37`, `ZReportsView.vue:66`). Sekarang dijumlahkan dalam satuan perseribu dengan `sumAmounts` yang sudah ada (`views/budget/budgetMath.ts`), termasuk baris kaki Laporan Z. Test: `DayReports.test.ts` (0.1 + 0.2 tampil 0.3, 0.3 + -0.1 tampil 0.2). Tidak ada gambar yang berubah, karena hotel contoh memakai rupiah tanpa desimal.
- *Diperbaiki (commit ccd43af):* **Piutang perusahaan, Buat invoice:** total pembayaran yang dicentang (`pickedTotal`) dan kolom "Sudah dipakai" (`credited + written_off`) dijumlahkan sebagai float (`CityLedgerAccountView.vue:365,691`). Sekarang dijumlahkan dalam satuan seperseribu dengan `sumAmounts`, seperti Laporan Z dan Pembatalan. Hanya tampilan.
- **Aset tetap, Pengaturan:** bulan tahun pajak tampil sebagai angka 1 sampai 12 (`AssetSettingsView.vue:311`). Isian tarif fiskal tampil dengan desimal seperti `25.000`.
- **Aset tetap, Tambah nilai:** pratinjau menghitung uang dengan `Number()` float (`AssetAddValueDialog.vue:82-83`). Hanya tampilan.

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

- **Persediaan, kotak masuk:** intro **Perlu saya setujui** hanya menyebut "Permintaan pembelian…", padahal daftarnya juga berisi permintaan barang (SR) (`id.ts:5999`, `inventory.inbox.intro`).
- **Persediaan, beranda:** KPI "Dokumen menunggu keputusan saya" membuka `/inventory/purchasing`, bukan kotak masuk `/inventory/approvals` (`InventoryHomeView.vue:75`).
- **Persediaan, Barang di bawah minimum:** di samping **Buat PR** ada tombol kedua, **Buat permintaan pembelian**. Layar tidak menjelaskan bedanya (gambar inventaris-laporan-04).
- **Pengaturan, Pengguna:** menyimpan pengguna adalah dua permintaan, profil (PATCH) lalu baris akses (PUT). Kalau yang kedua gagal, profil sudah tersimpan, tetapi aksesnya tidak. Layar menampilkan galat, jadi pembaca mengira tidak ada yang tersimpan (`UserFormView.vue:63-68`).
- **Pengaturan, galat isian hanya di kotak merah atas:** Pembatasan penjualan (`set.*`), Aturan persetujuan (`rules[i].*`), dan aturan pajak kode biaya (`taxes[i].*`) tidak menampilkan galat di bawah isiannya.
- **Pengaturan, daftar Peran:** galat daftar tampil sebagai alert biasa, bukan pemberitahuan galat yang biasa.
- **Pengaturan, formulir Peran:** **Batal** saat ada perubahan tetap bertanya "keluar tanpa menyimpan?", walaupun pengguna sengaja mengklik Batal (`RoleFormView.vue`).
- **Audit malam, `INVENTORY_QUEUE_FAILED`:** tautan **Buka** menuju `/inventory`, padahal teksnya menyebut "Persediaan > Harga pokok" (`/inventory/costing`) (`NightAuditView.vue:85`).
- **Audit malam, no-show massal:** kotak centang kedatangan dan **Pilih semua** tampil untuk pengguna tanpa `nightaudit.no_show`. Hanya formulirnya yang disembunyikan (`NightAuditView.vue:363-366`).
- **Audit malam, no-show massal:** pengguna dengan `nightaudit.no_show` tetapi tanpa `nightaudit.run` tidak bisa mencapai formulirnya, karena halamannya butuh `run` (`NightAuditView.vue:305`).
- **Audit malam, no-show massal berhasil:** pemberitahuan suksesnya memakai gaya peringatan kuning (`alert warning`), jadi tampak seperti masalah (`NightAuditView.vue:303`).
- **Audit malam, kartu hasil:** dua tautan **Buka arsip** yang sama di strip paket (`pack-mail-link` dan `pack-archive`) (`NightAuditView.vue:337`, `:341`).
- **Audit malam, no-show satu baris ditolak:** `LINE_NOT_CONFIRMED` menampilkan "Hanya kamar yang terkonfirmasi yang bisa check-in." (`internal/reservations/lifecycle.go:450`).
- **Laporan, tanggal tanpa audit:** `BUSINESS_DAY_NOT_FOUND` "Tanggal bisnis untuk posting ini tidak ada." juga dipakai untuk tanggal laporan dan paket, misalnya Guest ledger dan Ledger deposit sebelum audit malam pertama. Kata "posting" menyesatkan (`reports/ledgers.go:150`, `flash.go:135`).
- **Laporan, rentang terlalu panjang:** `OUT_OF_RANGE` punya dua teks Indonesia, "Nilai di luar rentang yang diizinkan." dan "Di luar batas yang diizinkan." (`id.ts:8512`). Rentang lebih dari 366 hari bisa memunculkan yang mana saja.
- **Aset tetap, Batalkan aset:** tombol tampil untuk setiap aset ACTIVE (`AssetView.vue:292`). Server menolak aset yang sudah disusutkan atau diubah, juga yang pelepasannya menunggu.
- **Aset tetap, Ubah data:** tombol tetap tampil saat pelepasan menunggu persetujuan (`AssetView.vue:291`).
- **Aset tetap, Opname, Tandai rusak:** catatan tidak ditandai wajib (`AssetAuditView.vue:212`), padahal server mewajibkannya (`audit.go:319-321`). Galatnya muncul sebagai toast halaman, bukan di jendela.
- **Aset tetap, batal pelepasan:** jendela persetujuan selalu meminta e-mail dan kata sandi, juga di aturan FREE, padahal server mengabaikannya.
- **Aset tetap, daftar tunggu:** catatan "Menambah nilai … menyusul di rilis berikutnya." sudah basi, karena **Tambah ke aset** sudah ada (`AssetWaitingView.vue:211`).
- **Aset tetap, Saldo awal aset:** petunjuk **Tanggal jurnal** berbunyi 'Tidak lewat dari hari ini. Kosong berarti tanggal "per".' (`id.ts:6786`). Batasnya sebenarnya tanggal bisnis, bukan "hari ini". Kalau kosong, server memakai tanggal "per" + 1 hari, dimajukan ke awal buku dan dibatasi tanggal bisnis.
- **Aset tetap, tutup periode di Akuntansi:** teks `PERIOD_NOT_READY` (`id.ts:8222`) tidak menyebut penyusutan. Pengguna yang menutup bulan tanpa menjalankan penyusutan hanya melihat alasan umum.

## 6. Catatan screenshot

- **Kode properti `E2E`.** Pemilih properti menampilkan "E2E · Hotel Kamara Ubud", dan riwayat tamu menampilkan `E2E`. Kode itu dibuat oleh setup E2E bersama (`web/e2e/setup/seed.setup.ts`), yang juga dipakai test lain. Kode properti juga tidak bisa diubah setelah dibuat. Jadi kode itu dibiarkan. Hanya nama properti yang diubah oleh `web/e2e/docs/world.ts`.
- **Cara membuat ulang gambar:** `E2E_DOCS=1 E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh docs-screenshots.spec.ts`. Nama file selalu sama, jadi run baru menimpa gambar lama.

- **Nama dan data test di gambar.** Selain kode properti `E2E`, gambar menampilkan "Admin E2E" (inventaris-rantai-05, -rantai-08, -sr-05, -opname-06), "Hadi Kepala" dan "Fajar Final" (inventaris-rantai-08, -sr-05), dan `admin@e2e.example`. Daftar peran berisi 10 peran test "E2E Akuntansi … E2E Supervisor FO" berketerangan `E2E` (pengaturan-peran-05). Daftar pengguna menampilkan `staf.aset@demo.test`, domain `.test`, bukan `.example` (pengaturan-pengguna-01). Pertimbangkan mengganti nama admin dan peran itu di `web/e2e/docs/world.ts`.
- **Tanggal 24 Nov, padahal tanggal bisnis 25 Nov.** Kolom **Diajukan** di inventaris-rantai-05 dan -rantai-08, serta rantai di inventaris-sr-05, menampilkan 24 Nov 2026. Opname di gambar yang sama menampilkan "25 Nov 2026 05:49". Ini tampaknya tanggal dari waktu UTC, bukan waktu hotel. Pembaca bisa bingung. Perlu dicek di UI, bukan di flow.
- **Teks bertumpuk di bawah isian tanggal.** `DateInput` menggambar baris nama hari dengan `absolute top-full` (`DateInput.vue:201`), sedangkan petunjuk `FormField` mengalir di bawahnya. Di pengaturan-properti-05, "Rabu, 25 Nov 2026" menimpa petunjuk "Hari ini di properti adalah 25 Nov 2026; …" di **Tanggal bisnis pembukaan**, dan keduanya tidak terbaca. Usulan: `compact` pada `DateInput` di `PropertyFormView.vue:322` (belum diterapkan). Hal yang sama terlihat di aset-saldoawal-03: "Sabtu, 31 Okt 2026" menimpa petunjuk **Per tanggal (akhir bulan)** (`AssetOpeningView.vue:182`), dan "Selasa, 2 Jan 2024" di **Mulai pakai** menempel pada "Kolom lain" (`:212`, `:216`). Ini bug tampilan, jadi gambarnya diambil ulang setelah UI dibetulkan.
- **Nilai stok perlu dicek sebelum terbit.** Kartu Ringkasan menampilkan Nilai stok 64.958.332 (inventaris-laporan-01). Baris akun di laporan Nilai stok berjumlah 62.102.532 (inventaris-laporan-03). Ini catatan data, bukan cacat gambar.
- **Dua biaya servis yang sama.** pengaturan-pajak-01 menampilkan `SVC` "Biaya layanan" 10% tanpa akun dan `SC` "Biaya layanan" 10% dengan akun 2430. Halaman hanya menyebut `SC`. Nonaktifkan atau ganti nama `SVC` di dunia gambar (`world.ts`), atau beri satu kalimat di halaman.
- **Langganan.** Kalimat "Untuk memperpanjang atau mengganti paket, hubungi: …" tidak tampil di pengaturan-langganan-01, kemungkinan karena dunia E2E tidak punya kontak penagihan. Bilah atas juga menampilkan "Audit malam · 2 tertunda".
- **Settlement kartu tanpa data di dunia gambar.** Layar **Void settlement** di [settlement-kartu.md](08-keuangan-back-office/settlement-kartu.md) (kolom status, tombol **Void settlement**, dan settlement yang **Dibatalkan**) belum punya gambar. Hal yang sama berlaku untuk formulir **Selesaikan pembayaran kartu** di halaman yang sama. Penyebabnya, data docs (`web/e2e/docs/world.ts`) belum memuat settlement kartu. Kedua tanda ini termasuk 11 gambar yang belum ada (bagian 8). Usulan: tambahkan satu settlement kartu di world docs (pembayaran kartu dari hari yang sudah ditutup, rekening koran dengan baris pencairannya, satu settlement yang dicatat, dan satu yang di-void) pada putaran screenshot berikutnya.
- **Alokasi penerimaan tanpa data di dunia gambar (KD-27).** Kartu **Alokasikan penerimaan** dan daftar alokasi dengan **Lepas** di [invoice-dan-penerimaan.md](06-corporate-group-city-ledger/invoice-dan-penerimaan.md) belum punya gambar: data docs (`web/e2e/docs/world.ts`) belum punya penerimaan dari hari yang sudah ditutup dengan sisa yang belum dialokasikan, dan invoice yang belum lunas. Kedua tanda ini termasuk 11 gambar yang belum ada (bagian 8). Usulan: pada putaran screenshot berikutnya, catat di world docs satu penerimaan tanpa alokasi sebelum audit malamnya, lalu ambil gambar alokasi dan pelepasannya. Layarnya diuji oleh Vitest dan E2E NA-07, tetapi belum dilihat dalam gambar.
- **Console platform tanpa dunia E2E.** Tidak ada operator dan rahasia autentikator di E2E, jadi hanya halaman masuk yang punya gambar (platform-login-01). Langkah lain masih placeholder `<!-- SCREENSHOT -->`.
- **Penyusutan aset tidak bisa dijalankan di E2E.** Buku mulai bulan berjalan, jadi bulan pertama (November 2026) belum berakhir, dan `depr-run` nonaktif (`depreciation.go:915`). Gambar hanya bisa menunjukkan pratinjau, bukan "Dijalankan" atau pembalikannya. Peran E2E juga tidak punya izin `asset.*`, jadi semua gambar aset diambil sebagai admin tenant. Alur "butuh orang kedua" untuk pelepasan baru tidak bisa diperlihatkan.
- **Elemen di bawah bilah atas.** `shot()` sekarang menggulir elemen yang tertutup bilah atas lengket ke bawah bilah (`web/e2e/docs/shot.ts`, `TOP_BAR = 64`). Penyebab gambar pengaturan-peran-03 dan pengaturan-persetujuan-03 yang terpotong.

## 7. Kandidat video

Lima belas tugas yang paling terbantu oleh video pendek. Halaman-halamannya sudah diberi tanda `> 🎬 Video tutorial: menyusul` di bawah judulnya. Tidak ada video yang dibuat di branch ini.

| No | Halaman | Peran | Kenapa video membantu | Perkiraan durasi |
|---|---|---|---|---|
| 1 | [Masuk dan mengenal layar](00-memulai/login-dan-navigasi.md) | Semua staf | Orientasi layar, menu, dan pemilih properti lebih cepat ditangkap dengan melihat | 2 menit |
| 2 | [Membuat reservasi](01-front-desk/membuat-reservasi.md) | Resepsionis | Formulir panjang dengan banyak pilihan tarif dan tipe kamar | 4 menit |
| 3 | [Check-in](01-front-desk/check-in.md) | Resepsionis | Tugas harian dengan urutan wizard yang harus tepat | 3 menit |
| 4 | [Walk-in](01-front-desk/walk-in.md) | Resepsionis | Gabungan reservasi, check-in, dan deposit dalam satu alur | 3 menit |
| 5 | [Check-out](01-front-desk/check-out.md) | Resepsionis, kasir | Tugas harian yang menyangkut uang; termasuk tamu yang pulang lebih awal | 3 menit |
| 6 | [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md) | Kasir | Buka shift, hitung kas, tutup shift, dan laporan Z | 4 menit |
| 7 | [Menerima pembayaran](03-kasir-dan-pembayaran/menerima-pembayaran.md) | Kasir | Metode pembayaran, deposit, dan pembayaran terpisah | 3 menit |
| 8 | [Mengoreksi biaya: reverse dan penyesuaian](03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md) | Kasir, supervisor | Beda reverse dan penyesuaian, serta persetujuan atasan, sering membingungkan | 4 menit |
| 9 | [Mengubah status kebersihan kamar](02-housekeeping/mengubah-status-kebersihan.md) | Housekeeping | Dipakai dari ponsel di lantai kamar; gerakan layar lebih mudah ditiru | 2 menit |
| 10 | [Mengambil pesanan](04-pos/mengambil-pesanan.md) | Kasir dan pelayan outlet | Alur meja, bill, varian, dan kirim ke dapur | 4 menit |
| 11 | [Membayar bill](04-pos/membayar-bill.md) | Kasir outlet | Bayar, pisah bill, dan bebankan ke kamar | 3 menit |
| 12 | [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md) | Night auditor | Tugas sekali sehari yang menutup hari dan tidak bisa diulang | 5 menit |
| 13 | [Membuat acara dan sesi](05-banquet/membuat-acara-dan-sesi.md) | Staf banquet | Acara, sesi, ruang, dan paket dalam beberapa layar | 5 menit |
| 14 | [Permintaan pembelian dan pesanan pembelian](09-inventory/permintaan-dan-pesanan-pembelian.md) | Staf gudang, pembelian | Permintaan, rantai persetujuan, lalu pesanan ke pemasok | 5 menit |
| 15 | [Invoice dan penerimaan piutang perusahaan](06-corporate-group-city-ledger/invoice-dan-penerimaan.md) | Akuntan piutang | Invoice dan pembayaran perusahaan; termasuk cara mencatat pembayaran ke invoice (KD-04) | 4 menit |

Saran perekaman: pakai hotel contoh dari run screenshot (`E2E_DOCS=1 … scripts/e2e.sh docs-screenshots.spec.ts`), layar 1440×900 untuk meja depan dan back office, serta layar ponsel untuk housekeeping. Sebutkan nama menu persis seperti di layar berbahasa Indonesia.

## 8. Ringkasan akhir (Fase 6)

| Pemeriksaan | Hasil |
|---|---|
| Halaman panduan | 123 (termasuk README, FAQ, troubleshooting, glosarium, alur harian, 12 panduan cepat) |
| Tautan dan anchor | 1.883 tautan relatif diperiksa, 0 rusak; tautan menunggu halaman di `_inventory.md`: kosong |
| Screenshot | 256 gambar, 12,0 MB (rata-rata 45,5 KB, terbesar 180 KB), semua terpakai; tidak ada halaman dengan lebih dari 3 gambar |
| Gambar yang belum bisa diambil | 11 tanda `<!-- SCREENSHOT: ... -->` di 7 halaman: surat peringatan, tutup periode, faktur pajak, settlement kartu (2, termasuk layar Void settlement dari KD-05), alokasi penerimaan (2, KD-27), penghalang dan peringatan audit malam (2), console platform (2). Alasannya tertulis di setiap tanda dan di bagian 6 |
| Cakupan route | 211 route; sebelum empat halaman baru, 199 tercakup penuh. Setelah halaman Kasir, Pelayan, Laporan banquet, dan Pengaturan aset ditambahkan, yang tersisa hanya laporan per-ruang, produksi penjualan, dan acara hilang, yang dijelaskan sebagai satu tabel di halaman Laporan banquet |
| Sampel 10 halaman terhadap kode | 7 benar, 3 benar dengan catatan; 5 perbaikan diterapkan (pindah kamar, void/komplimen POS ×2, tentatif banquet, aturan persetujuan) |
| Pemeriksaan kode | `npm run e2e:types` dan `npm run type-check` lolos; semua berkas di `docs/user-guide` dan `web/e2e/docs` ber-line ending LF |
| 🐞 Kendala yang diketahui | **28** (KD-01 sampai KD-28); 10 berdampak uang. KD-26 ditambahkan saat review PR #106, KD-27 saat memperbaiki KD-04, KD-28 saat memperbaiki KD-10 |
| PERLU VERIFIKASI | **0** |
| Harus diisi pemilik sebelum dibagikan | 2 placeholder di README, belum diisi (lihat bagian "Harus diisi sebelum panduan dibagikan") |
