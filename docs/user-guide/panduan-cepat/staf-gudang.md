# Panduan cepat: staf gudang dan persediaan

Ringkasan satu halaman untuk staf gudang, receiving, dan persediaan. Setiap baris menunjuk ke halaman panduan yang lengkap.

## Awal hari

1. Masuk ke KamaraPMS dan periksa **Tanggal bisnis** di bilah atas. Semua mutasi stok dicatat pada tanggal ini, dan jurnalnya dibuat saat audit malam menutup hari. → [Memahami tanggal bisnis](../00-memulai/memahami-tanggal-bisnis.md)
2. Buka **Persediaan → Ringkasan**. Periksa **Barang di bawah nol**, **Barang di bawah minimum**, dan **Dokumen menunggu keputusan saya**. → [Laporan persediaan](../09-inventory/laporan-persediaan.md)
3. Buka **Persediaan → Harga pokok**, bagian **Penjualan POS yang belum masuk stok**. Baris **Gagal** menahan audit malam: klik **Coba lagi**, atau **Batalkan** kalau penjualan itu memang tidak perlu memotong stok. → [Menyiapkan persediaan](../09-inventory/menyiapkan-persediaan.md)
4. Buka **Persediaan → Permintaan & transfer** → **Siap dikirim · {n}**, dan kirim barangnya sebelum outlet buka. → [Permintaan barang dan transfer](../09-inventory/permintaan-barang-dan-transfer.md)

## Sepanjang hari

| Yang terjadi | Yang Anda lakukan | Halaman |
|---|---|---|
| Dapur, bar, atau housekeeping meminta barang | Buka permintaan yang **Siap dikirim** → **Jawab dengan transfer** → periksa kolom **Kirim** → **Kirim transfer**. | [Permintaan barang dan transfer](../09-inventory/permintaan-barang-dan-transfer.md) |
| Stok tidak cukup untuk seluruh permintaan | Kirim sebanyak yang ada; sisanya tetap di permintaan. Kalau sisanya tidak bisa dipenuhi, klik **Tolak** dan isi **Alasan**. | [Permintaan barang dan transfer](../09-inventory/permintaan-barang-dan-transfer.md) |
| Barang dipindah tanpa permintaan | **Transfer bebas** → pilih **Dari** dan **Ke** → **Tambah barang** → **Kirim transfer**. | [Permintaan barang dan transfer](../09-inventory/permintaan-barang-dan-transfer.md) |
| Transfer salah, pada hari yang sama | Buka transfernya → **Balik transfer** → isi **Alasan pembalikan**. Sesudah hari itu, buat transfer ke arah sebaliknya. | [Permintaan barang dan transfer](../09-inventory/permintaan-barang-dan-transfer.md) |
| Stok menipis | Di laporan **Barang di bawah minimum**, centang barang dari satu gudang → **Buat PR**. | [Laporan persediaan](../09-inventory/laporan-persediaan.md) |
| Barang perlu dibeli dari pemasok | **Persediaan → Pembelian** → **Permintaan baru** → **Tambah barang** → **Ajukan untuk persetujuan**. | [Permintaan dan pesanan pembelian](../09-inventory/permintaan-dan-pesanan-pembelian.md) |
| Permintaan pembelian sudah **Disetujui** | Buka permintaannya. Di kotak **Pesanan pembelian**, periksa **Jumlah**, **Pemasok**, **Harga pesanan**, dan **Kirim ke** → **Buat {n} pesanan**. | [Permintaan dan pesanan pembelian](../09-inventory/permintaan-dan-pesanan-pembelian.md) |
| Barang pesanan datang | **Pembelian**, tab **Pesanan** → nomor pesanan → **Terima barang** → isi **Surat jalan** dan **Diterima sekarang** → **Posting penerimaan**. | [Penerimaan, tagihan, dan retur](../09-inventory/penerimaan-tagihan-dan-retur.md) |
| Barang rusak atau salah kirim | Di halaman penerimaan → **Retur ke pemasok** → isi **Alasan** dan **Jumlah** → **Posting retur**. | [Penerimaan, tagihan, dan retur](../09-inventory/penerimaan-tagihan-dan-retur.md) |
| Penerimaan salah input, belum ditagih | Pada hari yang sama: **Batalkan penerimaan**, isi **Alasan**. Setelah hari itu, kembalikan barangnya dengan retur. | [Penerimaan, tagihan, dan retur](../09-inventory/penerimaan-tagihan-dan-retur.md) |
| Departemen mengambil barang untuk dipakai | **Persediaan → Pemakaian & waste** → **Pemakaian baru** → isi gudang, departemen, dan barangnya → **Posting pemakaian**. | [Pemakaian dan waste](../09-inventory/pemakaian-dan-waste.md) |
| Barang basi, kedaluwarsa, atau rusak | **Catat waste** → pilih **Alasan** → **Posting waste**. Catat pada hari yang sama. | [Pemakaian dan waste](../09-inventory/pemakaian-dan-waste.md) |
| Bahan dipakai untuk makan staf atau jamuan | **Makan karyawan** (isi **Porsi**) atau **Entertainment** → posting dokumennya. | [Pemakaian dan waste](../09-inventory/pemakaian-dan-waste.md) |
| Dokumen pemakaian atau waste salah, pada hari yang sama | Buka dokumennya → **Balik** → isi **Alasan**. Lalu posting ulang dokumen yang benar. | [Pemakaian dan waste](../09-inventory/pemakaian-dan-waste.md) |
| Ada barang atau gudang baru | **Persediaan → Barang & stok** → **Barang baru**, atau **Persediaan → Gudang** → **Gudang baru**. | [Barang, gudang, dan saldo awal](../09-inventory/barang-gudang-dan-saldo-awal.md) |
| Stok amenities housekeeping kurang | Isi lagi gudang housekeeping dengan transfer. Tutup hari tetap jalan, tetapi saldonya jadi minus. | [Amenities otomatis](../09-inventory/amenities-otomatis.md) |

## Opname

1. Sepakati jam mulai dengan outlet: sejak opname dimulai, gudang dibekukan. Transfer, pemakaian, dan penerimaan ke gudang itu ditolak. → [Opname](../09-inventory/opname.md)
2. **Persediaan → Opname** → **Mulai opname** → pilih **Gudang** dan **Yang dihitung** → **Mulai dan bekukan**. → [Opname](../09-inventory/opname.md)
3. Ketik jumlah yang ditemukan di rak untuk setiap barang. Jumlah buku tidak ditampilkan (hitung buta). → [Opname](../09-inventory/opname.md)
4. Klik **Kirim untuk disetujui**, lalu minta penyetuju mengklik **Setujui opname** pada hari yang sama, sebelum audit malam. → [Opname](../09-inventory/opname.md)
5. Periksa hasilnya di laporan **Selisih opname**. → [Laporan persediaan](../09-inventory/laporan-persediaan.md)

## Ingat

- Dokumen yang sudah diposting tidak bisa diedit. Pembalikan hanya bisa pada hari diposting. Tidak ada layar "penyesuaian stok": selisih di rak dikoreksi lewat opname.
- **Posting saldo awal** langsung memposting tanpa konfirmasi dan tidak bisa dibatalkan. Periksa setiap baris dulu. → [Barang, gudang, dan saldo awal](../09-inventory/barang-gudang-dan-saldo-awal.md)
- Opname yang masih terbuka menahan audit malam, dan persetujuannya final. → [Opname](../09-inventory/opname.md)
- Di formulir barang, bulatan **Bawaan beli** dan **Bawaan resep** tidak tampil terisi setelah diklik, walaupun pilihannya tersimpan. Setelah menyimpan, buka barangnya lagi untuk memeriksanya. → [Barang, gudang, dan saldo awal](../09-inventory/barang-gudang-dan-saldo-awal.md)
- Kotak berlabel "Simpan dan ajukan" di kotak **Pesanan pembelian** langsung mengirim pesanan ke pemasok (**Terkirim ke pemasok**). Centang hanya kalau pesanan memang dikirim sekarang; tanpa centang, pesanan tersimpan sebagai **Draf**. → [Permintaan dan pesanan pembelian](../09-inventory/permintaan-dan-pesanan-pembelian.md)
- Akun persediaan dan jenis kategori terkunci begitu kategori berisi barang. Periksa keduanya sebelum menambahkan barang. Kalau terlanjur salah, buat kategori baru yang benar dan pindahkan barangnya lewat **Kategori** di formulir barang, selama barangnya belum punya stok. → [Menyiapkan persediaan](../09-inventory/menyiapkan-persediaan.md)
- Kalau menu atau tombol tidak ada, peran Anda tidak mencakupnya. Tanyakan ke administrator. → [Peran dan hak akses](../00-memulai/peran-dan-hak-akses.md)
