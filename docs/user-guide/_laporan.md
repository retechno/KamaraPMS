# Laporan penulisan Panduan Pengguna

Laporan untuk pemilik dan tim pengembang, bukan untuk staf hotel. Laporan ini diisi bertahap di setiap batch. Fase 6 menambahkan statistik akhir, hasil pemeriksaan, dan menggabungkan temuan audit Fase 1 (`_inventory.md` bagian 9).

## 1. Statistik per batch

| Batch | Isi | Halaman | Screenshot | Ukuran folder screenshot |
|---|---|---|---|---|
| 1 | 00-memulai, 01-front-desk, panduan-cepat/resepsionis | 23 | 46 | 1,6 MB (rata-rata 35,5 KB, terbesar 137 KB) |
| 2 | 02-housekeeping, 03-kasir-dan-pembayaran, 04-pos | 22 | 44 (1,7 MB) | 3,3 MB untuk 90 gambar (rata-rata 37,1 KB, terbesar 180 KB) |

## 2. Tanda PERLU VERIFIKASI

Tanda yang masih terbuka di halaman panduan:

- (tidak ada)

Tanda yang sudah diselesaikan:

- `01-front-desk/membatalkan-check-in.md`: batal check-in setelah audit malam. Dibaca dari `internal/frontdesk/service.go` (`ReverseCheckIn`), lalu dicocokkan dengan test `TestReverseNeedsTheSameBusinessDay` dan bagian akhir test batal check-in di `internal/frontdesk/frontdesk_test.go`. Setelah tanggal bisnis berganti, pembatalan ditolak dengan `CHECK_IN_NOT_REVERSIBLE` ("Check-in hanya bisa dibatalkan pada hari yang sama dan sebelum pindah kamar."). Biaya apa pun di folio, termasuk yang sudah dibalik, memberi `CHECK_IN_HAS_CHARGES`. Jalan keluarnya sudah ditulis di halaman itu.
- `01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md`: mengisi pemesan pada draf tanpa pemesan. Dari UI tidak bisa (lihat 5.1). Jalan keluarnya, yaitu membuat ulang lalu membatalkan draf, ditulis sebagai langkah 7 dan 8.

## 3. Teks bahasa Inggris, kunci i18n mentah, dan kode mentah di layar

Halaman panduan menuliskan teks ini persis seperti tampilannya, lalu memberi artinya dalam kurung.

| Tempat | Yang tampil | Catatan |
|---|---|---|
| Pengaturan → Kode biaya, kolom Jenis, kode POS_ROUNDING | `chargeCodes.type_ROUNDING` | Kunci i18n tidak ada, jadi kuncinya tampil mentah |
| Reservasi baru, kartu Rincian, ringkasan reservasi | `PHONE`, `EMAIL`, `WALK_IN`, `WEBSITE`, `OTA`, `AGENT`, `OTHER` | Label Indonesia sudah ada (`reports.source.*`), tetapi tidak dipakai di sini |
| Tamu → Tamu baru, peringatan duplikat | `same email`, `same phone`, `same id document`, `same name and birth date` | Tidak ada teks Indonesia |
| Profil tamu, isian identitas | `id_type and id_number go together` (INCOMPLETE) | Tidak ada `fieldErrors.INCOMPLETE` |
| Akun saya → Ubah kata sandi | `at least 12 characters` (WEAK_PASSWORD) | Tidak ada `fieldErrors.WEAK_PASSWORD` |
| Check-in, isian kamar | pesan ROOM_BED_MISMATCH dari server | Tidak ada `fieldErrors.ROOM_BED_MISMATCH` |
| Profil tamu → Riwayat | Peran `booker`, `occupant`, `primary`, `accompanying`; Status `CONFIRMED`, `OPEN`, … | Kode mentah |
| Profil tamu, catatan baca-saja | menyebut kode izin `guest.write` | Sebaiknya nama izinnya |
| Reservasi → kartu Pembayaran dan folio | status folio `OPEN` | Kode mentah |
| Kotak Check-out, langkah selesai | `DIRTY`, `CLOSED` | Kode mentah |
| Tooltip tanggal bisnis di bilah atas | `Server time` | Teks Inggris tertanam di `TopBar.vue` |
| Pengaturan → Peran | grup `Inventory`, `Banquet`, `Fixed assets` | Tidak ada `permissionGroup.*` Indonesia |
| Ketersediaan | `Tampilkan bed type` | Campuran Inggris |
| Bagan kamar | `OOO`/`OOS`, `(upgrade)`, tombol `Minggu` | "Minggu" bisa dibaca sebagai hari Minggu |
| Daftar pembersihan, tabel beban kerja | `Not assigned` | Nama dari server (`housekeeping/tasks.go:228`). Tugas saya sudah memakai "Belum ditugaskan" |
| Panel kamar, bagian Hari ini | `Cleaning started (task)`, `Cleaning finished (task)` | Catatan bahasa Inggris dari server (`tasks.go:507`, `:551`) |
| Perawatan dan Barang tertinggal, tanpa akses | izin `maintenance.report` / `lostfound.report` di dalam kalimat | Sebaiknya nama izinnya |
| Blokir kamar, baris konflik | "Baris reservasi {id}" | Nomor internal baris, bukan nomor reservasi |
| Blokir kamar dan Perawatan | "Rusak (out of order)" / "Tidak dipakai (out of service)" | Di tempat lain "Rusak (OOO)" / "Tidak dilayani (OOS)": istilahnya tidak seragam |
| Tugas saya, label Dimulai | jam dengan format browser (`toLocaleTimeString`) | Bisa tampil "02:30 PM" di browser berbahasa Inggris |
| Folio, rincian baris | `exclusive`/`inclusive`; tarif `10.0000%` | Mode harga dan tarif mentah |
| Folio, baris void | `Void: Payment PAY000012 (CASH)` | Tidak diterjemahkan oleh `utils/folioDescription.ts` |
| Folio, transfer ditolak | izin `cityledger.read atau reservation.read` | Kode izin mentah |
| Biaya kamar, kolom alasan | `MISSING_NIGHTLY_RATE`, `NO_OPEN_FOLIO`, `STAY_CLOSED`, … | Kode mentah |
| Shift kasir | kode laci `MAIN` di daftar shift dan placeholder; izin `cashier.shift` di teks tanpa akses | Judul shift aktif sudah menerjemahkan ("Laci utama") |
| Folio dan shift, pesan isian | `only on the master folio of a group`, `at most 200 charges in a request`, `no such outlet in this property` | Tidak ada `fieldErrors.*` |
| POS, struk komplimen di browser | `posPay.method_COMP` | Kunci i18n tidak ada |
| Laporan outlet, daftar Diskon | `Discount: <item>` | Deskripsi bahasa Inggris dari server (`folios/outletbill.go:218`) |
| Laporan outlet, CSV | `business_date,bills,net,service,tax,rounding,total` | Judul kolom bahasa Inggris, angka dan tanggal mentah |
| POS, produk | perintah pendaftaran agen bahasa Inggris; label `Code page`, `X`, `Y`; pesan `HOLD_NOT_FOUND`, `TOO_DEEP`, `OPTION_GROUP_LIMIT` ("choose from 1 to 1") | Tidak ada teks Indonesia |
| POS, impor CSV | "Baris {row}: <field> · <CODE> — <detail bahasa Inggris>" | Kode mentah |
| POS, jenis pesanan DIRECT | "Kasir" di denah, "Kasir langsung" di layar dapur | Istilah tidak seragam |

## 4. Perbedaan antara desain (`docs/architecture`) dan kode

Diisi di Fase 6 dari `_inventory.md` bagian 9.3, ditambah temuan batch berikutnya.

## 5. Fitur setengah jadi dan celah UX

### 5.1 Fitur setengah jadi atau tidak tersambung ke UI

- **Pemesan pada reservasi yang sudah tersimpan.** API `PATCH /reservations/{id}` menerima `guest_id` (`PatchReservationRequest`), tetapi tidak ada layar yang memakainya. Jendela **Ubah reservasi** hanya mengubah perusahaan, permintaan khusus, dan catatan, sedangkan formulir Reservasi baru tidak bisa membuat tamu. Akibatnya, draf yang tersimpan tanpa pemesan tidak pernah bisa dikonfirmasi, dan harus dibuat ulang. Pesan `BOOKER_REQUIRED` ("Pemesan wajib diisi: tamu atau perusahaan.") juga menyesatkan, karena konfirmasi hanya memeriksa tamu (`lifecycle.go:140`). Perusahaan tidak dihitung sebagai pemesan.
- **Profil tamu ganda tidak bisa digabung**, baik di UI maupun di API.
- **Tidak ada tombol menambah kamar** ke reservasi yang sudah ada. Reservasi baru dari layar Reservasi selalu satu kamar.
- **Walk-in** tidak punya langkah deposit dan isian identitas (berbeda dengan check-in biasa).
- **Produk habis (sold out)** hanya bisa ditandai lewat API (`PUT /pos/outlets/{id}/sold-out/{productId}`). Tidak ada tombol di layar POS. Panduan menyarankan menghapus centang **Dijual** untuk sementara.
- **Mengembalikan atau memindahkan beban POS ke kamar** (`POST /pos/room-charges/{id}/return|move`) tidak dipanggil layar mana pun. Baris transfernya di folio tamu juga tidak bisa di-reverse. Panduan menyarankan penyesuaian (kredit) di folio.
- **Laporan outlet per kasir dan per pelayan:** API mengirimnya (`floor_state.go:374`), tetapi halaman tidak menampilkannya.
- **Izin `folio.reopen` dan `folio.post_after_checkout`** ada di katalog izin, tetapi tidak ada layar atau layanan yang memakainya.
- **Menutup shift kasir lain** (`cashier.shift_manage`) diizinkan API, tetapi halaman Shift kasir tidak punya tombolnya.

### 5.2 Celah UX: angka, tanggal, dan teks yang tidak terformat

- **Toast perpanjang** menampilkan tanggal mentah: "Keberangkatan sekarang 2026-11-26.", bukan format tanggal lokal (misalnya 26 Nov 2026).
- **Pesan deposit** menampilkan saldo tanpa pemisah ribuan: "Deposit PAY000001 diterima; saldo folio -1000000.". Pesan "Biaya {amount} diposting ke folio {folio}." kemungkinan sama (belum dilihat di layar).
- **Kotak persetujuan Ubah tarif** selalu berbunyi "Malam yang sudah ditagih dikoreksi dengan adjustment…", juga saat yang diturunkan hanya malam yang belum ditagih.
- **Halaman tanpa akses:** halaman Reservasi tetap menampilkan tombol **Bagan kamar** kepada pengguna yang tidak boleh melihat reservasi.
- **Folio, Penyesuaian:** petunjuk "Terposting di folio ini: {net}" dan label "Jumlah (saldo folio {balance})" menampilkan angka tanpa pemisah ribuan (misalnya `90000`).
- **Folio, Refund:** jumlah refund terisi dengan angka mentah dari server (misalnya `250000.000`).
- **Shift kasir:** **Modal awal** dan batas selisih terisi dengan angka mentah tanpa pemisah ribuan. Waktu serah terima memakai `closed_at.slice(0, 10)` dari waktu UTC, jadi tanggalnya bisa bergeser sehari di sekitar tengah malam.
- **Daftar pembersihan:** kolom **Tugas** diformat sebagai uang (angka bulat, jadi untuk saat ini tidak terlihat bedanya). Judul kolom **Kamar** muncul dua kali.
- **Shift kasir, Tutup shift:** label **Departemen** tetap tampil tanpa isian kalau daftar departemen kosong untuk pengguna itu (`DepartmentSelect` disembunyikan saat daftarnya kosong, label `FormField` tidak). Terlihat untuk peran kasir E2E.
- **Laporan outlet, daftar Komplimen:** baris tidak punya keterangan apa yang digratiskan ("<bill> · " lalu jumlahnya).

### 5.3 Celah UX: tombol yang tidak mencegah penolakan server

- **Pindah kamar:** tombol **Pindahkan** tetap aktif untuk kamar yang belum siap, walaupun pengguna tidak berhak melewatinya. Server lalu menolak dengan ROOM_NOT_READY.
- **Menetapkan kamar dan check-in:** tipe lain yang ditandai "(naik kelas)" ditawarkan juga kepada pengguna tanpa `reservation.upgrade`. Server lalu menolak dengan PERMISSION_DENIED.
- **Check-out:** folio perusahaan yang belum nol tidak menonaktifkan tombol **Check-out**. Server lalu menolak dengan FOLIO_NOT_BALANCED.
- **Bagan kamar, jendela pindah:** LINE_NOT_CONFIRMED menampilkan teks tentang check-in ("Hanya kamar yang terkonfirmasi yang bisa check-in.").
- **POS, Cetak:** tombol **Cetak** tampil untuk pelayan yang hanya punya `pos.order`, padahal server mewajibkan `pos.cashier`. Server lalu menolak dengan PERMISSION_DENIED.
- **POS, membuka bill meja:** jumlah tamu yang kosong di outlet yang mewajibkannya hanya memberi "Ada isian yang tidak valid.", tanpa pesan di bawah isian.
- **Folio, koreksi:** kesalahan isian pada reverse, penyesuaian, dan refund hanya tampil sebagai "Ada isian yang tidak valid." di jendela persetujuan, tidak di bawah isiannya.

## 6. Catatan screenshot

- **Kode properti `E2E`.** Pemilih properti menampilkan "E2E · Hotel Kamara Ubud", dan riwayat tamu menampilkan `E2E`. Kode itu dibuat oleh setup E2E bersama (`web/e2e/setup/seed.setup.ts`), yang juga dipakai test lain. Kode properti juga tidak bisa diubah setelah dibuat. Jadi kode itu dibiarkan. Hanya nama properti yang diubah oleh `web/e2e/docs/world.ts`.
- **Cara membuat ulang gambar:** `E2E_DOCS=1 E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh docs-screenshots.spec.ts`. Nama file selalu sama, jadi run baru menimpa gambar lama.

## 7. Kandidat video

Diisi di Fase 5.
