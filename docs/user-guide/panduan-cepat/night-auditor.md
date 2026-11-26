# Panduan cepat: night auditor dan manajer jaga

Ringkasan satu halaman untuk night auditor dan manajer jaga yang menutup hari bisnis. Setiap baris menunjuk ke halaman panduan yang lengkap.

## Awal shift

1. Masuk ke KamaraPMS dan periksa **Tanggal bisnis** di bilah atas. Di sebelahnya tampil keadaan audit malam, misalnya **Audit malam nanti**, **Audit malam · 2 tertunda**, atau **Audit malam siap**. → [Memahami tanggal bisnis](../00-memulai/memahami-tanggal-bisnis.md)
2. Pastikan dengan resepsionis tamu mana yang benar-benar tidak datang malam ini, dan tamu mana yang masih harus check-out. → [Menandai tamu tidak datang](../11-audit-malam/menandai-no-show.md)
3. Ingatkan kasir dan outlet: semua shift kasir ditutup, dan bill outlet dibayar atau dibebankan ke kamar. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)
4. Buka **Akhir hari → Audit malam**. Di samping judul tertulis **Siap dijalankan** atau **{n} langkah perlu ditangani**. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)

## Sebelum audit malam

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Langkah **Waktu** belum beres | Tunggu sampai jam yang tertulis. Jamnya diatur administrator di **Pengaturan → Properti** (**Jam paling awal audit malam**). | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Tamu yang ditunggu ternyata sudah tiba | Check-in di **Front Desk → Tamu hari ini → Kedatangan**. | [Check-in](../01-front-desk/check-in.md) |
| Tamu tidak datang | Di langkah 2 **Kedatangan yang belum selesai**, centang tamunya, isi **Alasan**, centang **Saya menyatakan tamu-tamu ini tidak datang**, klik **Tandai {n} sebagai no-show**. | [Menandai tamu tidak datang](../11-audit-malam/menandai-no-show.md) |
| Hotel mengenakan biaya no-show | **Front Desk → Reservasi** → buka reservasinya → **Posting biaya no-show** → isi **Jumlah** dan **Alasan** → **Posting biaya**. | [Membatalkan reservasi dan tamu tidak datang](../01-front-desk/membatalkan-reservasi-dan-no-show.md) |
| Tamu yang seharusnya pulang belum check-out | Di langkah 3, klik **Check-out** atau **Bayar & check-out**. Kalau tamu ternyata masih menginap, klik **Perpanjang**. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Shift kasir masih terbuka | Minta kasir itu menutup shift-nya di **Tagihan → Shift kasir**. | [Shift kasir](../03-kasir-dan-pembayaran/shift-kasir.md) |
| Bill outlet masih terbuka dan menahan hari | Minta staf POS membayar, membebankan ke kamar, atau void bill itu di **POS → Denah meja dan bill**. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Opname persediaan masih terbuka | Minta staf persediaan menyetujui atau membatalkan opname itu di **Persediaan → Opname**. | [Opname](../09-inventory/opname.md) |
| Penjualan POS atau banquet gagal dipotong dari stok | Minta staf persediaan mengklik **Coba lagi** atau **Batalkan** di **Persediaan → Harga pokok**. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Langkah 4 **Biaya kamar**: malam yang tidak bisa dibebankan, misalnya `MISSING_NIGHTLY_RATE` | Cari kodenya di daftar penghalang, lalu kerjakan yang tertulis, misalnya mengisi harga malam itu di grid tarif. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| "Akuntansi belum siap, sehingga hari tidak dapat ditutup beserta jurnalnya" | Minta akuntan memperbaikinya di **Akuntansi → Akun sistem** atau **Akuntansi → Bagan akun**. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Tamu yang berangkat sangat pagi minta tagihan malam ini | **Tagihan → Biaya kamar** → **Posting biaya kamar**. Audit malam tidak memposting malam itu lagi. | [Memposting biaya kamar](../03-kasir-dan-pembayaran/posting-biaya-kamar.md) |
| Ada kartu **Peringatan (tidak menghentikan audit malam)** | Peringatan tidak menahan. Catat, lalu tindak lanjuti besok. Semuanya juga masuk paket audit malam. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |

## Menjalankan audit malam

1. Setelah penghalang selesai, klik **Muat ulang** sampai tanda di samping judul menjadi **Siap dijalankan**. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)
2. Di langkah 6 **Jalankan**, centang **Tutup {tanggal} dan buka tanggal bisnis berikutnya**, lalu klik **Jalankan audit malam**. Jangan menutup halaman dan jangan mengklik lagi. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)
3. Kartu hijau **Hari bisnis {tanggal} ditutup** menunjukkan tanggal bisnis baru dan ringkasan hari itu. Kalau muncul pesan merah, tidak ada yang berubah: selesaikan yang disebut, lalu jalankan lagi. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)
4. Periksa baris **Paket audit malam**. Kalau bertanda **Sebagian**, buka **Akhir hari → Arsip audit malam** → **Buka** → **Bangun ulang bagian yang gagal**. E-mail yang gagal dikirim ulang dari kartu **Kirim ulang**. → [Paket audit malam](../11-audit-malam/paket-audit-malam.md)
5. Ada orang baru yang perlu menerima paket? **Akhir hari → Penerima paket** → **Tambah penerima**, isi **Alamat** dan **Bagian yang dikirim**, lalu **Simpan**, kalau peran Anda mencakupnya. → [Paket audit malam](../11-audit-malam/paket-audit-malam.md)

## Pagi berikutnya

1. Buka **Akhir hari → Laporan** → **Daily flash report** untuk hari yang baru ditutup. Kalau ada peringatan "Control balance berbeda: …", minta akuntan memeriksanya. → [Laporan harian](../12-laporan/laporan-harian.md)
2. Buka **Guest ledger** hari yang sama. Kartu pencocokannya harus **Cocok**. Kalau **Berbeda**, laporkan kepada akuntan. → [Laporan harian](../12-laporan/laporan-harian.md)
3. Periksa **Laporan Z semua laci** dan **Void, koreksi dan diskon**, kalau peran Anda mencakupnya. → [Laporan harian](../12-laporan/laporan-harian.md)

## Ingat

- Hari yang sudah ditutup tidak bisa dibuka kembali. Kesalahan yang ditemukan kemudian dikoreksi pada tanggal bisnis yang baru.
- Tanda no-show tidak bisa dibatalkan dan tidak menagih apa pun. Kalau tamunya ternyata datang, buat reservasi atau walk-in baru. → [Menandai tamu tidak datang](../11-audit-malam/menandai-no-show.md)
- Satu kali audit malam menutup satu hari. Kalau tertinggal dua hari (**Audit malam terlambat**), jalankan dua kali. → [Menjalankan audit malam](../11-audit-malam/menjalankan-audit-malam.md)
- Mengklik baris di daftar "Hari belum bisa ditutup karena:" tidak menggulir halaman. Gulir sendiri ke langkahnya. → [Menangani penghalang](../11-audit-malam/menangani-penghalang.md)
- Shift kasir lain tidak bisa ditutup dari layar, juga oleh supervisor. Minta kasir itu masuk dan menutupnya sendiri. Kalau tidak bisa, laporkan ke manajer atau administrator dengan nomor shift, nama kasir, dan lacinya. → [Menangani penghalang](../11-audit-malam/menangani-penghalang.md)
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
