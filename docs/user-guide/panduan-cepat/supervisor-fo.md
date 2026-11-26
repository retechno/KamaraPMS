# Panduan cepat: supervisor front office

Ringkasan satu halaman untuk supervisor front office. Setiap baris menunjuk ke halaman panduan yang lengkap.

## Awal shift

1. Masuk ke KamaraPMS dan periksa **Tanggal bisnis** di bilah atas. Semua koreksi dicatat pada tanggal ini. → [Memahami tanggal bisnis](../00-memulai/memahami-tanggal-bisnis.md)
2. Buka **Hari Ini** dan periksa kartu **Perlu perhatian**. → [Masuk dan mengenal layar](../00-memulai/login-dan-navigasi.md)
3. Cari kamar yang belum siap untuk tamu yang tiba hari ini: **Kamar → Status kamar**, pilih Hunian **Dipesan** dan Kebersihan **Kotor**. → [Melihat status kamar](../02-housekeeping/status-kamar.md)
4. Kalau Anda juga menerima uang tunai, buka shift di **Tagihan → Shift kasir** → **Buka shift**. → [Shift kasir](../03-kasir-dan-pembayaran/shift-kasir.md)

## Sepanjang hari

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Staf meminta persetujuan koreksi | Ketik sendiri email dan kata sandi Anda di **Email penyetuju** dan **Kata sandi penyetuju**, lalu klik **Setujui**. | [Meminta persetujuan](../00-memulai/meminta-persetujuan.md) |
| Koreksi Anda sendiri ditolak: "Aturan properti ini meminta penyetuju selain orang yang membuat perubahan." | Minta orang lain yang berwenang menyetujuinya. | [Meminta persetujuan](../00-memulai/meminta-persetujuan.md) |
| Check-in keliru, masih hari yang sama | **Front Desk → Tamu hari ini**, tab **Menginap**, **Lihat** → **Batalkan check-in**, isi **Alasan**, klik **Batalkan**. Hanya bisa sebelum ada biaya dan sebelum pindah kamar. | [Membatalkan check-in](../01-front-desk/membatalkan-check-in.md) |
| Tamu harus masuk ke kamar yang belum bersih | Di langkah **Kamar** saat check-in, centang **Tetap check-in** dan isi alasannya, kalau peran Anda mencakupnya. | [Check-in](../01-front-desk/check-in.md) |
| Biaya salah diposting hari ini | Di folio, klik **Reverse** pada baris itu, isi **Alasan**, klik **Lanjut ke persetujuan**. | [Reverse dan penyesuaian](../03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md) |
| Biaya hari sebelumnya salah, atau tamu diberi potongan | Tab **Penyesuaian** di folio. Isi angka negatif untuk mengurangi tagihan, lalu **Lanjut ke persetujuan**. | [Reverse dan penyesuaian](../03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md) |
| Pembayaran salah dicatat hari ini | Klik **Void** pada baris pembayaran, isi **Alasan**, lalu minta persetujuan. Posting ulang yang benar. | [Void dan refund](../03-kasir-dan-pembayaran/void-dan-refund.md) |
| Uang tamu dikembalikan, misalnya sisa deposit | Klik **Refund** pada baris pembayaran, isi **Jumlah**, **Refund lewat**, dan **Alasan**. | [Void dan refund](../03-kasir-dan-pembayaran/void-dan-refund.md) |
| Tarif tamu menginap diturunkan | Tab **Menginap** → **⋮** → **Ubah tarif** → **Terapkan**. Tarif yang lebih rendah membuka kotak **Setujui koreksi**. | [Mengubah tarif tamu yang sedang menginap](../01-front-desk/mengubah-tarif-tamu-menginap.md) |
| Biaya masuk ke folio yang salah | Centang biayanya → **Transfer…** → pilih **Folio tujuan**, isi **Alasan**, klik **Lanjut ke persetujuan**. | [Transfer antar folio](../03-kasir-dan-pembayaran/transfer-antar-folio.md) |
| Transfer ternyata keliru | Di folio asal: **Tampilkan yang ditransfer** → **Kembalikan**. | [Transfer antar folio](../03-kasir-dan-pembayaran/transfer-antar-folio.md) |
| Tamu tidak datang | Buka reservasinya. Di kartu kamar, klik **Tidak datang** → **Tandai no-show**. Biayanya lewat **Posting biaya no-show**. | [Membatalkan reservasi dan tamu tidak datang](../01-front-desk/membatalkan-reservasi-dan-no-show.md), [Menandai no-show](../11-audit-malam/menandai-no-show.md) |
| Rombongan butuh satu tagihan | **Front Desk → Grup**, buka grupnya → **Buka master folio**, centang **Biaya kamar semua anggota**, klik **Simpan pengaturan**. | [Grup dan master folio](../06-corporate-group-city-ledger/grup-dan-master-folio.md) |
| Biaya anggota grup sudah terposting di folio tamu | Di kartu master folio, klik **Pindahkan ke master folio…**, isi **Alasan**, **Lanjut ke persetujuan**. | [Grup dan master folio](../06-corporate-group-city-ledger/grup-dan-master-folio.md) |
| Rombongan tiba | **Check-in grup** dari halaman grupnya. | [Grup dan master folio](../06-corporate-group-city-ledger/grup-dan-master-folio.md) |
| Tagihan tamu ditanggung perusahaan | Folio → tab **Transfer** → pilih perusahaannya, klik **Transfer**, lalu check-out. | [Transfer ke piutang perusahaan](../06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md) |
| Kamar rusak dan tidak boleh dijual | **Kamar → Blokir kamar**: pilih **Rusak (OOO)** atau **Tidak dilayani (OOS)**, klik **Buat blokir**. Kalau peran Anda mencakupnya. | [Blokir kamar (OOO/OOS)](../02-housekeeping/blokir-kamar-ooo-oos.md) |
| Beban restoran ke kamar ditolak | **Tagihan → Beban outlet** → **Ditolak** → **Buka folio**, lalu ubah batas atau outletnya kalau tamu memang boleh. | [Membuka folio untuk outlet](../03-kasir-dan-pembayaran/membuka-folio-untuk-outlet.md) |
| Manajer meminta angka front office | Buka laporan front office. | [Laporan front office](../12-laporan/laporan-front-office.md) |

## Akhir shift

1. Periksa daftar **Shift**: shift kasir yang masih terbuka bisa menahan audit malam. Minta kasir menutupnya. → [Shift kasir](../03-kasir-dan-pembayaran/shift-kasir.md)
2. Tandai tamu yang tidak datang dengan **Tidak datang**, atau serahkan ke night auditor. → [Membatalkan reservasi dan tamu tidak datang](../01-front-desk/membatalkan-reservasi-dan-no-show.md)
3. Pastikan tab **Keberangkatan** kosong, kecuali tamu yang memang terlambat check-out. → [Check-out](../01-front-desk/check-out.md)
4. Klik inisial nama Anda → **Keluar**. → [Masuk dan mengenal layar](../00-memulai/login-dan-navigasi.md)

## Ingat

- Persetujuan Anda dicatat atas nama Anda: riwayat koreksi menyimpan siapa yang membuat dan siapa yang menyetujui.
- **Reverse** hanya untuk biaya hari ini. Biaya kemarin dikoreksi dengan **Penyesuaian**. Pembayaran dikoreksi dengan **Void** (hari ini) atau **Refund**.
- Void dan refund tidak bisa dibatalkan. Kalau keliru, posting pembayaran baru.
- Check-in yang dibatalkan membuat kamar **Kotor**. Setelah audit malam, check-in tidak bisa dibatalkan lagi.
- Setiap grup hanya punya satu master folio.
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
