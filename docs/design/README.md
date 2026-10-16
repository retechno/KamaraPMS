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
| `31-inv-daily-cost.dc.html` | Daily cost F&B: per hari dan MTD, food dan beverage, grafik cost % dengan garis target | 5 |
| `32-inv-amenities-paket.dc.html` | Pengaturan amenities otomatis: paket bawaan per kamar / per tamu, tambahan per tipe kamar | 3 |
| `33-inv-amenities-malam.dc.html` | Dokumen amenities otomatis satu malam (dibuat tutup hari): kamar terisi, tamu, biaya, stok kurang jadi minus | 3 |
| `34-inv-alur-persetujuan.dc.html` | Alur persetujuan: PR 3 tingkat (kepala dept, cost control, akhir di atas batas), SR 2 tingkat | 2 |

Contoh angka inventory saling cocok antarlayar: sirup di Bar 28,2 botol / 1.393.080 (rata-rata 49.400); F&B September food 31,1% aktual vs 28,3% teori, beverage 25,0% vs 22,0%; amenities 3 Sep 78 kamar, 142 tamu, 1.895.640.

Cara membaca:
- Nilai warna, ukuran font, radius, jarak, dan tinggi tombol ada di atribut `style="…"`. Itu nilai yang dimaksud.
- `<x-dc>`, `<helmet>`, `<sc-for>`, `<sc-if>` adalah format editor desain: `sc-for` = `v-for`, `sc-if` = `v-if`, dan `{{x}}` diisi dari `renderVals()` di bagian bawah file (data contoh).
- Data (nama tamu, angka) hanya contoh. Di aplikasi ambil dari API.
- Teks berbahasa Indonesia; di aplikasi semua teks lewat i18n (en + id).
- Font final: Plus Jakarta Sans. Di mockup dimuat dari Google Fonts; di aplikasi pakai `@fontsource-variable/plus-jakarta-sans` (self-host).
