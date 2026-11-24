# Panduan cepat: administrator hotel

Ringkasan satu halaman untuk administrator hotel (pengguna dengan centang **Administrator tenant**). Setiap baris menunjuk ke halaman panduan yang lengkap.

## Sekali saat mulai

1. Buka **Pengaturan → Properti** → **Properti baru**, isi kartunya, lalu klik **Buat properti**. **Kode**, **Tanggal bisnis pembukaan**, dan **Bahasa nama bawaan** hanya bisa diisi sekali. → [Properti](../13-pengaturan/properti.md)
2. Buat tipe kamar di **Pengaturan → Tipe kamar** dan nomor kamar di **Pengaturan → Kamar**. → [Tipe kamar dan kamar](../13-pengaturan/tipe-kamar-dan-kamar.md)
3. Buat pajak dan biaya servis di **Pengaturan → Pajak & biaya layanan**, lalu pasang di **Pengaturan → Kode biaya**: **Ubah** pada **ROOM** → **Aturan** → **Simpan aturan**. Ulangi untuk kode lain yang dikenai pajak, kecuali **ROOM_EXEMPT**. → [Setup awal hotel](../00-memulai/setup-awal-hotel.md)
4. Buat paket tarif di **Pengaturan → Paket tarif**, isi harganya di **Pengaturan → Grid tarif**, dan tandai satu paket **Berbayar** sebagai **Plan acuan**. → [Paket tarif dan grid tarif](../13-pengaturan/paket-tarif-dan-grid-tarif.md)
5. Buat peran di **Pengaturan → Peran** → **Peran baru**. KamaraPMS tidak menyediakan peran siap pakai. → [Peran](../13-pengaturan/peran.md)
6. Buat pengguna di **Pengaturan → Pengguna** → **Pengguna baru**, pilih properti dan perannya di **Akses properti**, lalu **Buat pengguna**. → [Pengguna](../13-pengaturan/pengguna.md)
7. Atur **Pengaturan → Aturan persetujuan**, lalu **Simpan**. Pastikan setiap jenis koreksi punya minimal dua penyetuju selain administrator. → [Aturan persetujuan](../13-pengaturan/aturan-persetujuan.md)
8. Buat satu reservasi percobaan. **Perkiraan (termasuk service dan pajak)** harus lebih besar dari harga kamar saja. → [Setup awal hotel](../00-memulai/setup-awal-hotel.md)

## Sepanjang waktu

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Ada staf baru | **Pengguna baru** → isi **Email**, **Nama lengkap**, dan **Kata sandi awal** (minimal 12 karakter) → **Tambah properti** → **Buat pengguna**. Sampaikan kata sandinya secara pribadi. | [Pengguna](../13-pengaturan/pengguna.md) |
| Staf pindah tugas atau mulai bekerja di hotel lain | Klik nama pengguna → ganti perannya atau **Tambah properti** → **Simpan perubahan**. | [Pengguna](../13-pengaturan/pengguna.md) |
| Staf lupa kata sandi | Di halaman pengguna, kartu **Atur kata sandi baru** → **Atur kata sandi**. | [Pengguna](../13-pengaturan/pengguna.md) |
| Staf berhenti | Hapus centang **Aktif (menonaktifkan langsung mengeluarkan pengguna)** → **Simpan perubahan**. | [Pengguna](../13-pengaturan/pengguna.md) |
| Tugas sebuah jabatan berubah | **Pengaturan → Peran** → klik nama peran → centang atau hapus izin → **Simpan perubahan**. | [Peran](../13-pengaturan/peran.md) |
| Harga musim ramai atau akhir pekan | **Pengaturan → Grid tarif** → pilih **Paket tarif** → kartu **Atur tarif** → **Terapkan**. | [Paket tarif dan grid tarif](../13-pengaturan/paket-tarif-dan-grid-tarif.md) |
| Minimal menginap atau tutup jual | **Pengaturan → Pembatasan penjualan** → kartu **Atur pembatasan** → **Terapkan**. | [Pembatasan, yield, dan kuota](../13-pengaturan/pembatasan-yield-dan-kuota.md) |
| Harga naik atau turun otomatis menurut keterisian | **Pengaturan → Aturan yield** → **Aturan baru** → **Simpan**, lalu periksa dengan **Cek harga**. | [Pembatasan, yield, dan kuota](../13-pengaturan/pembatasan-yield-dan-kuota.md) |
| Tarif pajak daerah atau service charge berubah | **Pengaturan → Pajak & biaya layanan** → **Ubah** → **Tarif (%)** → **Simpan**. Berlaku untuk posting berikutnya. | [Pajak dan kode biaya](../13-pengaturan/pajak-dan-kode-biaya.md) |
| Hotel menjual layanan baru, misalnya spa | **Pengaturan → Kode biaya** → **Kode biaya baru** → **Simpan**, lalu **Ubah** untuk memasang pajaknya. | [Pajak dan kode biaya](../13-pengaturan/pajak-dan-kode-biaya.md) |
| Kamar ditambah atau berhenti dijual lama | **Pengaturan → Kamar** → **Kamar baru**, atau **Ubah** lalu hapus centang **Aktif (beroperasi)**. | [Tipe kamar dan kamar](../13-pengaturan/tipe-kamar-dan-kamar.md) |
| Jam audit malam perlu diubah | **Pengaturan → Properti** → klik kode properti → **Jam paling awal audit malam** → **Simpan perubahan**. | [Properti](../13-pengaturan/properti.md) |
| Audit malam tertahan karena kode biaya kamar (`INVALID_CHARGE_CODE` atau `ROOM_CHARGE_CODE_UNMAPPED`) | Aktifkan kode biaya kamar, atau isi akunnya, di **Pengaturan → Kode biaya**. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |
| Banner langganan muncul, atau "Batas paket tercapai: …" | **Pengaturan → Langganan**: baca kartu **Paket {nama}**, **Pemakaian**, dan **Invoice**. Perpanjangan dan paket baru diurus tim KamaraPMS. | [Langganan dan sistem](../13-pengaturan/langganan-dan-sistem.md) |
| Layar lambat atau server tidak menjawab | **Pengaturan → Sistem** → **Muat ulang**. Kalau **Tidak tersedia**, foto kalimat merahnya dan kirimkan ke tim KamaraPMS. | [Langganan dan sistem](../13-pengaturan/langganan-dan-sistem.md) |
| Perlu tahu siapa mengubah pengguna atau peran | **Pengaturan → Jejak audit** → **Lingkup** **Tingkat perusahaan** → **Cari**. | [Jejak audit](../12-laporan/jejak-audit.md) |

## Ingat

- Pengguna, peran, properti, pajak, dan kode biaya tidak bisa dihapus: nonaktifkan saja. **Mata uang** properti terkunci setelah catatan keuangan pertama. → [Properti](../13-pengaturan/properti.md)
- Setelah masa tenggang habis, langganan menjadi **Hanya baca**: check-in, pembayaran, dan audit malam pun ditolak. Perpanjang sebelum **Masa tenggang sampai**. → [Langganan dan sistem](../13-pengaturan/langganan-dan-sistem.md)
- Kolom **Akses** di daftar **Pengguna** menulis "Tanpa akses properti" untuk semua yang bukan administrator. Klik nama penggunanya untuk melihat properti dan perannya yang benar. → [Pengguna](../13-pengaturan/pengguna.md)
- Baris akses yang properti atau perannya belum dipilih dibuang saat menyimpan, tanpa peringatan. Setelah menyimpan, buka lagi pengguna itu dan periksa aksesnya. → [Pengguna](../13-pengaturan/pengguna.md)
- Kode pembulatan POS (`POS_ROUNDING`) tidak bisa diubah: **Jenis** tampil sebagai teks mentah dan setiap simpan gagal. Biarkan kode itu apa adanya; kalau akunnya perlu diubah, laporkan ke tim KamaraPMS. → [Pajak dan kode biaya](../13-pengaturan/pajak-dan-kode-biaya.md)
- **Simpan rantai** di **Persediaan → Rantai persetujuan** hanya diterima dari peran yang juga boleh **Mengubah pengaturan properti**. Kalau manajer persediaan ditolak, Anda yang menyimpan rantainya. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)
- Penyetuju permintaan pembelian juga butuh izin **Melihat barang, gudang, stok, kartu stok, dan saldo awal**. Tanpa izin itu, permintaan tertahan di tingkatnya. Tambahkan izinnya ke peran penyetuju. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)
- Administrator boleh menyetujui koreksinya sendiri walaupun aturan meminta orang lain, dan hal itu ditandai di jejak audit. → [Aturan persetujuan](../13-pengaturan/aturan-persetujuan.md)
