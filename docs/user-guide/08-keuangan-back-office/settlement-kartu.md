# Settlement kartu, e-wallet, dan QRIS

**Siapa yang melakukan:** staf akuntansi yang perannya boleh merekonsiliasi bank. Aturan fee diatur oleh peran yang boleh mengelola bank. Membatalkan settlement butuh persetujuan orang yang boleh menyetujui koreksi.
**Kapan dilakukan:** saat bank menerima pencairan dari acquirer (penyedia mesin EDC atau QRIS) untuk pembayaran tamu.
**Sebelum mulai:**
- Rekening koran yang memuat pencairan itu sudah diimpor. Lihat [Rekonsiliasi bank](rekonsiliasi-bank.md).
- Siapkan laporan settlement dari acquirer: tanggal transaksi, jumlah kotor, dan potongan komisi (MDR) beserta PPN-nya.

Pembayaran kartu, e-wallet, dan QRIS dari tamu ditampung di akun penampung sampai acquirer mencairkannya ke bank, dikurangi komisi.

## Langkah-langkah

### Melihat yang belum dibayar acquirer

1. Buka **Bank → Settlement kartu**. Kartu **Belum dibayar** menunjukkan pembayaran yang menunggu pencairan, perkiraan fee, dan jatuh temponya. Pilih **Pembayaran**: **Kartu**, **E-wallet**, atau **QRIS**.

   ![Kartu Belum dibayar di Settlement kartu](../screenshots/kartu-belum-01.png)

### Aturan fee (sekali, dan saat tarif berubah)

2. Di kartu **Aturan fee**, isi formulirnya.

   | Field | Wajib? | Keterangan |
   |---|---|---|
   | Metode | Ya | **Kartu**, **E-wallet**, atau **QRIS**. |
   | Persen (MDR) | Ya | Komisi acquirer, misalnya `2.5`. |
   | PPN atas fee (%) | Tidak | PPN yang dikenakan acquirer atas komisinya, misalnya `11`. `0` kalau tidak ada. |
   | Hari sampai dibayar | Ya | Berapa hari sampai pencairan, misalnya `2`. |
   | Mulai | Ya | Tanggal aturan berlaku. |

3. Klik **Tambah aturan**. Muncul "Aturan ditambahkan.".

   ![Kartu Aturan fee](../screenshots/kartu-aturan-03.png)

### Mencatat settlement

4. Buka halaman rekonsiliasi rekening koran yang memuat pencairan. Centang satu baris uang masuk dari acquirer, lalu klik **Selesaikan pembayaran kartu atau dompet digital…**.
5. Pilih **Pembayaran** (**Kartu**, **Dompet digital dan lainnya**, atau **QRIS**), lalu klik **Sarankan pembayarannya**. Pembayaran yang paling pas dicentang otomatis. Sesuaikan centangnya dengan laporan acquirer.
6. Periksa ringkasannya: "Pembayaran {kotor} · dibayarkan {bersih} · komisi {jumlah}". Ubah **PPN dalam potongan** kalau acquirer menunjukkan angka lain. Pilih **Akun komisi**, lalu isi **Departemen** dan **Keterangan** kalau perlu.

   <!-- SCREENSHOT: formulir Selesaikan pembayaran kartu pada halaman rekonsiliasi (butuh pembayaran kartu dari hari yang sudah ditutup dan baris pencairan di rekening koran) -->

7. Klik **Selesaikan dan cocokkan**. Muncul "Baris {n} diselesaikan.". Settlement tercatat di kartu **Settlement yang dibuat**.

### Membatalkan settlement yang salah

Kalau settlement dicatat dengan pembayaran, komisi, atau PPN yang keliru, batalkan settlement-nya, lalu catat ulang yang benar.

8. Buka **Bank → Settlement kartu**. Di kartu **Settlement yang dibuat**, cari settlement-nya. Kolom **Status** menunjukkan **Tercatat**. Klik **Void settlement**.
9. Isi **Alasan pembatalan**, lalu klik **Lanjut**.
10. Di jendela **Setujui pembatalan settlement**, orang yang menyetujui mengisi email dan kata sandinya. Jumlah yang dibandingkan dengan aturan persetujuan hotel adalah total pembayaran settlement (bruto).
11. Muncul "Settlement {nomor} dibatalkan: pembayarannya menunggu di-settle lagi.". Status settlement menjadi **Dibatalkan**, dengan nomor jurnal pembaliknya dan alasannya.
12. Catat ulang settlement yang benar dengan langkah 4 sampai 7.

    <!-- SCREENSHOT: kartu Settlement yang dibuat dengan tombol Void settlement dan settlement yang Dibatalkan (butuh settlement yang sudah dicatat; data contoh panduan belum punya settlement) -->

Di halaman rekonsiliasi, pencocokan baris pencairan dari settlement yang masih berlaku tidak bisa dilepas dengan **batalkan**. Tautannya menjadi **void settlement**, yang membuka **Bank → Settlement kartu**.

## Hasilnya

- Jurnal bank dibuat pada tanggal baris rekening koran: bank bertambah sebesar yang dibayarkan, komisi dan PPN-nya dibebankan, dan akun penampung berkurang sebesar jumlah kotor.
- PPN atas komisi diperlakukan menurut status PKP hotel: diklaim di SPT PPN, disimpan terpisah, atau masuk biaya.
- **Settlement yang dibatalkan:**
  - Jurnal pembalik dibuat pada tanggal settlement (tanggal baris rekening koran), sehingga bank, komisi, PPN, dan akun penampung kembali seperti sebelum settlement.
  - Pembayarannya kembali ke daftar **Belum dibayar** dan bisa di-settle lagi.
  - Baris pencairan di rekening koran kembali belum dicocokkan. Baris bank dari jurnal settlement dan baris pembaliknya saling dicocokkan otomatis, sehingga tidak tersisa di daftar yang belum dicocokkan.
  - Kalau PPN komisinya sudah diklaim di SPT yang dilaporkan, SPT bulan berikutnya menariknya kembali. Lihat [Pajak dan faktur](pajak-dan-faktur.md).

## Perlu diperhatikan

> ⚠️ Penting: setiap pembayaran hanya bisa diselesaikan oleh satu settlement yang berlaku. Settlement yang salah tidak bisa diubah, hanya dibatalkan lalu dicatat ulang. Periksa centang pembayaran dan komisinya sebelum mengklik **Selesaikan dan cocokkan**.

> ⚠️ Penting: **settlement di bulan yang sudah ditutup** tidak bisa dibatalkan begitu saja. Pembatalan bertanggal sama dengan settlement-nya, jadi pembukuan bulan itu harus dibuka ulang dulu. Membuka ulang bulan yang sudah ditutup mengubah angka laporan bulan itu, yang mungkin sudah dikirim ke pemilik atau bank. Karena itu keputusannya ada di akuntan. Sistem menampilkan pesan "Pembukuan {bulan} sudah ditutup…".
>
> **Alternatifnya:** biarkan settlement itu, lalu buat jurnal manual di bulan berjalan untuk membetulkan selisihnya. Lihat [Jurnal manual dan pembalikan](../07-accounting/jurnal-manual-dan-pembalikan.md). Kalau akuntan memutuskan membuka ulang bulannya, lihat [Periode dan tahun buku](../07-accounting/periode-dan-tahun-buku.md), lalu batalkan settlement-nya.

- **Pembayaran muncul di sini** setelah audit malam menutup hari bisnisnya. Pembayaran hari ini belum terlihat. Pembayaran kartu dari POS, dari penerimaan piutang perusahaan, dan dari deposit banquet juga ikut. Pembayaran yang di-void tidak pernah muncul. Refund ke kartu muncul sebagai baris minus setelah harinya ditutup.

- **Rekening koran yang sudah direkonsiliasi** harus dibuka kembali dulu (dengan persetujuan) sebelum settlement di dalamnya bisa dibatalkan. Lihat [Rekonsiliasi bank](rekonsiliasi-bank.md).
- **Settlement yang pencocokannya sudah dilepas** sebelum fitur pembatalan ada tetap bisa dibatalkan. Setelah itu, baris bank dari jurnal settlement dan baris pembaliknya muncul berdampingan di daftar yang belum dicocokkan. Centang keduanya, lalu cocokkan tanpa baris rekening koran.
- **Settlement yang sudah dibatalkan** tidak bisa dibatalkan lagi.

- **Aturan fee tidak bisa diubah atau dihapus.** Untuk tarif baru, tambahkan aturan baru dengan tanggal mulai yang lebih baru. Setiap pembayaran menyimpan tarif pada harinya.
- **Pembayaran sebelum ada aturan** tidak punya perkiraan fee.
- **Kolom nomor jurnal** di **Settlement yang dibuat** salah berlabel **Tanggal**.
- **Daftar aturan** menampilkan hari pencairan tanpa satuan, misalnya `Kartu · 2.5% + PPN 11% · 2 · …` (2 hari).
- **"Yang dibayar bank melebihi total pembayaran yang dipilih."**: centang pembayaran yang lebih banyak, atau pilih baris rekening koran lain.

## Terkait

- [Rekonsiliasi bank](rekonsiliasi-bank.md)
- [Menerima pembayaran](../03-kasir-dan-pembayaran/menerima-pembayaran.md)
- [Pajak dan faktur](pajak-dan-faktur.md)
