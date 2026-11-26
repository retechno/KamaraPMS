# Alur harian hotel

Halaman ini menunjukkan satu hari kerja hotel di KamaraPMS dari pagi sampai malam: siapa mengerjakan apa, dan halaman panduan mana yang menjelaskannya. Hari di KamaraPMS mengikuti **tanggal bisnis**, bukan kalender. Satu hari bisnis baru selesai saat audit malam menutupnya, walaupun sudah lewat tengah malam. Lihat [Memahami tanggal bisnis](00-memulai/memahami-tanggal-bisnis.md).

Jam di tabel hanya contoh. Ikuti jadwal dan prosedur hotel Anda. Menu dan tombol yang Anda lihat mengikuti peran Anda; kalau ada yang tidak tampil, lihat [Peran dan hak akses](00-memulai/peran-dan-hak-akses.md).

Perjalanan seorang tamu, dari pesan sampai hari ditutup:

```
Reservasi (draf atau langsung dikonfirmasi)
   │  Konfirmasi, deposit
   ▼
Tetapkan kamar
   │
   ▼
Check-in  ◄── atau Walk-in untuk tamu tanpa reservasi
   │
   ▼
Menginap: biaya ke folio, pembayaran, pindah kamar, perpanjang
   │
   ▼
Check-out (semua folio bersaldo nol)
   │
   ▼
Audit malam: biaya kamar diposting, hari ditutup, tanggal bisnis maju
```

## Pagi

Pagi dimulai setelah audit malam semalam membuka tanggal bisnis yang baru. Pemeriksaan laporan hari yang baru ditutup ada di bagian [Setelah audit malam](#setelah-audit-malam).

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Awal shift | Semua staf | Masuk, lalu periksa **Tanggal bisnis** di bilah atas. Buka **Hari Ini** dan lihat kartu **Perlu perhatian**. | [Masuk dan mengenal layar](00-memulai/login-dan-navigasi.md), [Memahami tanggal bisnis](00-memulai/memahami-tanggal-bisnis.md) |
| Sebelum menerima uang tunai | Kasir, resepsionis, kasir outlet | Hitung modal awal. **Tagihan → Shift kasir**: isi **Laci** dan **Modal awal**, lalu **Buka shift**. Kasir outlet memakai kode laci outlet, misalnya `LACI1`. | [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md) |
| Awal shift housekeeping | Supervisor housekeeping | **Kamar → Status kamar**: pilih Hunian **Dipesan** dan Kebersihan **Kotor** untuk melihat kamar yang belum siap bagi tamu yang tiba hari ini. | [Melihat status kamar](02-housekeeping/status-kamar.md) |
| Setelah melihat status kamar | Supervisor housekeeping | **Kamar → Daftar pembersihan** → **Buat daftar hari ini**. Centang tugas, pilih petugas, lalu **Tugaskan**. | [Daftar pembersihan dan Tugas saya](02-housekeeping/daftar-pembersihan-dan-tugas-saya.md) |
| Tugas sudah dibagi | Petugas housekeeping | **Kamar → Tugas saya**: kerjakan dulu kartu **Tamu tiba hari ini**. Klik **Mulai bersihkan**, lalu **Selesai: siap diperiksa**. | [Daftar pembersihan dan Tugas saya](02-housekeeping/daftar-pembersihan-dan-tugas-saya.md) |
| Kedatangan hari ini belum punya kamar | Resepsionis | **Tamu hari ini → Kedatangan → Tetapkan otomatis**. | [Menetapkan kamar](01-front-desk/menetapkan-kamar.md) |
| Tamu pulang, biasanya sebelum jam check-out hotel | Resepsionis, kasir | **Tamu hari ini → Keberangkatan → Bayar & check-out**: **Bayar**, lalu **Check-out**. Kalau tagihan ditanggung perusahaan, pindahkan dulu lewat tab **Transfer** di folio. | [Check-out](01-front-desk/check-out.md), [Transfer ke piutang perusahaan](06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md) |
| Sebelum outlet buka | Staf gudang | **Persediaan → Ringkasan**: periksa **Barang di bawah nol**, **Barang di bawah minimum**, dan **Dokumen menunggu keputusan saya**. Di **Persediaan → Harga pokok**, baris **Gagal** diberi **Coba lagi** atau **Batalkan**. | [Laporan persediaan](09-inventory/laporan-persediaan.md), [Menyiapkan persediaan](09-inventory/menyiapkan-persediaan.md) |
| Dapur, bar, atau housekeeping butuh barang | Staf outlet atau departemen, lalu staf gudang | Peminta: **Persediaan → Permintaan & transfer** → **Permintaan baru**. Gudang: **Siap dikirim** → **Jawab dengan transfer** → **Kirim transfer**. | [Permintaan barang dan transfer](09-inventory/permintaan-barang-dan-transfer.md) |
| Outlet buka | Kasir dan pelayan outlet | **POS → Denah meja dan bill**. Kalau tertulis "Printer belum terhubung ke agen: …", minta administrator memeriksanya. | [Mengambil pesanan](04-pos/mengambil-pesanan.md) |
| Awal hari banquet | Staf banquet | Periksa **Lonceng banquet** dan **Banquet → Acara hari ini**. Hubungi pemesan acara bertanda **Tanggal opsi lewat**. | [Tentatif dan konfirmasi](05-banquet/tentatif-dan-konfirmasi.md), [Posting sesi dan penagihan](05-banquet/posting-sesi-dan-penagihan.md) |

## Siang

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Tamu memesan kamar | Resepsionis, staf reservasi | **Reservasi → Reservasi baru**: cari tanggal, **Pesan**, pilih pemesan, **Pesan dan konfirmasi**. Draf yang sudah pasti: buka reservasinya → **Konfirmasi**. | [Membuat reservasi](01-front-desk/membuat-reservasi.md), [Mengonfirmasi dan mengubah reservasi](01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md) |
| Tamu mengirim uang muka | Resepsionis, kasir | Buka reservasinya → kartu **Terima deposit**. | [Menerima deposit](01-front-desk/menerima-deposit.md) |
| Tamu memakai laundry, minibar, dan sejenisnya | Resepsionis, kasir | Folio → tab **Biaya**: pilih kode biaya, isi jumlahnya, **Posting biaya**. | [Memposting biaya ke folio](03-kasir-dan-pembayaran/posting-biaya-ke-folio.md) |
| Tamu membayar sebagian tagihan | Kasir | Folio → tab **Pembayaran**: isi **Jumlah**, **Metode**, dan **Referensi**, lalu **Posting pembayaran**. | [Menerima pembayaran](03-kasir-dan-pembayaran/menerima-pembayaran.md) |
| Biaya atau pembayaran salah | Supervisor front office, penyetuju | **Reverse** untuk biaya hari ini, **Penyesuaian** untuk biaya kemarin, **Void** atau **Refund** untuk pembayaran. Penyetuju mengetik email dan kata sandinya sendiri. | [Reverse dan penyesuaian](03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md), [Void dan refund](03-kasir-dan-pembayaran/void-dan-refund.md), [Meminta persetujuan](00-memulai/meminta-persetujuan.md) |
| Tamu minta pindah kamar atau tinggal lebih lama | Resepsionis | **Menginap → ⋮ → Pindah kamar** atau **Perpanjang**. | [Pindah kamar](01-front-desk/pindah-kamar.md), [Perpanjang atau persingkat](01-front-desk/perpanjang-atau-persingkat.md) |
| Ada kamar yang baru kotor setelah check-out | Supervisor housekeeping | Klik **Buat daftar hari ini** lagi: hanya kamar yang baru yang ditambahkan. | [Daftar pembersihan dan Tugas saya](02-housekeeping/daftar-pembersihan-dan-tugas-saya.md) |
| Kamar sudah **Bersih** | Supervisor housekeeping | Periksa kamarnya, lalu klik **Periksa** di **Kamar → Housekeeping**. Kamar menjadi **Sudah diperiksa**. | [Mengubah status kebersihan](02-housekeeping/mengubah-status-kebersihan.md) |
| Ada kerusakan atau barang tamu tertinggal | Petugas dan supervisor housekeeping | **Laporkan masalah** untuk kerusakan. Kamar yang tidak boleh dijual: **Kamar → Blokir kamar**. Barang temuan: **Kamar → Barang tertinggal → Catat barang**. | [Laporan perawatan](02-housekeeping/laporan-perawatan.md), [Blokir kamar (OOO/OOS)](02-housekeeping/blokir-kamar-ooo-oos.md), [Barang tertinggal](02-housekeeping/barang-tertinggal.md) |
| Tamu ingin restoran ditagihkan ke kamar | Resepsionis | Folio → kartu **Beban dari outlet: Terkunci** → isi batas, masa berlaku, dan outletnya → **Buka untuk outlet**. | [Membuka folio untuk outlet](03-kasir-dan-pembayaran/membuka-folio-untuk-outlet.md) |
| Jam makan di outlet | Pelayan, kasir outlet, dapur | **Bill baru** → **Kirim ke dapur dan bar**. Dapur: **POS → Layar dapur**. Kasir: **Bayar**, atau **Bebankan ke kamar** untuk tamu hotel. | [Mengambil pesanan](04-pos/mengambil-pesanan.md), [Layar dapur](04-pos/layar-dapur.md), [Membayar bill](04-pos/membayar-bill.md), [Bebankan ke kamar](04-pos/bebankan-ke-kamar.md) |
| Calon pemesan acara bertanya atau setuju | Staf banquet | **Pemesanan baru**, **Tahan sebagai tentatif**, lalu **Konfirmasi**. Uang muka: **Terima DP**. Detail untuk departemen: tab **BEO**. | [Membuat acara dan sesi](05-banquet/membuat-acara-dan-sesi.md), [Tentatif dan konfirmasi](05-banquet/tentatif-dan-konfirmasi.md), [Item, paket, dan deposit](05-banquet/item-paket-dan-deposit.md), [BEO dan dokumen](05-banquet/beo-dan-dokumen.md) |
| Sesi acara berlangsung dan jumlah tamu hadir diketahui | Staf banquet | Tab **Folio** → **Posting sesi**. Setelah semua sesi diposting: **Selesaikan acara**. | [Posting sesi dan penagihan](05-banquet/posting-sesi-dan-penagihan.md) |
| Barang pesanan datang | Staf receiving atau gudang | **Pembelian**, tab **Pesanan** → **Terima barang** → **Posting penerimaan**. Barang rusak: **Retur ke pemasok**. | [Penerimaan, tagihan, dan retur](09-inventory/penerimaan-tagihan-dan-retur.md) |
| Stok menipis atau barang perlu dibeli | Staf gudang, staf departemen | **Buat PR** dari laporan **Barang di bawah minimum**, atau **Persediaan → Pembelian** → **Permintaan baru** → **Ajukan untuk persetujuan**. | [Permintaan dan pesanan pembelian](09-inventory/permintaan-dan-pesanan-pembelian.md) |
| Permintaan menunggu keputusan | Kepala departemen, cost controller, manajer | Klik lonceng atau **Persediaan → Perlu saya setujui** → **Setujui** atau **Kembalikan**. | [Rantai persetujuan](09-inventory/rantai-persetujuan.md) |
| Barang dipakai, dibuang, atau dipakai makan staf | Staf gudang, chef | **Persediaan → Pemakaian & waste**: **Pemakaian baru**, **Catat waste**, atau **Makan karyawan**. Waste dicatat pada hari yang sama. | [Pemakaian dan waste](09-inventory/pemakaian-dan-waste.md) |
| Faktur pemasok tiba, atau hotel membayar pemasok | Akuntan | **Utang usaha → Tagihan pemasok** → **Catat tagihan**. Pembayaran: **Utang usaha → Pembayaran pemasok** → **Bayar pemasok**. | [Utang usaha](08-keuangan-back-office/utang-usaha.md) |
| Tamu perusahaan sudah check-out, atau perusahaan membayar | Akuntan (piutang) | **Tagihan → Piutang perusahaan** → kode perusahaan → kartu **Invoice baru** → **Buat invoice ({n} dipilih, {total})** → **Cetak**. Pembayaran dicatat dengan **Bayar** pada invoicenya. | [Invoice dan penerimaan](06-corporate-group-city-ledger/invoice-dan-penerimaan.md) |

## Sore

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Sebelum check-in ramai | Resepsionis, supervisor housekeeping | **Kamar → Status kamar**, saring menurut Kebersihan. Klik **Muat ulang** untuk melihat perubahan terbaru. | [Melihat status kamar](02-housekeeping/status-kamar.md) |
| Tamu dengan reservasi tiba | Resepsionis | **Tamu hari ini → Kedatangan → Check-in**: Tamu, Kamar, Deposit, Selesai. **Cetak kartu registrasi** untuk ditandatangani tamu. | [Check-in](01-front-desk/check-in.md) |
| Tamu harus masuk ke kamar yang belum bersih | Supervisor front office | Di langkah **Kamar**, centang **Tetap check-in** dan isi alasannya, kalau peran Anda mencakupnya. | [Check-in](01-front-desk/check-in.md) |
| Tamu datang tanpa reservasi | Resepsionis | **Walk-in**, lalu lengkapi identitas lewat **Ubah tamu**. | [Walk-in](01-front-desk/walk-in.md) |
| Rombongan tiba | Supervisor front office | **Check-in grup** dari halaman grupnya. Satu tagihan untuk rombongan: **Buka master folio**. | [Grup dan master folio](06-corporate-group-city-ledger/grup-dan-master-folio.md) |
| Check-in ternyata keliru, masih hari yang sama | Supervisor front office | **Batalkan check-in**, isi **Alasan**. Hanya bisa sebelum ada biaya dan sebelum pindah kamar. | [Membatalkan check-in](01-front-desk/membatalkan-check-in.md) |
| Opname yang dijadwalkan hari ini | Staf gudang, penyetuju | **Persediaan → Opname** → **Mulai opname** → hitung → **Kirim untuk disetujui** → **Setujui opname**, semuanya pada hari yang sama, sebelum audit malam. | [Opname](09-inventory/opname.md) |
| Akhir shift housekeeping | Petugas dan supervisor housekeeping | Petugas memeriksa tab **Selesai** di **Tugas saya**. Supervisor memeriksa tugas **Belum** dan **Dilewati**, kamar **Bersih** untuk kedatangan, dan **Blokir aktif dalam tampilan**. | [Daftar pembersihan dan Tugas saya](02-housekeeping/daftar-pembersihan-dan-tugas-saya.md), [Blokir kamar (OOO/OOS)](02-housekeeping/blokir-kamar-ooo-oos.md) |
| Akhir hari banquet | Staf banquet | **Banquet → Sesi belum diposting**: baca sebabnya, misalnya **Tanpa pax**. Lihat jadwal besok di **Banquet → Laporan banquet → Jadwal harian**. | [Posting sesi dan penagihan](05-banquet/posting-sesi-dan-penagihan.md), [BEO dan dokumen](05-banquet/beo-dan-dokumen.md) |
| Akhir shift kasir | Kasir, resepsionis | Hitung laci. **Tutup shift…** → isi **Kas yang dihitung**. Selisih di atas batas disetujui atasan di **Setujui selisih**. Klik **Cetak laporan Z**. | [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md) |

## Malam

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Outlet tutup | Kasir outlet | **POS → Laporan outlet**: **Bill terbuka dengan item terjual** harus nol. Cocokkan **Pembayaran per metode**, lalu tutup shift dan **Cetak laporan Z**. | [Laporan outlet](04-pos/laporan-outlet.md), [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md) |
| Akhir shift front office | Supervisor front office | Periksa daftar **Shift**: shift yang masih terbuka bisa menahan audit malam. Pastikan tab **Keberangkatan** kosong, kecuali tamu yang memang terlambat. | [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md), [Check-out](01-front-desk/check-out.md) |
| Awal shift night auditor | Night auditor, manajer jaga | Periksa keadaan di samping **Tanggal bisnis**, misalnya **Audit malam nanti**. Pastikan dengan resepsionis siapa yang tidak datang. Ingatkan kasir dan outlet menutup shift dan bill. | [Panduan cepat night auditor](panduan-cepat/night-auditor.md) |
| Sebelum audit malam | Night auditor | Buka **Akhir hari → Audit malam**. Di samping judul tertulis **Siap dijalankan** atau **{n} langkah perlu ditangani**. | [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md) |
| Tamu yang ditunggu tidak datang | Night auditor | Langkah 2 **Kedatangan yang belum selesai**: centang tamunya, isi **Alasan**, centang **Saya menyatakan tamu-tamu ini tidak datang**, **Tandai {n} sebagai no-show**. Biaya no-show: **Posting biaya no-show** di reservasinya. | [Menandai tamu tidak datang](11-audit-malam/menandai-no-show.md), [Membatalkan reservasi dan tamu tidak datang](01-front-desk/membatalkan-reservasi-dan-no-show.md) |
| Tamu yang seharusnya pulang belum check-out | Night auditor | Langkah 3: **Check-out** atau **Bayar & check-out**. Kalau tamu masih menginap: **Perpanjang**. | [Menangani penghalang](11-audit-malam/menangani-penghalang.md) |
| Ada penghalang lain | Night auditor, dibantu kasir, staf POS, staf persediaan, atau akuntan | Shift kasir terbuka, bill outlet terbuka, opname terbuka, penjualan yang gagal dipotong dari stok, atau akun yang belum siap: minta pemiliknya membereskan, lalu **Muat ulang**. | [Menangani penghalang](11-audit-malam/menangani-penghalang.md) |
| Mulai **Jam paling awal audit malam**, semua penghalang selesai | Night auditor | Langkah 6 **Jalankan**: centang **Tutup {tanggal} dan buka tanggal bisnis berikutnya**, klik **Jalankan audit malam**. Jangan menutup halaman dan jangan mengklik lagi. | [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md) |

## Setelah audit malam

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Kartu hijau **Hari bisnis {tanggal} ditutup** | Night auditor | Periksa ringkasan hari itu dan baris **Paket audit malam**. Kalau bertanda **Sebagian**: **Akhir hari → Arsip audit malam** → **Buka** → **Bangun ulang bagian yang gagal**. | [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md), [Paket audit malam](11-audit-malam/paket-audit-malam.md) |
| Saat hari ditutup | Otomatis oleh KamaraPMS | Biaya kamar diposting, sesi banquet yang jatuh tempo diposting, persediaan ditutup (termasuk amenities otomatis), jurnal hari itu dibuat, dan kamar terisi menjadi **Kotor** kalau pengaturannya menyala. Tanggal bisnis maju satu hari. | [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md), [Amenities otomatis](09-inventory/amenities-otomatis.md) |
| Tidak lama setelah audit malam | Penerima paket | Kalau e-mail aktif, paket audit malam tiba lewat e-mail. Paket juga bisa dibuka di **Akhir hari → Arsip audit malam**. | [Paket audit malam](11-audit-malam/paket-audit-malam.md) |
| Pagi berikutnya | Night auditor, manajer, supervisor | **Akhir hari → Laporan** → **Daily flash report**. Kalau ada "Control balance berbeda: …", minta akuntan memeriksanya. Lalu **Guest ledger** (harus **Cocok**), **Laporan Z semua laci**, dan **Void, koreksi dan diskon**. | [Laporan harian](12-laporan/laporan-harian.md) |
| Pagi berikutnya | Manajer | **Akhir hari → Kinerja**: okupansi, ADR, RevPAR, pendapatan kamar, dan kartu **14 malam ke depan**. | [Kinerja dan dasbor](12-laporan/kinerja-dan-dasbor.md) |
| Pagi berikutnya | Akuntan | **Akuntansi → Akun kontrol** → **Hari tutup terakhir** → **Periksa**. Hari tertutup tanpa jurnal: **Jurnalkan hari yang hilang**, kalau peran Anda mencakupnya. | [Laporan keuangan](07-accounting/laporan-keuangan.md), [Jurnal manual dan pembalikan](07-accounting/jurnal-manual-dan-pembalikan.md) |
| Ada yang dipertanyakan | Manajer | **Pengaturan → Jejak audit** → isi **Nomor dokumen** → **Cari** → **Detail**. | [Jejak audit](12-laporan/jejak-audit.md) |

Hari yang sudah ditutup tidak bisa dibuka kembali. Kesalahan yang ditemukan sesudahnya dikoreksi pada tanggal bisnis yang baru, lalu hari berikutnya dimulai lagi dari [Pagi](#pagi).

## Mingguan dan bulanan

| Waktu/kejadian | Siapa | Yang dilakukan | Halaman |
|---|---|---|---|
| Rekening koran bank tersedia, misalnya mingguan atau bulanan | Akuntan | **Bank → Mutasi bank** → **Impor rekening koran** → **Rekonsiliasi** → **Cocokkan otomatis**. Pencairan kartu atau QRIS: **Selesaikan pembayaran kartu atau dompet digital…**. | [Rekonsiliasi bank](08-keuangan-back-office/rekonsiliasi-bank.md), [Settlement kartu](08-keuangan-back-office/settlement-kartu.md) |
| Berkala, misalnya mingguan | Akuntan (piutang) | **Tagihan → Invoice jatuh tempo** → **Kirim surat peringatan…** → **Catat dan cetak**. | [Surat peringatan](06-corporate-group-city-ledger/surat-peringatan.md) |
| Rapat okupansi | Manajer | **Akhir hari → Laporan** → **Okupansi dan statistik** → **Jalankan** → **Unduh CSV**. | [Laporan front office](12-laporan/laporan-front-office.md) |
| Setiap minggu untuk bar, akhir bulan untuk semua gudang | Staf gudang, penyetuju | Opname gudang, lalu periksa laporan **Selisih opname**. | [Opname](09-inventory/opname.md), [Laporan persediaan](09-inventory/laporan-persediaan.md) |
| Biaya makanan dan minuman | Manajer, cost controller | **Persediaan → Ringkasan** → **Biaya harian** atau **Biaya F&B per outlet**. | [Laporan persediaan](09-inventory/laporan-persediaan.md) |
| Setelah bulan pajak berakhir | Staf pajak, akuntan | **Pajak → SPT Masa PPN** → pilih **Pajak** → klik bulannya → **PDF** → **Laporkan SPT**. Setelah disetor: **Catat pembayaran**. | [Pajak dan faktur](08-keuangan-back-office/pajak-dan-faktur.md) |
| Setelah bulan berakhir | Akuntan | **Aset tetap → Penyusutan** → klik bulannya → **Jalankan penyusutan {bulan}**. | [Penyusutan bulanan](10-aset-tetap/penyusutan-bulanan.md) |
| Laporan bulanan | Akuntan, manajer | **Neraca saldo**, **Laporan laba rugi**, **Neraca**, dan **Laporan arus kas**; **Laporan departemen**; **Anggaran → Anggaran terhadap realisasi** → **Tampilkan**. Periksa juga **Utang usaha → Umur utang usaha**. | [Laporan keuangan](07-accounting/laporan-keuangan.md), [Departemen](07-accounting/departemen.md), [Anggaran](08-keuangan-back-office/anggaran.md), [Utang usaha](08-keuangan-back-office/utang-usaha.md) |
| Akhir bulan | Kepala akuntansi | Pastikan **Hari yang dijurnalkan** lengkap dan **Akun kontrol** tidak menunjukkan **Selisih**, lalu **Akuntansi → Periode akuntansi** → **Tutup** → **Konfirmasi**. | [Periode dan tahun buku](07-accounting/periode-dan-tahun-buku.md) |
| Berkala, misalnya setiap triwulan | Pengelola aset | **Aset tetap → Opname aset** → pilih **Lokasi** → **Mulai opname**. | [Opname aset dan label QR](10-aset-tetap/opname-aset-dan-label-qr.md) |
| Sebelum tahun buku dimulai | Akuntan, manajer | Susun anggaran tahun baru, lalu **Aktifkan** dengan persetujuan di jendela **Setujui anggaran**. | [Anggaran](08-keuangan-back-office/anggaran.md) |
| Akhir tahun, setelah semua bulan ditutup | Kepala akuntansi | **Akuntansi → Tahun buku** → **Tutup tahun** → **Konfirmasi**. | [Periode dan tahun buku](07-accounting/periode-dan-tahun-buku.md) |

---

Ada langkah yang tidak berjalan seperti di sini? Tanyakan ke supervisor Anda, lalu ke administrator hotel. Administrator hotel meneruskannya ke tim dukungan KamaraPMS kalau perlu. Lihat [Bantuan dan kontak](README.md#bantuan-dan-kontak).
