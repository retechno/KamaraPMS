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

## POS (`docs/architecture/34-pos.md`)

| File | Isi | Tahap POS |
|---|---|---|
| `10-pos-denah-meja.dc.html` | Kasir: denah meja visual per area (status kosong, terisi, menunggu bayar, dipesan), pemilih outlet, status shift, daftar bill terbuka | 2 |
| `11-pos-pesanan.dc.html` | Kasir: grid menu + varian/tambahan/catatan dapur; bill dengan item dikirim ke dapur (status), void dicoret dengan penyetuju, draf; total dengan servis dan PB1 | 2 |
| `12-pos-pembayaran.dc.html` | Pembayaran campuran, bebankan ke kamar dengan status kunci folio dan sisa batas, keypad | 2 |
| `13-pos-buka-folio-outlet.dc.html` | Resepsionis: buka folio untuk beban outlet (batas, berlaku sampai, outlet yang boleh) + daftar beban outlet masuk | 1 |
| `14-pos-layar-dapur.dc.html` | Layar dapur (KDS, tema gelap): Baru / Dimasak / Siap diambil per stasiun | 3 |
| `15-pos-master-produk.dc.html` | Master produk: kode biaya, stasiun, harga per outlet, varian, tambahan, pajak & servis, resep (menunggu inventory) | 1 |
| `16-pos-pelayan-ponsel.dc.html` | Pelayan di ponsel (390 px): menu per kategori, Tambah / pengatur jumlah, Kirim ke dapur & bar | 3 |

Contoh angka di mockup POS: subtotal 375.000, servis 10% 37.500, PB1 10% dari subtotal + servis 41.250, total 453.750. Di aplikasi semua dihitung oleh ChargeCalculationEngine.

## Inventory (`docs/architecture/35-inventory.md`)

Pola layar: halaman daftar selebar layar (tabel, chip filter, tombol "Baru") lalu halaman dokumen selebar layar (form + ringkasan di kanan, tautan kembali). Tidak ada kolom daftar di samping dokumen. Pengecualian: Barang & stok memakai panel samping yang bisa ditutup.

| File | Isi | PR inventory |
|---|---|---|
| `17-inv-barang-stok.dc.html` | Daftar barang, stok per gudang, nilai, penanda di bawah minimum / minus; panel barang (satuan, harga pokok per gudang, kartu stok singkat) | 1 |
| `18-inv-pembelian-daftar.dc.html` | Daftar pembelian: tab PR, PO, penerimaan; filter "Perlu saya setujui"; kolom tahap persetujuan | 2 |
| `19-inv-permintaan-pembelian.dc.html` | PR: barang, stok sekarang, perkiraan harga, supplier usulan; persetujuan 3 tingkat; pratinjau PO per supplier | 2 |
| `20-inv-penerimaan.dc.html` | Penerimaan dari PO: harga dari PO (tidak bisa diubah), terima sebagian, "Terima dan tagih", terima tanpa PO | 2 |
| `21-inv-draf-tagihan.dc.html` | Draf tagihan, cocok 3 arah, pembagian selisih harga (nilai stok / selisih harga beli), persetujuan di atas toleransi, baris non-stok | 2 |
| `22-inv-permintaan-gudang-daftar.dc.html` | Daftar permintaan barang ke gudang (SR) dan transfer; kolom persetujuan 2 tingkat | 3 |
| `23-inv-transfer.dc.html` | Transfer menjawab SR: stok asal, kirim sebagian, gudang asal tidak boleh minus | 3 |
| `24-inv-pemakaian-daftar.dc.html` | Daftar pemakaian & waste, ringkasan biaya per departemen bulan ini | 3 |
| `25-inv-pemakaian.dc.html` | Pemakaian departemen (contoh housekeeping: cairan pembersih, amenities), akun beban per baris, jurnal | 3 |
| `26-inv-opname-ponsel.dc.html` | Opname di ponsel (390 px): hitung buta, jam dihitung per barang, gudang dibekukan, POS tetap jalan | 3 |
| `27-inv-resep.dc.html` | Tab resep di master produk POS: bahan per porsi, varian mengganti / tambahan menambah, food cost % | 4 |
| `28-inv-harga-pokok.dc.html` | Proses harga pokok: rata-rata bergerak vs FIFO dengan data yang sama, metode terkunci, stok minus dan koreksinya, jurnal | 1 |
| `29-inv-laporan-fb-cost.dc.html` | F&B cost per outlet: teori (resep) vs aktual (opname), cocok dengan buku besar | 5 |
| `30-inv-fb-reconciliation.dc.html` | F&B cost reconciliation: stok awal + pembelian ± transfer biaya food/beverage − stok akhir − makan karyawan & entertainment | 5 |
| `31-inv-daily-cost.dc.html` | Daily cost semua biaya: per hari, HPP F&B dan F&B %, waste, makan karyawan & entertainment, amenities per kamar terisi, engineering & ATK, selisih opname & harga, total; tampilan F&B saja dan per departemen | 5 |
| `32-inv-amenities-paket.dc.html` | Pengaturan amenities otomatis: paket bawaan per kamar / per tamu, tambahan per tipe kamar | 3 |
| `33-inv-amenities-malam.dc.html` | Dokumen amenities otomatis satu malam (dibuat tutup hari): kamar terisi, tamu, biaya, stok kurang jadi minus | 3 |
| `34-inv-alur-persetujuan.dc.html` | Alur persetujuan: PR 3 tingkat (kepala dept, cost control, akhir di atas batas), SR 2 tingkat | 2 |

Contoh angka inventory saling cocok antarlayar: sirup di Bar 28,2 botol / 1.393.080 (rata-rata 49.400); F&B September food 31,1% aktual vs 28,3% teori, beverage 25,0% vs 22,0%; amenities 3 Sep 78 kamar, 142 tamu, 1.895.640.

## Banquet (`docs/architecture/36-banquet.md`)

Pola layar sama dengan Inventory: daftar selebar layar lalu halaman acara selebar layar dengan tab dan ringkasan di kanan. Dialog digambar di atas layar yang diredupkan.

| File | Isi | PR banquet |
|---|---|---|
| `35-bq-kalender-harian.dc.html` | Kalender ruang harian: ruang × jam, Definit, opsi 1, opsi 2+ (garis putus), arsiran persiapan/bongkar, bagian yang terkunci oleh ruang gabungan, sesi lewat tengah malam | 1 |
| `36-bq-kalender-mingguan.dc.html` | Kalender ruang mingguan: ruang × hari, hari ini disorot | 1 |
| `37-bq-acara-daftar.dc.html` | Daftar acara: chip status dengan jumlah, sales, estimasi neto, kolom "perlu perhatian" (tanggal opsi lewat, deposit terlambat, sesi belum diposting) | 1 |
| `38-bq-acara-hari-ini.dc.html` | Acara hari ini dan sesi belum diposting dari hari sebelumnya: garansi, aktual, ditagih, status BEO, tombol posting | 2 |
| `39-bq-acara-sesi.dc.html` | Halaman acara, tab Sesi: pax perkiraan/garansi/aktual, tambah sesi dengan cek ketersediaan, ringkasan pemesan dan uang, transfer ke folio master grup | 1 (uang: 2) |
| `40-bq-acara-item-paket.dc.html` | Tab Item & paket: paket nett dipecah ke komponen dan akun, tambah item ++ dengan diskon di atas batas (perlu persetujuan) | 2 |
| `41-bq-acara-deposit.dc.html` | Tab Deposit & kebijakan: jadwal deposit kumulatif, deposit diterima, tingkat pembatalan dengan tingkat yang berlaku disorot | 2 |
| `42-bq-beo.dc.html` | BEO revisi: bagian per departemen, "berubah sejak revisi X", tandai sudah dibaca, riwayat revisi | 3 |
| `43-bq-konfirmasi.dc.html` | Dialog Konfirmasi (Definit): memakai folio dari "Terima DP", opsi lain yang kehilangan ruang, template kebijakan dan jadwal deposit, mode posting | 1 (uang: 2) |
| `44-bq-pindah-sesi.dc.html` | Dialog Pindah sesi Tentative: peringkat di slot lama dan baru, siapa yang naik | 1 |
| `45-bq-batal.dc.html` | Dialog Batalkan acara Definit: tingkat menurut hari, biaya (4510) atau hangus (4520), sisa deposit dikembalikan atau tetap | 2 |
| `46-bq-posting-sesi.dc.html` | Dialog Posting sesi: pax ditagih = maks(garansi, aktual), baris folio per komponen, pelepasan deposit, potong stok | 2 (stok: 4) |
| `47-bq-ruang.dc.html` | Master ruang acara dan ruang gabungan: kapasitas per setup, persiapan/bongkar, nonaktif | 1 |
| `48-bq-paket.dc.html` | Master paket: harga per pax, mode Nett/++, komponen dengan bobot, contoh pemecahan 100 pax, resep dari produk POS | 1 (resep: 4) |
| `49-bq-pengaturan.dc.html` | Pengaturan: Tentative bentrok (tolak/antrean), mode harga, posting otomatis/manual, batas diskon, gudang, jam operasi; template kebijakan (neto/gross) dan jadwal deposit; penerima BEO | 1 (uang: 2) |
| `50-bq-laporan-forecast.dc.html` | Forecast pendapatan Definit vs Tentative per bulan dan jenis acara, konversi, sesi belum diposting | 4 |

Contoh angka banquet saling cocok antarlayar dan dengan dokumen 36 bagian 10: paket Fullday Meeting 363.000 nett × 100 pax = 36.300.000 per sesi (neto 30.000.000, servis 3.000.000, pajak 3.300.000); BQ-000123 dua sesi, total kontrak 72.600.000, deposit 30/50/20 = 21.780.000 / 36.300.000 / 14.520.000; pembatalan 20 hari sebelum: 50% × 30.000.000 = 15.000.000, sisa deposit 14.040.000.

## Aset tetap (`docs/architecture/37-fixed-assets.md`)

Bagian dari modul Inventory (A17). Pola layar sama: daftar selebar layar lalu halaman dokumen; dialog di atas layar yang diredupkan; opname di ponsel 390 px.

| File | Isi | PR aset |
|---|---|---|
| `51-aset-register.dc.html` | Register aset: kategori, lokasi dan departemen, harga, akumulasi, nilai buku, sisa umur, status; tanah, aset dalam pembangunan, batch OE, aset lama yang habis disusutkan | 1 |
| `52-aset-halaman.dc.html` | Halaman satu aset: data, nilai, kelompok fiskal; jadwal penyusutan komersial vs fiskal per tahun dengan koreksi; riwayat | 1 (jadwal: 2, fiskal: 4) |
| `53-aset-daftar-tunggu.dc.html` | Daftar tunggu "Jadikan aset" dari setiap baris jurnal berakun harga perolehan (tagihan, kas/kasir, bank, manual), batas kapitalisasi, "Bukan aset" | 2 |
| `54-aset-jadikan-aset.dc.html` | Dialog Jadikan aset: satu baris jadi beberapa unit, bulan mulai penyusutan per aset (A8), tanpa jurnal baru | 2 |
| `55-aset-saldo-awal.dc.html` | Saldo awal aset lama: CSV, mode dengan jurnal atau register saja, kolom fiskal, aset yang sudah habis disusutkan, pratinjau jurnal ke 3900 | 1 |
| `56-aset-penyusutan.dc.html` | Penyusutan bulanan: bulan berurutan, pratinjau jurnal per kategori dan departemen, hambatan | 2 |
| `57-aset-pelepasan.dc.html` | Dialog pelepasan: dijual atau dihapus, laba/rugi 7420, jurnal, persetujuan ASSET_DISPOSAL, catatan PPN | 3 |
| `58-aset-kategori-pengaturan.dc.html` | Kategori dengan akun, metode, umur, departemen bawaan, kelompok fiskal; pengaturan properti (bulan mulai, aturan OE, batas kapitalisasi, register mulai) | 1 |
| `59-aset-rekap-kategori.dc.html` | Rekap per kategori: awal, tambah, kurang, penyusutan, akhir, nilai buku, dicocokkan dengan buku besar dan selisihnya | 4 |
| `60-aset-koreksi-fiskal.dc.html` | Koreksi fiskal per aset untuk satu tahun pajak | 4 |
| `61-aset-label-qr.dc.html` | Cetak label QR (A4 atau printer label); QR berisi kode, bukan URL (gambar QR di mockup hanya pola contoh) | 1 |
| `62-aset-opname-ponsel.dc.html` | Opname aset di ponsel: pindai lokasi dan aset, ditemukan / lokasi lain / rusak / hilang menjadi usulan | 4 |

Contoh angka aset saling cocok antarlayar: register 15 aset, harga 9.208.800.000, akumulasi 899.483.334, nilai buku 8.309.316.666 per 31 Okt 2026; penyusutan Oktober 16.091.667; rekap FF&E selisih 186.800.000 = genset dan lampu taman di daftar tunggu; pelepasan sofa lobi 36.000.000 / 21.000.000 dijual 18.000.000, laba 3.000.000 (37 bagian 10.6); aset lama lemari kayu sesuai 37 bagian 10.15.

## Laporan & paket night audit (`docs/architecture/38-reports-night-audit-pack.md`)

Lima laporan baru di halaman Laporan dan paket night audit yang dikirim lewat e-mail. Layar aplikasi berbahasa Indonesia; e-mail dan PDF paket berbahasa Inggris (K4). Daily flash report mengikuti struktur DFR hotel (K7).

| File | Isi | PR laporan |
|---|---|---|
| `63-lap-daily-flash.dc.html` | Daily flash report: hari ini, MTD, MTD bulan lalu, MTD tahun lalu, anggaran MTD, selisih, YTD; statistik kamar per tipe, aktivitas, cover; pendapatan A kamar, B F&B per outlet dan banquet, C lain-lain (kelompok dari `statement_group` akun), GTR; pembayaran per kasir dan metode; control balance terhadap guest ledger; tanda tangan | 1 |
| `64-lap-pembatalan-no-show.dc.html` | Pembatalan & no-show per kejadian: nilai hilang, lead time, alasan, denda yang diposting (kosong bila belum), deposit, diaktifkan lagi; per alasan dan sumber | 1 |
| `65-lap-guest-ledger.dc.html` | Guest ledger per tanggal: saldo setiap folio, deposit ditahan, umur; dicocokkan dengan akun 1210 | 1 |
| `66-lap-ledger-deposit.dc.html` | Ledger deposit: awal, diterima, dipakai, dikembalikan, akhir; dicocokkan dengan akun 2310 dan guest ledger | 1 |
| `67-lap-void-koreksi.dc.html` | Void, koreksi, allowance, refund, diskon, komplimen, void POS: alasan, pembuat, penyetuju; porsi terhadap pendapatan | 1 |
| `68-lap-z-semua-laci.dc.html` | Laporan Z semua laci: setiap shift (X untuk yang masih terbuka), tunai di luar shift, back office; dicocokkan dengan laporan kasir | 1 |
| `69-na-selesai-paket.dc.html` | Night audit selesai: status 15 bagian paket, e-mail per penerima, peringatan pengiriman gagal | 2 (e-mail: 3) |
| `70-na-arsip.dc.html` | Arsip night audit: paket per hari (lengkap, sebagian, dibangun ulang, e-mail mati); riwayat pengiriman dengan SHA-256 dan versi renderer; kirim ulang ke penerima terdaftar | 2 (riwayat, kirim ulang: 3) |
| `71-na-penerima.dc.html` | Penerima paket: daftar alamat, bagian yang dipilih (label nama tamu / kas), aktif, hapus | 3 |
| `72-na-email-paket.dc.html` | E-mail paket (Inggris): angka utama di badan hanya untuk penerima SUMMARY/FLASH, lampiran PDF, tanpa tautan | 3 |
| `73-na-pdf-sampul.dc.html` | PDF paket: sampul dengan bagian yang dipilih penerima dan ringkasan satu baris | 2 |
| `74-na-pdf-daily-flash.dc.html` | PDF paket: halaman daily flash report (pendapatan, pembayaran, kontrol, tanda tangan) | 2 |

Contoh angka saling cocok antarlayar untuk tanggal bisnis 28 Okt 2026: pendapatan neto 139.480.000 (A 96.200.000, B 32.100.000, C 11.180.000), GTR 168.770.800, pembayaran 148.920.000 (sama di daily flash dan Z semua laci), control balance 0; guest ledger 74.655.000 + deposit ditahan 12.490.000 = akun 1210 87.145.000; ledger deposit 22.750.000 + 3.000.000 − 12.510.000 − 750.000 = 12.490.000; void dan koreksi neto 1.161.000 = 0,83% pendapatan. Angka anggaran: bulan Oktober kamar 2.650.000.000, F&B 950.000.000, lain 336.500.000, pro rata 28/31.

Cara membaca:
- Nilai warna, ukuran font, radius, jarak, dan tinggi tombol ada di atribut `style="…"`. Itu nilai yang dimaksud.
- `<x-dc>`, `<helmet>`, `<sc-for>`, `<sc-if>` adalah format editor desain: `sc-for` = `v-for`, `sc-if` = `v-if`, dan `{{x}}` diisi dari `renderVals()` di bagian bawah file (data contoh).
- Data (nama tamu, angka) hanya contoh. Di aplikasi ambil dari API.
- Teks berbahasa Indonesia; di aplikasi semua teks lewat i18n (en + id).
- Font final: Plus Jakarta Sans. Di mockup dimuat dari Google Fonts; di aplikasi pakai `@fontsource-variable/plus-jakarta-sans` (self-host).
