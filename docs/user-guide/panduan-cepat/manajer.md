# Panduan cepat: manajer

Ringkasan satu halaman untuk general manager dan manajer departemen: dasbor, laporan, dan persetujuan. Setiap baris menunjuk ke halaman panduan yang lengkap.

## Setiap pagi

1. Buka **Akhir hari → Kinerja**. Pilih **Periode** **Hari ini** atau **Bulan berjalan** untuk okupansi, ADR, RevPAR, dan pendapatan kamar. → [Kinerja dan dasbor](../12-laporan/kinerja-dan-dasbor.md)
2. Lihat kartu **14 malam ke depan**. Batang berwarna berarti **90% atau lebih: pertimbangkan naikkan harga**. → [Kinerja dan dasbor](../12-laporan/kinerja-dan-dasbor.md)
3. Buka **Akhir hari → Laporan** → **Daily flash report** untuk hari yang baru ditutup. Kalau ada "Control balance berbeda: …", minta akuntan memeriksanya. → [Laporan harian](../12-laporan/laporan-harian.md)
4. Baca paket audit malam di e-mail, atau di **Akhir hari → Arsip audit malam**. Peringatan malam itu ada di catatan auditnya. → [Paket audit malam](../11-audit-malam/paket-audit-malam.md)
5. Klik lonceng di bilah atas, atau buka **Persediaan → Perlu saya setujui**, untuk melihat permintaan yang menunggu Anda. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)

## Persetujuan

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Staf meminta persetujuan koreksi di layarnya, misalnya penyesuaian, void, atau refund | Periksa koreksinya. Ganti **Email penyetuju** dengan email Anda, ketik **Kata sandi penyetuju**, klik **Setujui**. | [Meminta persetujuan](../00-memulai/meminta-persetujuan.md) |
| Selisih kas kasir melewati batas saat tutup shift | Di jendela **Setujui selisih**, isi email dan kata sandi Anda, lalu klik **Setujui**. | [Shift kasir](../03-kasir-dan-pembayaran/shift-kasir.md) |
| Permintaan pembelian atau permintaan gudang menunggu tingkat Anda | Klik nomornya, periksa barang dan harganya, isi **Catatan**, lalu **Setujui** atau **Kembalikan**. Catatan wajib untuk **Kembalikan**. | [Rantai persetujuan](../09-inventory/rantai-persetujuan.md) |
| Opname berstatus **Perlu disetujui** | **Persediaan → Opname** → buka opnamenya → periksa **Selisih** dan **Total selisih** → **Setujui opname**. | [Opname](../09-inventory/opname.md) |
| Harga faktur pemasok di atas toleransi | Di kotak **Persetujuan selisih harga**, isi **E-mail penyetuju** dan **Kata sandi**. | [Penerimaan, tagihan, dan retur](../09-inventory/penerimaan-tagihan-dan-retur.md) |
| Ada pengajuan pelepasan aset | **Aset tetap → Pelepasan aset**, tab **Menunggu persetujuan** → **Setujui**, atau **Tolak** dengan **Alasan penolakan**. | [Pelepasan aset](../10-aset-tetap/pelepasan-aset.md) |
| Anggaran tahun baru akan diaktifkan | Di jendela **Setujui anggaran**, isi email dan kata sandi Anda, lalu klik **Setujui**. | [Anggaran](../08-keuangan-back-office/anggaran.md) |
| Night auditor melaporkan shift kasir yang ditinggal terbuka | Minta kasir itu menutupnya sendiri. Kalau tidak bisa, putuskan apakah **Shift terbuka menahan audit malam** di kartu **Pengaturan kasir** (halaman **Tagihan → Shift kasir**) dimatikan sementara. | [Menangani penghalang](../11-audit-malam/menangani-penghalang.md) |

## Setiap minggu dan setiap bulan

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Rapat okupansi | **Akhir hari → Laporan** → **Okupansi dan statistik** → **Jalankan** → **Unduh CSV**. | [Laporan front office](../12-laporan/laporan-front-office.md) |
| Memeriksa kamar gratis | Laporan **Kamar gratis per alasan** atau **Kamar gratis per jenis**. | [Laporan front office](../12-laporan/laporan-front-office.md) |
| Memeriksa pembatalan, void, dan diskon | **Pembatalan dan no-show** dan **Void, koreksi dan diskon** di kartu **Laporan harian**. | [Laporan harian](../12-laporan/laporan-harian.md) |
| Laporan keuangan bulanan | **Akuntansi → Laporan laba rugi**, **Neraca**, dan **Laporan arus kas** → **PDF** atau **Ekspor CSV**. | [Laporan keuangan](../07-accounting/laporan-keuangan.md) |
| Membandingkan dengan anggaran | **Anggaran → Anggaran terhadap realisasi** → **Tampilkan**. | [Anggaran](../08-keuangan-back-office/anggaran.md) |
| Biaya makanan dan minuman | **Persediaan → Ringkasan** → **Biaya harian** atau **Biaya F&B per outlet**. | [Laporan persediaan](../09-inventory/laporan-persediaan.md) |
| Ada yang dipertanyakan: siapa mengubah apa | **Pengaturan → Jejak audit** → isi **Nomor dokumen** → **Cari** → **Detail**. | [Jejak audit](../12-laporan/jejak-audit.md) |
| Memeriksa koreksi yang disetujui sendiri oleh administrator | Di **Jejak audit**, centang **Hanya persetujuan yang diberikan admin untuk koreksinya sendiri melawan aturan properti** → **Cari**. | [Jejak audit](../12-laporan/jejak-audit.md) |

## Ingat

- Persetujuan Anda dicatat atas nama Anda. **Setujui** di rantai persetujuan tidak bisa ditarik, dan persetujuan opname final. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)
- Anda tidak bisa menyetujui permintaan Anda sendiri, dan satu orang hanya menyetujui satu tingkat dari satu dokumen. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)
- Izin persetujuan Anda sudah cukup untuk membuka **Persediaan → Pembelian** dan permintaan yang menunggu Anda, walaupun peran Anda tidak boleh melihat barang dan stok. → [Rantai persetujuan](../09-inventory/rantai-persetujuan.md)
- Pembayaran perusahaan melunasi invoice hanya kalau dibagi ke invoicenya di **Catat pembayaran dari perusahaan** (terisi dari invoice tertua) atau dicatat lewat **Bayar**. Bagian yang **Belum dialokasikan** menjadi uang muka, dan invoicenya tetap muncul di **Invoice jatuh tempo**. Uang muka itu bisa dialokasikan belakangan lewat **Alokasikan** di rekening koran, juga setelah audit malam. → [Invoice dan penerimaan](../06-corporate-group-city-ledger/invoice-dan-penerimaan.md)
- Angka di **Kinerja** dan sebagian besar laporan harian berasal dari hari yang sudah ditutup audit malam. ADR dan RevPAR hari ini menulis "Tersedia setelah audit malam". → [Kinerja dan dasbor](../12-laporan/kinerja-dan-dasbor.md)
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
