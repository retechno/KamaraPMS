# Pertanyaan yang sering diajukan (FAQ)

Halaman ini menjawab pertanyaan yang paling sering muncul saat bekerja dengan KamaraPMS, masing-masing dengan tautan ke halaman panduan yang lengkap.

## Masuk dan akun

### Kenapa layar saya berbahasa Inggris?

Bahasa bawaan KamaraPMS adalah English, dan pilihan bahasa disimpan di browser, bukan di akun Anda. Di komputer lain, browser lain, jendela penyamaran, atau setelah riwayat browser dihapus, layar kembali berbahasa Inggris. Pilih **Bahasa Indonesia** di pojok kanan bawah kotak masuk, atau lewat tombol inisial nama Anda → **Bahasa**.
→ [Mengganti bahasa dan tampilan](00-memulai/mengganti-bahasa-dan-tampilan.md)

### Saya lupa kata sandi. Bagaimana cara meresetnya?

Tidak ada reset kata sandi lewat email. Klik **Lupa kata sandi?** di halaman masuk: layar meminta Anda menghubungi administrator hotel, yang akan mengatur kata sandi baru untuk Anda. Setelah masuk, ganti kata sandi itu di **Akun saya**.
→ [Masuk dan mengenal layar](00-memulai/login-dan-navigasi.md)

### Kenapa muncul "Terlalu banyak percobaan gagal. Tunggu beberapa menit lalu coba lagi."?

Akun Anda dikunci sementara karena terlalu banyak percobaan masuk yang gagal. Tunggu beberapa menit, lalu coba lagi. Periksa **Kode tenant**, **Email**, dan **Kata sandi**, termasuk huruf besar dan kecil.
→ [Masuk dan mengenal layar](00-memulai/login-dan-navigasi.md)

### Setelah masuk, muncul kartu "Belum ada properti untuk Anda". Apa artinya?

Akun Anda belum ditugaskan ke hotel mana pun. Minta administrator hotel menambahkan properti dan peran untuk Anda.
→ [Masuk dan mengenal layar](00-memulai/login-dan-navigasi.md)

### Bagaimana cara mengganti kata sandi saya sendiri?

Klik tombol inisial nama Anda → **Akun saya**, lalu isi kartu **Ubah kata sandi** dan klik **Ubah kata sandi**. Kata sandi baru minimal 12 karakter. Setelah itu sesi Anda di komputer atau ponsel lain dikeluarkan.
→ [Mengubah kata sandi](00-memulai/mengubah-kata-sandi.md)

## Peran, hak akses, dan persetujuan

### Kenapa menu atau tombol yang disebut di panduan tidak ada di layar saya?

Menu dan tombol mengikuti peran Anda di properti itu. Kalau sebuah menu tidak muncul, atau tombol menolak dengan "Peran Anda tidak mengizinkan tindakan ini.", peran Anda tidak memuat izinnya. Minta izin ke administrator hotel, atau minta rekan yang berwenang mengerjakannya.
→ [Peran dan hak akses](00-memulai/peran-dan-hak-akses.md)

### Kenapa saya diminta email dan kata sandi atasan?

Beberapa koreksi butuh persetujuan orang kedua, misalnya menurunkan tarif, membalik tagihan, penyesuaian, void, dan refund. Penyetuju mengetik **Email penyetuju** dan **Kata sandi penyetuju** miliknya sendiri langsung di layar Anda, lalu mengklik **Setujui**. Persetujuan tidak dikirim lewat pesan atau email, dan kata sandinya tidak disimpan.
→ [Meminta persetujuan](00-memulai/meminta-persetujuan.md)

### Kenapa saya tidak boleh menyetujui koreksi saya sendiri?

Aturan properti bisa meminta penyetuju selain orang yang membuat perubahan, dan kotak persetujuan menuliskan aturan itu. Kalau muncul "Aturan properti ini meminta penyetuju selain orang yang membuat perubahan.", minta orang lain yang berwenang menyetujuinya.
→ [Meminta persetujuan](00-memulai/meminta-persetujuan.md)

## Tanggal bisnis

### Kenapa tanggal di aplikasi berbeda dengan tanggal kalender?

Yang tampil di bilah atas adalah **Tanggal bisnis**, tanggal operasional hotel. Tanggal ini tidak berganti saat tengah malam, tetapi baru maju satu hari setelah audit malam selesai. Pembayaran tamu yang check-out pukul 01.30 dini hari tetap tercatat pada tanggal bisnis yang belum ditutup.
→ [Memahami tanggal bisnis](00-memulai/memahami-tanggal-bisnis.md)

### Bisakah tanggal bisnis diganti secara manual?

Tidak. Tidak ada tombol untuk mengganti tanggal: hanya audit malam yang memajukan tanggal bisnis. Kalau audit malam tertinggal dua hari, jalankan dua kali.
→ [Memahami tanggal bisnis](00-memulai/memahami-tanggal-bisnis.md)

### Muncul "Tanggal bisnis sudah berganti: muat ulang lalu coba lagi." Apa yang harus saya lakukan?

Audit malam selesai saat layar Anda masih terbuka dengan tanggal lama. Tekan `F5` untuk memuat ulang halaman, lalu ulangi pekerjaan Anda.
→ [Memahami tanggal bisnis](00-memulai/memahami-tanggal-bisnis.md)

### Hari kemarin sudah ditutup, tetapi ada yang salah. Bisakah hari itu dibuka lagi?

Tidak. Hari bisnis yang sudah ditutup tidak bisa dibuka kembali, dan catatan ke hari itu ditolak dengan "Hari bisnis sudah ditutup.". Kesalahan yang ditemukan kemudian dikoreksi pada tanggal bisnis yang sedang terbuka.
→ [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md)

## Reservasi

### Kenapa tombol Pesan tidak bisa diklik?

Arahkan kursor ke tombol **Pesan** untuk melihat alasannya. Biasanya kamar tipe itu sudah habis, rombongan terlalu besar untuk tipe kamarnya, ada malam yang belum punya harga di grid tarif, atau menginap itu melanggar pembatasan penjualan, misalnya minimal menginap 2 malam.
→ [Membuat reservasi](01-front-desk/membuat-reservasi.md)

### Apakah reservasi Draf sudah menahan kamar?

Belum. Reservasi **Draf** tidak mengurangi sisa kamar, jadi konfirmasinya bisa ditolak kalau kamar tipe itu habis lebih dulu. Konfirmasi butuh tamu pemesan. Kalau draf tersimpan tanpa pemesan, tambahkan lewat **Ubah reservasi** → **Cari pemesan**.
→ [Mengonfirmasi dan mengubah reservasi](01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md)

### Tamu sudah saya tandai no-show, ternyata ia datang. Bisakah tandanya dibatalkan?

Tidak. Tanda no-show tidak bisa dibatalkan, dan kamar yang sudah ditandai no-show tidak bisa di-check-in lagi. Buat reservasi atau walk-in baru untuk tamu itu.
→ [Menandai tamu tidak datang (no-show)](11-audit-malam/menandai-no-show.md)

### Apakah menandai no-show langsung menagih biaya ke tamu?

Tidak. Menandai no-show tidak menagih apa pun, dan deposit tamu tidak dikembalikan otomatis. Kalau hotel mengenakan biaya, klik **Posting biaya no-show** di kartu kamar reservasinya. Biaya itu hanya bisa diposting sekali: kalau jumlahnya salah, koreksi biaya itu di folio.
→ [Membatalkan reservasi dan tamu tidak datang](01-front-desk/membatalkan-reservasi-dan-no-show.md)

### Reservasi dibatalkan, tetapi masih ada deposit. Apa yang harus saya lakukan?

Muncul pesan "Folio masih menahan {jumlah}: refund deposit atau posting biaya.". Posting biaya pembatalannya kalau ada, lalu kembalikan sisanya dengan refund dari halaman folio.
→ [Membatalkan reservasi dan tamu tidak datang](01-front-desk/membatalkan-reservasi-dan-no-show.md)

### Saya salah mencatat deposit. Bisakah dihapus?

Tidak. Deposit yang sudah tersimpan tidak bisa dihapus. Koreksinya dengan membatalkan pembayaran (void) di hari yang sama, atau dengan refund, dari halaman folio. Keduanya butuh izin dan persetujuan.
→ [Menerima deposit](01-front-desk/menerima-deposit.md)

## Check-in dan check-out

### Kenapa tombol Check-in tidak ada di baris tamu?

Tombol **Check-in** tidak tampil kalau tanggal kedatangan tamu bukan tanggal bisnis hari ini (**Bukan hari ini**), atau kalau peran Anda tidak boleh check-in. Rombongan di-check-in dari halaman grupnya.
→ [Check-in](01-front-desk/check-in.md)

### Kamarnya masih Kotor. Bisakah tamu tetap check-in?

Check-in butuh kamar **Bersih** atau **Sudah diperiksa**. Pilih kamar lain, atau minta housekeeping membersihkannya. Pengguna yang berwenang bisa mencentang **Tetap check-in** dengan alasan.
→ [Check-in](01-front-desk/check-in.md)

### Saya check-in tamu yang salah. Bagaimana membatalkannya?

Klik **Batalkan check-in** pada hari yang sama, sebelum ada biaya di folio dan sebelum tamu pindah kamar. Setelah audit malam, check-in tidak bisa dibatalkan lagi: gunakan **Pindah kamar** kalau kamarnya yang salah, **Ubah tamu** kalau datanya yang salah, atau minta supervisor mengoreksi folionya lalu check-out kalau tamunya memang tidak menginap.
→ [Membatalkan check-in](01-front-desk/membatalkan-check-in.md)

### Kenapa tamu tidak bisa check-out?

Semua folio tamu harus bersaldo nol, termasuk folio yang ditagihkan ke perusahaan. Di samping tombol **Check-out** tertulis alasannya, misalnya "Bayar dulu saldo 828.850.". Kalau tamu kelebihan bayar, muncul "Kembalikan … ke tamu dulu.": klik **Buka folio**, lakukan refund, lalu kembali ke check-out.
→ [Check-out](01-front-desk/check-out.md)

### Tamu pulang lebih awal. Berapa yang harus dibayar?

Centang **Konfirmasi keberangkatan lebih awal** di kotak **Check-out**, lalu terima jumlah yang diminta di sana. Tamu yang sudah menginap sejak sebelum hari ini dan pulang pada siang hari tidak ditagih untuk malam ini, jadi yang dibayar cukup saldo folionya. Tamu yang tiba hari ini tetap membayar malam ini.
→ [Check-out](01-front-desk/check-out.md)

### Tagihan tamu dibayar perusahaan. Apakah check-out memindahkannya ke perusahaan?

Tidak. Pindahkan saldonya dulu di tab **Transfer** folio, lalu check-out. Tab ini hanya muncul kalau peran Anda boleh memindahkan ke piutang perusahaan.
→ [Transfer ke piutang perusahaan](06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md)

### Tamu pindah ke kamar yang lebih mahal. Apakah harganya ikut berubah?

Tidak, harga tidak berubah otomatis. Kalau harganya perlu disesuaikan, ubah tarifnya lewat **Ubah tarif**. Menurunkan tarif butuh persetujuan.
→ [Pindah kamar](01-front-desk/pindah-kamar.md)

## Kamar dan housekeeping

### Kenapa kamar Kotor tidak bisa langsung saya tandai Sudah diperiksa?

Status kebersihan punya urutan. Kamar **Kotor** harus ditandai **Bersih** dulu, baru **Sudah diperiksa**. Kalau muncul "Peran Anda tidak mengizinkan tindakan ini." saat menandai **Sudah diperiksa**, minta supervisor memeriksanya.
→ [Mengubah status kebersihan kamar](02-housekeeping/mengubah-status-kebersihan.md)

### Kenapa pagi ini semua kamar terisi menjadi Kotor?

Setelah audit malam, semua kamar yang terisi menjadi **Kotor** supaya kamar tamu yang menginap lanjut ikut dibersihkan, kalau pengaturan properti itu menyala (bawaan: menyala). Kamar juga menjadi **Kotor** setelah check-out, pindah kamar, dan pembatalan check-in.
→ [Melihat status kamar](02-housekeeping/status-kamar.md)

### Kamar rusak sementara. Sebaiknya kamar itu dinonaktifkan?

Jangan. Blokir kamarnya sebagai OOO atau OOS. Tanggal blokir tidak bisa diubah: untuk memperpanjang atau memperpendek, lepaskan blokirnya lalu buat blokir baru.
→ [Memblokir kamar (OOO dan OOS)](02-housekeeping/blokir-kamar-ooo-oos.md)

### Saya tidak melihat tugas apa pun di Tugas saya. Kenapa?

"Tidak ada tugas untuk Anda hari ini." berarti supervisor belum memberi Anda kamar. Klik **Lihat semua kamar kotor** untuk melihat kamar yang kotor.
→ [Daftar pembersihan dan Tugas saya](02-housekeeping/daftar-pembersihan-dan-tugas-saya.md)

## Folio, pembayaran, dan koreksi

### Kenapa biaya yang salah tidak bisa dihapus dari folio?

Folio hanya bisa ditambah, tidak bisa dihapus. Biaya yang salah hari ini dibalik dengan **Reverse** oleh supervisor. Kalau salahnya ditemukan setelah audit malam, koreksinya lewat **Penyesuaian**. Keduanya butuh persetujuan.
→ [Mengoreksi biaya: reverse dan penyesuaian](03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md)

### Kenapa tombol Reverse tidak ada pada biaya kemarin?

**Reverse** hanya ada untuk biaya dan penyesuaian yang diposting pada tanggal bisnis hari ini. Untuk biaya kemarin, pakai **Penyesuaian**.
→ [Mengoreksi biaya: reverse dan penyesuaian](03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md)

### Saat membalik biaya kemarin, muncul pesan "Hanya tanda terima pada tanggal bisnis berjalan yang bisa dibatalkan." Kenapa menyebut tanda terima?

Pesan ini muncul kalau layar belum dimuat ulang setelah audit malam (kendala yang diketahui). Yang ditolak sebenarnya biaya atau pembayaran dari hari sebelumnya. Muat ulang halaman folio, lalu koreksi biayanya dengan **Penyesuaian**. Untuk pembayaran kemarin, pakai refund (lihat [Void dan refund](03-kasir-dan-pembayaran/void-dan-refund.md)).
→ [Mengoreksi biaya: reverse dan penyesuaian](03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md)

### Saya salah memilih metode atau jumlah pembayaran. Bisakah diubah?

Pembayaran tidak bisa diubah. Batalkan dengan **Void** pada hari yang sama, minta persetujuan, lalu posting ulang yang benar. Untuk pembayaran kemarin, tombol **Void** tidak ada: pakai **Refund**. Void dan refund tidak bisa dibatalkan.
→ [Membatalkan (void) dan mengembalikan (refund) pembayaran](03-kasir-dan-pembayaran/void-dan-refund.md)

### Koneksi terputus saat saya memposting pembayaran. Apakah pembayarannya tercatat dua kali kalau saya tekan lagi?

Tidak. Klik **Posting pembayaran** sekali lagi tanpa mengubah isian. Pembayaran tidak akan tercatat dua kali.
→ [Menerima pembayaran](03-kasir-dan-pembayaran/menerima-pembayaran.md)

### Biaya ini sebenarnya milik tamu di kamar lain. Bagaimana memindahkannya?

Centang biayanya, klik **Transfer…**, lalu lanjut ke persetujuan. Hanya biaya yang bisa dipindahkan: pembayaran yang salah folio di-void lalu diposting ulang di folio yang benar.
→ [Memindahkan transaksi antar folio](03-kasir-dan-pembayaran/transfer-antar-folio.md)

### Di halaman Kasir ada metode bertulisan `cashier.COMP`. Apa itu?

Itu bill POS yang ditutup sebagai komplimen; namanya belum tampil dengan benar (kendala yang diketahui). Tidak ada uang yang diterima untuk baris itu, jadi jangan menghitungnya sebagai kas atau kartu.
→ [Daftar pembayaran di Kasir](03-kasir-dan-pembayaran/daftar-kasir.md)

## Shift kasir

### Kenapa saya tidak bisa menerima uang tunai?

Uang tunai butuh shift kasir yang terbuka. Kalau muncul "Uang tunai butuh shift kasir yang terbuka.", isi **Laci** dan **Modal awal** di kotak itu, klik **Buka shift**, lalu lanjutkan. Kalau peran Anda tidak boleh membuka shift, minta kasir yang menerima uangnya.
→ [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md)

### Muncul "Ada shift lain yang terbuka di laci ini." Apa artinya?

Laci itu sedang dipegang rekan Anda: satu laci hanya untuk satu shift. Pakai kode laci lain. Kalau pesannya "Anda masih punya shift terbuka: tutup dulu.", shift Anda yang lama belum ditutup.
→ [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md)

### Apakah pembayaran kartu dan QRIS ikut dihitung di laci saya?

Tidak. Pembayaran kartu, QRIS, dan transfer bank tidak masuk hitungan laci. Hanya uang tunai yang dicocokkan saat shift ditutup.
→ [Shift kasir](03-kasir-dan-pembayaran/shift-kasir.md)

### Rekan kasir pulang tanpa menutup shift-nya. Bisakah supervisor menutupnya?

Bisa, kalau perannya memegang **Melihat semua shift kasir di properti dan menutup shift kasir lain**. Di halaman **Tagihan → Shift kasir**, shift itu ada di daftar **Shift** dengan tombol **Tutup shift…**. Supervisor menghitung uang di laci itu dan mengisi **Kas yang dihitung**. Selisih di atas batas butuh alasan dan persetujuan, seperti shift lain. Laporan Z mencatat supervisor sebagai penutupnya.
→ [Menutup shift kasir lain](03-kasir-dan-pembayaran/shift-kasir.md#menutup-shift-kasir-lain)

## POS

### Kenapa bill tidak bisa dibebankan ke kamar tamu?

Biasanya folio tamu terkunci untuk beban outlet ("Folio ini terkunci untuk beban outlet: minta resepsionis membukanya."), outlet Anda tidak ada di daftar folio itu, atau jumlahnya melebihi sisa batas. Minta resepsionis membuka folionya kalau tamu memang boleh. Kalau batasnya kurang, klik **Bebankan sebesar yang bisa diterima kamar, sisanya dengan cara lain**.
→ [Membebankan bill ke kamar](04-pos/bebankan-ke-kamar.md)

### Bill restoran terlanjur dibebankan ke kamar yang salah. Bisakah dibatalkan?

Bisa. Selama folio tamu masih terbuka, buka kembali bill-nya, lalu klik **Kembalikan beban kamar** di baris "Dibebankan ke kamar". Keduanya butuh alasan dan persetujuan. Beban dibatalkan di folio tamu, batas outlet pulih, lalu bill dibayar dengan cara lain atau dibebankan ke kamar yang benar. Di folio tamu sendiri, beban POS tidak bisa di-reverse atau dikoreksi dengan **Penyesuaian**.
→ [Mengembalikan beban kamar dari bill POS](04-pos/mengembalikan-beban-kamar.md)

### Beban restoran ke kamar yang salah baru ketahuan setelah tamunya check-out. Bisakah dikembalikan?

Bisa. Di bill outlet, baris beban bertanda **Sudah check-out** punya tombol **Kembalikan setelah check-out**. Dengan satu alasan dan satu persetujuan, beban kembali ke bill dan tamu menerima refund dari pembayarannya. Kalau tagihan tamu dipindah ke master folio grup yang masih terbuka, jumlahnya menjadi kredit di master itu. Folio tamu tetap ditutup dengan saldo nol, lalu bill dibayar dengan cara lain. Kalau tagihan tamu dipindah ke perusahaan, jalur ini menolak. Pakai cara manual dengan nota kredit.
→ [Mengembalikan beban kamar setelah tamu check-out](04-pos/mengembalikan-beban-kamar.md#mengembalikan-beban-kamar-setelah-tamu-check-out)

### Saya sudah memakai Buka kembali pada bill, tetapi bebannya masih ada di tagihan tamu. Kenapa?

**Buka kembali** hanya membuka bill. Beban kamarnya ditarik dengan **Kembalikan beban kamar** di bill yang sudah terbuka itu.
→ [Mengembalikan beban kamar dari bill POS](04-pos/mengembalikan-beban-kamar.md)

### Item sudah dikirim ke dapur, lalu tamu membatalkannya. Bisakah item itu dihapus?

Tidak. Item yang sudah dikirim tidak bisa diubah atau dihapus ("Item yang sudah dikirim tidak bisa diubah: void saja."). Batalkan dengan **Void**, yang butuh persetujuan. Item yang belum dikirim bebas diubah dan dihapus.
→ [Mengambil pesanan](04-pos/mengambil-pesanan.md)

### Kenapa muncul "Buka shift kasir sebelum menerima uang tunai." padahal saya sudah membuka shift?

Shift harus terbuka pada laci outlet ini. Shift di laci lain tidak dihitung. Buka shift di **Tagihan → Shift kasir** dengan kode laci yang sama dengan laci outlet.
→ [Membayar bill](04-pos/membayar-bill.md)

## Banquet

### Tamu yang hadir lebih banyak dari garansi. Berapa yang ditagih?

Ketik jumlah yang hadir di **Pax aktual** pada jendela **Posting sesi**. Yang ditagih adalah yang lebih besar dari pax garansi dan pax aktual, dan **Ditagih** menunjukkannya sebelum Anda memposting. Garansi 40 dengan 42 yang hadir ditagih 42; garansi 40 dengan 35 yang hadir tetap ditagih 40.
→ [Posting sesi dan penagihan acara](05-banquet/posting-sesi-dan-penagihan.md)

### Sesi sudah diposting, lalu jumlah tamunya berubah. Bisakah sesi diposting ulang?

Tidak. Satu sesi hanya diposting sekali, dan sesi yang sudah diposting tidak bisa diubah atau dibatalkan. Koreksinya lewat penyesuaian folio dengan persetujuan.
→ [Posting sesi dan penagihan acara](05-banquet/posting-sesi-dan-penagihan.md)

### Acara sudah Definit. Bisakah dikembalikan ke Tentatif?

Tidak. Setelah Definit, acara hanya bisa diselesaikan atau dibatalkan, dan pembatalan bisa dikenai biaya sesuai kebijakan. Acara yang dibatalkan tidak bisa dibuka atau diubah lagi.
→ [Tentatif dan konfirmasi acara](05-banquet/tentatif-dan-konfirmasi.md)

## Perusahaan dan piutang

### Perusahaan sudah membayar, tetapi invoicenya masih Belum dibayar. Kenapa?

Penerimaannya dicatat tanpa dibagi ke invoice itu: **Dibayar sekarang** pada barisnya kosong, sehingga jumlahnya masuk **Belum dialokasikan** sebagai uang muka. Saldo piutang turun, tetapi invoice tetap **Belum dibayar** dan muncul di **Invoice jatuh tempo**. Klik **Alokasikan** pada baris penerimaan itu di **Rekening koran** akun perusahaan, lalu bagi sisanya ke invoice yang dibayar. Ini bisa dilakukan kapan saja, juga setelah audit malam. Kalau penerimaan terlanjur dialokasikan ke invoice yang salah, klik **Lepas** pada alokasinya (dengan alasan dan persetujuan), lalu alokasikan lagi.
→ [Invoice dan penerimaan piutang perusahaan](06-corporate-group-city-ledger/invoice-dan-penerimaan.md)

### Muncul "Pemindahan ini melampaui batas kredit perusahaan." Apa yang bisa dilakukan?

Saldo yang dipindahkan melebihi batas kredit perusahaan. Minta manajer menaikkan batas kreditnya, atau tagih sebagian ke tamu.
→ [Transfer ke piutang perusahaan](06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md)

### Kenapa tamu yang masih menginap belum bisa dimasukkan ke invoice?

Hanya tamu yang sudah check-out yang bisa diinvoice. Pemindahan tamu yang masih menginap bertanda "menginap".
→ [Invoice dan penerimaan piutang perusahaan](06-corporate-group-city-ledger/invoice-dan-penerimaan.md)

## Akuntansi dan keuangan

### Jurnal manual saya salah. Bisakah diubah atau dihapus?

Tidak. Jurnal yang sudah diposting tidak bisa diubah atau dihapus. Koreksinya dengan pembalikan, lalu jurnal baru yang benar. Satu jurnal hanya bisa dibalik sekali.
→ [Jurnal manual dan pembalikan](07-accounting/jurnal-manual-dan-pembalikan.md)

### Settlement kartu yang saya catat ternyata salah. Bisakah dibatalkan?

Belum bisa dari layar (kendala yang diketahui). Kalau pasangan barisnya dilepas di halaman rekonsiliasi, jurnalnya tetap ada. Laporkan ke manajer keuangan, yang bisa membalik jurnalnya di [Jurnal manual dan pembalikan](07-accounting/jurnal-manual-dan-pembalikan.md). Periksa centang pembayaran dan komisinya sebelum menyimpan.
→ [Settlement kartu, e-wallet, dan QRIS](08-keuangan-back-office/settlement-kartu.md)

### Kenapa pembayaran kartu hari ini belum muncul di settlement?

Pembayaran baru muncul setelah audit malam menutup hari bisnisnya. Pembayaran yang di-void tidak pernah muncul.
→ [Settlement kartu, e-wallet, dan QRIS](08-keuangan-back-office/settlement-kartu.md)

### Di umur utang usaha, total Belum jatuh tempo kok ada di kolom 31–60 hari?

Di baris **Total**, angka-angkanya bergeser satu kolom (kendala yang diketahui). Baca total per kolom dari baris pemasok, atau dari kolom **Total** paling kanan.
→ [Utang usaha: pemasok, tagihan, dan pembayaran](08-keuangan-back-office/utang-usaha.md)

### Kenapa tombol Tutup bulan tidak muncul?

Ada syarat yang belum terpenuhi, dan layar tidak menjelaskan yang mana. Periksa: bulan sudah berakhir, bulan sebelumnya sudah ditutup, **Hari yang dijurnalkan** lengkap, dan penyusutan aset sudah dijalankan.
→ [Periode dan tahun buku](07-accounting/periode-dan-tahun-buku.md)

## Persediaan

### Pemakaian atau waste yang sudah diposting ternyata salah. Bisakah diedit?

Tidak. Dokumen yang sudah diposting tidak bisa diedit. Pembalikan hanya bisa pada hari diposting. Setelah hari itu, koreksi dengan dokumen baru ke arah sebaliknya, atau dengan opname.
→ [Pemakaian, waste, makan karyawan, dan entertainment](09-inventory/pemakaian-dan-waste.md)

### Kenapa transfer atau penerimaan ke gudang ditolak dengan "Opname sedang berjalan di gudang."?

Gudang itu sedang dibekukan oleh opname. Tunggu opname disetujui atau dibatalkan. Opname yang masih terbuka juga menahan audit malam, jadi selesaikan pada hari yang sama.
→ [Opname stok gudang](09-inventory/opname.md)

### Saya mencentang "Simpan dan ajukan" di pesanan pembelian, kenapa pesanan langsung berstatus Terkirim ke pemasok?

Label kotak itu menyesatkan: artinya "kirim pesanan ke pemasok sekarang", bukan mengajukan untuk persetujuan (kendala yang diketahui). Centang kotak itu hanya kalau pesanan memang dikirim ke pemasok sekarang. Tanpa centang, pesanan tersimpan sebagai **Draf**.
→ [Permintaan pembelian dan pesanan pembelian](09-inventory/permintaan-dan-pesanan-pembelian.md)

### Saya hanya penyetuju. Apakah peran saya butuh izin melihat barang dan stok?

Tidak. Izin persetujuan (kepala departemen, kontrol biaya, atau persetujuan akhir) sudah cukup untuk membuka **Persediaan → Pembelian** dan permintaan yang menunggu Anda, lalu memutuskannya. Izin **Melihat barang, gudang, stok, kartu stok, dan saldo awal** hanya perlu kalau Anda juga bekerja dengan barang dan stoknya.
→ [Rantai persetujuan: mengatur dan memutuskan](09-inventory/rantai-persetujuan.md)

### Rantai persetujuan terkunci dan tidak ada tombol Simpan rantai. Kenapa?

Rantai persetujuan hanya diubah oleh peran yang boleh **Mengubah pengaturan properti**. Peran yang hanya boleh mengelola persediaan bisa membacanya, tetapi tidak mengubahnya. Minta administrator, atau manajer yang perannya boleh mengubah pengaturan properti, untuk mengubah rantainya.
→ [Rantai persetujuan: mengatur dan memutuskan](09-inventory/rantai-persetujuan.md)

### Bulatan Bawaan beli dan Bawaan resep tidak terisi setelah saya klik. Apakah pilihannya tersimpan?

Pilihannya tetap tercatat saat disimpan, hanya bulatannya yang tidak tampil terisi di formulir (kendala yang diketahui). Setelah menyimpan, buka barangnya lagi dan periksa bulatannya: di formulir yang baru dibuka, bulatan menunjukkan pilihan yang tersimpan.
→ [Barang, gudang, dan saldo awal](09-inventory/barang-gudang-dan-saldo-awal.md)

### Akun persediaan sebuah kategori salah, tetapi tidak bisa diubah. Apa yang harus dilakukan?

Selama belum ada barang kategori itu yang punya mutasi stok, akunnya bisa diubah langsung di formulir kategori. Setelah mutasi stok pertama, akunnya terkunci. Buat kategori baru dengan akun yang benar; barang yang belum punya stok bisa dipindah ke kategori itu lewat **Kategori** di formulir barang. Ke depan, periksa akunnya sebelum mencatat saldo awal atau penerimaan pertama.
→ [Menyiapkan persediaan: metode harga pokok, satuan, dan kategori](09-inventory/menyiapkan-persediaan.md)

### Saldo awal barang salah ketik. Bisakah dibatalkan?

Tidak. Saldo awal tidak bisa dibatalkan, dan setiap barang hanya punya satu saldo awal per gudang. Kesalahan jumlah dikoreksi lewat opname.
→ [Barang, gudang, dan saldo awal](09-inventory/barang-gudang-dan-saldo-awal.md)

## Aset tetap

### Kenapa Mulai disusutkan di jendela Jadikan aset selalu terpilih pada bulan mulai pakai?

Pilihan itu selalu terpilih pada **Bulan mulai pakai**, walaupun pengaturan properti memakai bulan berikutnya (kendala yang diketahui), sehingga aset bisa mulai disusutkan sebulan lebih awal. Periksa dan pilih sendiri bulan mulai disusutkan setiap kali. Selama aset belum disusutkan, pilihan ini masih bisa diubah lewat **Ubah data**.
→ [Daftar tunggu dan register aset](10-aset-tetap/daftar-tunggu-dan-register.md)

### Tab Riwayat register menampilkan "[object Object] → [object Object]". Di mana saya bisa melihat perubahannya?

Riwayat perubahan belum bisa dibaca dari tab itu (kendala yang diketahui). Lihat **Buku besar aset** di tab yang sama untuk perubahan harga dan akumulasi, atau buka jejak audit.
→ [Daftar tunggu dan register aset](10-aset-tetap/daftar-tunggu-dan-register.md)

### Saya administrator dan mengajukan pelepasan aset. Kenapa langsung diposting?

Administrator hotel boleh menyetujui sendiri, jadi pelepasannya langsung diposting walaupun tombolnya bertuliskan **Ajukan pelepasan** (kendala yang diketahui). Kalau hotel ingin pelepasan diperiksa orang kedua, minta staf aset yang mengajukannya, lalu administrator memutuskannya di tab **Menunggu persetujuan**.
→ [Pelepasan aset: dijual atau dihapus](10-aset-tetap/pelepasan-aset.md)

### Berkas saldo awal aset dari template ditolak dengan "Kategori tidak ada di properti ini.". Kenapa?

Baris contoh di **Unduh template CSV** memakai kategori `FF&E`, yang ditolak (kendala yang diketahui). Tulis kode kategorinya, misalnya `FFE`. Daftar kode ada di pilihan **Kategori** pada formulir satu aset.
→ [Saldo awal aset](10-aset-tetap/saldo-awal-aset.md)

## Audit malam

### Kenapa audit malam belum bisa dijalankan?

Masih ada penghalang, yang tercantum di bawah "Hari belum bisa ditutup karena:". Yang paling sering: kedatangan yang belum check-in atau ditandai no-show, keberangkatan yang belum check-out, biaya kamar yang gagal, dan shift kasir yang masih terbuka. Selesaikan setiap butirnya, lalu klik **Muat ulang**.
→ [Menangani penghalang audit malam](11-audit-malam/menangani-penghalang.md)

### Saya klik baris di daftar penghalang, tetapi tidak terjadi apa-apa. Kenapa?

Setiap baris tampil sebagai tautan ke langkahnya, tetapi mengkliknya tidak menggulir halaman (kendala yang diketahui). Gulir sendiri ke langkah yang disebut. Halaman hanya bergulir otomatis saat dibuka dari tautan di luar halaman ini, misalnya dari **Hari ini**.
→ [Menangani penghalang audit malam](11-audit-malam/menangani-penghalang.md)

### Muncul pita "Audit malam terlambat". Apa yang harus dilakukan?

Tanggal kalender di hotel sudah lewat lebih dari satu hari dari tanggal bisnis. Transaksi tetap dicatat pada tanggal bisnis lama sampai audit malam menutupnya. Jalankan audit malam secepatnya: satu kali jalan menutup satu hari, jadi kalau tertinggal dua hari, jalankan dua kali.
→ [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md)

### Kenapa PDF paket audit malam berbahasa Inggris?

Ini disengaja, karena paket bisa dikirim ke pemilik atau kantor pusat. Paket audit malam selalu berbahasa Inggris, apa pun bahasa layar Anda.
→ [Menjalankan audit malam](11-audit-malam/menjalankan-audit-malam.md)

### Paket audit malam tidak terkirim lewat e-mail. Apa yang harus dilakukan?

Kalau tertulis "E-mail mati di server ini…", paket tetap dibuat dan bisa diunduh dari arsip, tetapi tidak dikirim. Hubungi administrator hotel; administrator hotel akan meneruskannya ke tim dukungan KamaraPMS (lihat [Bantuan dan kontak](README.md#bantuan-dan-kontak)).
→ [Paket audit malam: arsip dan penerima](11-audit-malam/paket-audit-malam.md)

## Laporan

### Kenapa okupansi hari ini belum muncul di laporan?

Okupansi dan statistik hanya menghitung hari yang sudah ditutup oleh audit malam. Hari ini baru muncul setelah audit malam malam ini.
→ [Laporan front office](12-laporan/laporan-front-office.md)

### Kenapa Guest ledger menolak tanggal hari ini?

**Guest ledger** dan **Ledger deposit** hanya untuk hari bisnis yang sudah ditutup: "Laporan ini untuk hari bisnis yang sudah ditutup: pilih hari yang ditutup oleh audit malam.". Daily flash report untuk hari ini masih berupa angka sementara.
→ [Laporan harian](12-laporan/laporan-harian.md)

### Kenapa ADR dan RevPAR di dasbor masih "–"?

Angka itu baru terisi setelah biaya kamar malam ini diposting. Sebelumnya kartunya menampilkan "–" dengan keterangan "Tersedia setelah audit malam".
→ [Kinerja dan dasbor manajer](12-laporan/kinerja-dan-dasbor.md)

## Pengaturan dan hak akses

### Di daftar Pengguna, seorang staf tertulis "Tanpa akses properti". Apa artinya?

Staf itu belum punya peran di properti mana pun, sehingga setelah masuk ia tidak bisa membuka hotel apa pun. Klik nama penggunanya, lalu tambahkan properti dan perannya di **Akses properti**.
→ [Pengguna](13-pengaturan/pengguna.md)

### Akses staf sudah saya isi, tetapi ia tetap tidak bisa masuk ke properti itu. Kenapa?

Periksa kolom **Akses** di daftar **Pengguna**: properti itu harus tertulis dengan perannya. Kalau tidak ada, buka pengguna itu dan tambahkan baris properti dan perannya. Baris yang belum lengkap tidak bisa disimpan: layar menunjukkan baris mana yang belum dipilih propertinya atau perannya. Kalau aksesnya sudah benar, periksa apakah perannya punya izin untuk menu yang ia butuhkan. Lihat [Peran](13-pengaturan/peran.md).
→ [Pengguna](13-pengaturan/pengguna.md)

### Email pengguna salah ketik. Bisakah diubah?

Tidak. Pengguna tidak bisa dihapus, dan emailnya tidak bisa diubah. Nonaktifkan pengguna itu, lalu buat yang baru dengan email yang benar.
→ [Pengguna](13-pengaturan/pengguna.md)

### Bagaimana mengubah akun kode biaya pembulatan POS?

Buka **Pengaturan → Kode biaya**, klik **Ubah** pada kode `POS_ROUNDING` (jenis **Pembulatan POS**), ganti **Akun pendapatan** atau **Departemen bawaan**, lalu simpan. **Jenis** kode ini terkunci.
→ [Pajak dan kode biaya](13-pengaturan/pajak-dan-kode-biaya.md)

### Muncul "Langganan sudah berakhir: data bisa dibaca tetapi tidak bisa diubah." Apa artinya?

Masa tenggang langganan sudah habis, jadi hotel dalam keadaan **Hanya baca**: check-in, pembayaran, dan audit malam ditolak. Hotel tidak bisa memperpanjang paket sendiri. Beri tahu administrator hotel; administrator hotel akan meneruskannya ke tim dukungan KamaraPMS (lihat [Bantuan dan kontak](README.md#bantuan-dan-kontak)).
→ [Langganan dan sistem](13-pengaturan/langganan-dan-sistem.md)
