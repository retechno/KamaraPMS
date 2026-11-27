# Panduan cepat: akuntan

Ringkasan satu halaman untuk staf akuntansi dan keuangan. Setiap baris menunjuk ke halaman panduan yang lengkap.

## Awal hari

1. Masuk ke KamaraPMS dan periksa **Tanggal bisnis** di bilah atas. Tanggal jurnal dan tagihan tidak boleh setelah tanggal ini. → [Memahami tanggal bisnis](../00-memulai/memahami-tanggal-bisnis.md)
2. Ingat bahwa transaksi folio, kasir, dan outlet baru masuk buku besar setelah audit malam menutup harinya. → [Laporan keuangan](../07-accounting/laporan-keuangan.md)
3. Buka **Akuntansi → Akun kontrol**, klik **Hari tutup terakhir**, lalu **Periksa**. Kalau ada **Selisih**, laporkan ke kepala akuntansi. → [Laporan keuangan](../07-accounting/laporan-keuangan.md)
4. Kalau ada hari bisnis tertutup yang belum punya jurnal, klik **Jurnalkan hari yang hilang** di halaman jurnal, kalau peran Anda mencakupnya. → [Jurnal manual dan pembalikan](../07-accounting/jurnal-manual-dan-pembalikan.md)

## Sepanjang hari

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Faktur pemasok tiba | **Utang usaha → Tagihan pemasok** → **Catat tagihan**, isi kepala dan barisnya, klik **Catat tagihan**. | [Utang usaha](../08-keuangan-back-office/utang-usaha.md) |
| Hotel membayar pemasok | **Utang usaha → Pembayaran pemasok** → **Bayar pemasok**, isi **Bayar** per tagihan, klik **Lakukan pembayaran**. | [Utang usaha](../08-keuangan-back-office/utang-usaha.md) |
| Pemasok memberi potongan atau menerima retur | Buka tagihannya → **Nota kredit…** → **Catat nota kredit**. | [Utang usaha](../08-keuangan-back-office/utang-usaha.md) |
| Tagihan atau pembayaran pemasok salah | **Batalkan…**, isi **Alasan**, **Batalkan dengan persetujuan**. Batalkan pembayarannya dulu, baru tagihannya. | [Utang usaha](../08-keuangan-back-office/utang-usaha.md) |
| Rekening koran bank tersedia | **Bank → Mutasi bank** → **Impor rekening koran** → **Rekonsiliasi** → **Cocokkan otomatis**. | [Rekonsiliasi bank](../08-keuangan-back-office/rekonsiliasi-bank.md) |
| Biaya administrasi atau bunga hanya ada di bank | Centang barisnya → **Posting baris {n} ke buku…**, pilih akunnya, klik **Posting dan cocokkan**. | [Rekonsiliasi bank](../08-keuangan-back-office/rekonsiliasi-bank.md) |
| Semua baris cocok dan **Selisih** nol | Klik **Rekonsiliasi**. | [Rekonsiliasi bank](../08-keuangan-back-office/rekonsiliasi-bank.md) |
| Pencairan kartu atau QRIS masuk bank | Di halaman rekonsiliasi: **Selesaikan pembayaran kartu atau dompet digital…** → **Sarankan pembayarannya** → **Selesaikan dan cocokkan**. | [Settlement kartu](../08-keuangan-back-office/settlement-kartu.md) |
| Tarif komisi acquirer berubah | **Bank → Settlement kartu** → kartu **Aturan fee** → **Tambah aturan** dengan tanggal mulai yang baru. | [Settlement kartu](../08-keuangan-back-office/settlement-kartu.md) |
| Tamu perusahaan sudah check-out | **Tagihan → Piutang perusahaan** → kode perusahaan → kartu **Invoice baru** → **Buat invoice ({n} dipilih, {total})** → **Cetak**. | [Invoice dan penerimaan](../06-corporate-group-city-ledger/invoice-dan-penerimaan.md) |
| Perusahaan membayar invoice | Klik **Bayar** pada invoicenya, isi formulir, klik **Catat pembayaran**. | [Invoice dan penerimaan](../06-corporate-group-city-ledger/invoice-dan-penerimaan.md) |
| Perusahaan diberi potongan | **Nota kredit…** pada baris invoice → **Lanjutkan** → penyetuju **Setujui**. | [Nota kredit dan penghapusan piutang](../06-corporate-group-city-ledger/nota-kredit-dan-penghapusan-piutang.md) |
| Utang perusahaan tidak akan tertagih | **Penghapusan piutang…** pada baris invoice → **Lanjutkan**, lalu minta persetujuan. | [Nota kredit dan penghapusan piutang](../06-corporate-group-city-ledger/nota-kredit-dan-penghapusan-piutang.md) |
| Invoice lewat jatuh tempo (berkala, misalnya mingguan) | **Tagihan → Invoice jatuh tempo** → **Kirim surat peringatan…** → **Catat dan cetak**. | [Surat peringatan](../06-corporate-group-city-ledger/surat-peringatan.md) |
| Transaksi di luar modul lain, misalnya kas kecil | **Akuntansi → Jurnal** → **Jurnal baru**. Setelah **Seimbang**, klik **Posting**. | [Jurnal manual dan pembalikan](../07-accounting/jurnal-manual-dan-pembalikan.md) |
| Jurnal manual salah | Buka jurnalnya → **Balik…**, isi **Alasan**, **Balik dengan persetujuan**. Lalu posting jurnal yang benar. | [Jurnal manual dan pembalikan](../07-accounting/jurnal-manual-dan-pembalikan.md) |

## Setiap bulan

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Bulan pajak sudah berakhir | **Pajak → SPT Masa PPN** → pilih **Pajak** → klik bulannya → **PDF** → **Laporkan SPT**. | [Pajak dan faktur](../08-keuangan-back-office/pajak-dan-faktur.md) |
| Pajak sudah disetor | Di bulan itu, klik **Catat pembayaran**. Periksa sisanya di **Pajak → Pajak terutang**. | [Pajak dan faktur](../08-keuangan-back-office/pajak-dan-faktur.md) |
| Laporan manajemen | **Akuntansi → Neraca saldo**, **Akuntansi → Laporan laba rugi**, **Neraca**, dan **Laporan arus kas**, lalu **PDF** atau **Ekspor CSV**. | [Laporan keuangan](../07-accounting/laporan-keuangan.md) |
| Laporan per departemen | **Akuntansi → Laporan departemen**, klik **Bulan lalu**, lalu **Tampilkan**. | [Departemen](../07-accounting/departemen.md) |
| Membandingkan dengan anggaran | **Anggaran → Anggaran terhadap realisasi** → **Tampilkan**. | [Anggaran](../08-keuangan-back-office/anggaran.md) |
| Rencana anggaran berubah | **Revisi (salin)**, ubah drafnya, lalu **Aktifkan** dengan persetujuan. | [Anggaran](../08-keuangan-back-office/anggaran.md) |
| Memeriksa umur utang | **Utang usaha → Umur utang usaha**, klik baris pemasok untuk melihat tagihannya. | [Utang usaha](../08-keuangan-back-office/utang-usaha.md) |

## Akhir bulan

1. Pastikan semua hari bisnis bulan itu sudah ditutup audit malam dan dijurnal: **Hari yang dijurnalkan** lengkap, misalnya `30 dari 30`. → [Periode dan tahun buku](../07-accounting/periode-dan-tahun-buku.md)
2. Kalau hotel memakai aset tetap, jalankan penyusutan bulan itu. → [Penyusutan bulanan](../10-aset-tetap/penyusutan-bulanan.md)
3. Periksa **Akun kontrol** dan pastikan laporan tidak menampilkan peringatan "Buku tidak seimbang sebesar …". → [Laporan keuangan](../07-accounting/laporan-keuangan.md)
4. Buka **Akuntansi → Periode akuntansi**, klik **Tutup** pada bulannya, lalu **Konfirmasi**. → [Periode dan tahun buku](../07-accounting/periode-dan-tahun-buku.md)
5. Di akhir tahun, setelah semua bulan ditutup: **Akuntansi → Tahun buku** → **Tutup tahun** → **Konfirmasi**. → [Periode dan tahun buku](../07-accounting/periode-dan-tahun-buku.md)

## Ingat

- Jurnal yang sudah diposting tidak bisa diubah atau dihapus. Koreksinya dengan pembalikan, lalu jurnal baru yang benar.
- Tagihan, pembayaran, dan nota kredit pemasok tidak bisa diubah. Batalkan dengan persetujuan, lalu catat ulang.
- Settlement kartu tidak bisa dibatalkan atau dikoreksi. Periksa centang pembayaran dan komisinya dulu.
- Pembayaran perusahaan dibagi ke invoice di **Catat pembayaran dari perusahaan** (terisi dari invoice tertua) atau lewat **Bayar** pada satu invoice. Periksa **Belum dialokasikan** sebelum mencatat: penerimaan tidak bisa dialokasikan belakangan. → [Invoice dan penerimaan](../06-corporate-group-city-ledger/invoice-dan-penerimaan.md)
- Menutup periode mengunci laporan. Hanya bulan tertutup terakhir yang bisa dibuka kembali.
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
