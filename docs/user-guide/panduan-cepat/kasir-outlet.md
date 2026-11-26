# Panduan cepat: kasir dan pelayan outlet

Ringkasan satu halaman untuk kasir dan pelayan outlet (restoran, bar, room service). Setiap baris menunjuk ke halaman panduan yang lengkap.

## Awal shift

1. Masuk ke KamaraPMS dan periksa **Tanggal bisnis** di bilah atas. Penjualan dicatat pada tanggal ini. → [Memahami tanggal bisnis](../00-memulai/memahami-tanggal-bisnis.md)
2. Kalau Anda menerima uang tunai, buka shift di **Tagihan → Shift kasir** dengan kode laci yang sama dengan laci outlet, misalnya `LACI1`. → [Membayar bill](../04-pos/membayar-bill.md)
3. Buka **POS → Denah meja dan bill**. Kalau hotel punya beberapa outlet, pilih outletnya di kanan atas. → [Mengambil pesanan](../04-pos/mengambil-pesanan.md)
4. Kalau di atas denah tertulis "Printer belum terhubung ke agen: …", tiket atau struk belum bisa tercetak. Minta administrator memeriksanya. → [Mengambil pesanan](../04-pos/mengambil-pesanan.md)

## Sepanjang hari

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Tamu duduk di meja | Klik mejanya, isi **Jumlah tamu**, klik **Bill baru**. | [Mengambil pesanan](../04-pos/mengambil-pesanan.md) |
| Pesanan bawa pulang atau room service | **Bawa pulang / bill tanpa meja** → pilih **Jenis pesanan** → **Bill baru**. | [Mengambil pesanan](../04-pos/mengambil-pesanan.md) |
| Tamu memesan menu dengan pilihan, misalnya tingkat pedas | Klik produknya, pilih variannya, isi **Catatan untuk dapur**, klik **Tambahkan ke bill**. | [Mengambil pesanan](../04-pos/mengambil-pesanan.md) |
| Pesanan siap dikirim | Klik **Kirim ke dapur dan bar**. | [Mengambil pesanan](../04-pos/mengambil-pesanan.md) |
| Pelayan mencatat pesanan dari ponsel | **POS → Pelayan**: pilih meja, **Buka bill**, **Tambah**, lalu **Kirim ke dapur dan bar**. Pelayan tidak menerima pembayaran. | [Pelayan](../04-pos/pelayan.md) |
| Ingin tahu apakah pesanan sudah siap | Lihat status item di bill: **Terkirim**, **Dimasak**, **Siap**, atau **Diantar**. | [Layar dapur](../04-pos/layar-dapur.md) |
| Anda bertugas di dapur atau bar | **POS → Layar dapur**: **Mulai**, lalu **Siap**, lalu **Sudah diambil**. | [Layar dapur](../04-pos/layar-dapur.md) |
| Tamu meminta bill | Klik **Minta bill**. Pra-tagihan tercetak kalau outlet punya printer struk. | [Mengambil pesanan](../04-pos/mengambil-pesanan.md) |
| Tamu membayar | **Bayar** → atur metodenya, klik **Pas** → **Bayar dan tutup**. | [Membayar bill](../04-pos/membayar-bill.md) |
| Tamu membayar sendiri-sendiri per item | Klik **Pisah**, centang item yang pindah, klik **Pisah**. Setiap bill dibayar sendiri. | [Membayar bill](../04-pos/membayar-bill.md) |
| Tamu hotel ingin tagihannya masuk ke kamar | Periksa isi bill dulu. **Bayar** → hapus baris yang ada dengan **×** → **+ Bebankan ke kamar** → **Cari** → pilih tamu → **Pas** → **Bayar dan tutup**. Minta tamu menandatangani struk. | [Bebankan ke kamar](../04-pos/bebankan-ke-kamar.md) |
| Beban ke kamar ditolak karena folio terkunci | Minta resepsionis membuka folio tamu untuk outlet, atau tamu membayar di tempat. | [Bebankan ke kamar](../04-pos/bebankan-ke-kamar.md) |
| Sisa batas kamar tidak cukup | Klik **Bebankan sebesar yang bisa diterima kamar, sisanya dengan cara lain**. Sisanya dibayar tunai atau kartu. | [Bebankan ke kamar](../04-pos/bebankan-ke-kamar.md) |
| Item yang sudah dikirim dibatalkan | Klik **Void**, isi **Alasan**, **Lanjut ke persetujuan**. Item yang belum dikirim cukup dihapus dengan **Hapus**. | [Void, diskon, dan komplimen](../04-pos/void-diskon-komplimen.md) |
| Tamu mendapat potongan harga | Klik **Diskon**, isi jendela, klik **Beri diskon**. Diskon di atas batas outlet butuh persetujuan. | [Void, diskon, dan komplimen](../04-pos/void-diskon-komplimen.md) |
| Bill digratiskan, misalnya jamuan manajer | Klik **Komplimen**, isi **Tamu atau manajer** dan **Alasan**, **Lanjut ke persetujuan**. | [Void, diskon, dan komplimen](../04-pos/void-diskon-komplimen.md) |
| Bill yang sudah lunas harus dikoreksi | Klik **Buka kembali**, isi **Alasan**, minta persetujuan. Koreksi isinya, lalu bayar lagi. | [Void, diskon, dan komplimen](../04-pos/void-diskon-komplimen.md) |
| Bill terlanjur dibebankan ke kamar yang salah | Klik **Buka kembali** dan minta persetujuan, lalu klik **Kembalikan beban kamar** di baris "Dibebankan ke kamar", isi **Alasan**, dan minta persetujuan. Bayar bill dengan cara lain. | [Mengembalikan beban kamar](../04-pos/mengembalikan-beban-kamar.md) |
| Tamu meminta salinan struk | Klik **Cetak ulang struk** dan isi alasannya. | [Membayar bill](../04-pos/membayar-bill.md) |

## Akhir shift

1. Buka **POS → Laporan outlet**. **Bill terbuka dengan item terjual** harus nol: selesaikan bill-bill itu sebelum audit malam. → [Laporan outlet](../04-pos/laporan-outlet.md)
2. Cocokkan **Pembayaran per metode** dengan uang dan bukti kartu Anda. → [Laporan outlet](../04-pos/laporan-outlet.md)
3. Kalau Anda memegang laci, klik **Tutup shift…**, isi **Kas yang dihitung**, lalu **Cetak laporan Z**. → [Shift kasir](../03-kasir-dan-pembayaran/shift-kasir.md)
4. Klik inisial nama Anda → **Keluar**. → [Masuk dan mengenal layar](../00-memulai/login-dan-navigasi.md)

## Ingat

- Periksa bill dengan teliti sebelum memilih **Bebankan ke kamar**. Beban yang terlanjur salah ditarik dari bill-nya (**Buka kembali**, lalu **Kembalikan beban kamar**) selama tamu belum check-out. → [Mengembalikan beban kamar](../04-pos/mengembalikan-beban-kamar.md)
- Item yang sudah dikirim tidak bisa diubah atau dihapus. Pembatalannya lewat **Void** dengan persetujuan.
- Uang tunai hanya bisa diterima kalau shift Anda terbuka di laci outlet ini. Shift di laci lain tidak dihitung.
- Item bertulisan **DIBATALKAN** di layar dapur jangan dimasak.
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
