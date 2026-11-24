# Laporan penulisan Panduan Pengguna

Laporan untuk pemilik dan tim pengembang, bukan untuk staf hotel. Laporan ini diisi bertahap di setiap batch. Fase 6 menambahkan statistik akhir, hasil pemeriksaan, dan menggabungkan temuan audit Fase 1 (`_inventory.md` bagian 9).

## 1. Statistik per batch

| Batch | Isi | Halaman | Screenshot | Ukuran folder screenshot |
|---|---|---|---|---|
| 1 | 00-memulai, 01-front-desk, panduan-cepat/resepsionis | 23 | 46 | 1,6 MB (rata-rata 35,5 KB, terbesar 137 KB) |

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

## 4. Perbedaan antara desain (`docs/architecture`) dan kode

Diisi di Fase 6 dari `_inventory.md` bagian 9.3, ditambah temuan batch berikutnya.

## 5. Fitur setengah jadi dan celah UX

### 5.1 Fitur setengah jadi atau tidak tersambung ke UI

- **Pemesan pada reservasi yang sudah tersimpan.** API `PATCH /reservations/{id}` menerima `guest_id` (`PatchReservationRequest`), tetapi tidak ada layar yang memakainya. Jendela **Ubah reservasi** hanya mengubah perusahaan, permintaan khusus, dan catatan, sedangkan formulir Reservasi baru tidak bisa membuat tamu. Akibatnya, draf yang tersimpan tanpa pemesan tidak pernah bisa dikonfirmasi, dan harus dibuat ulang. Pesan `BOOKER_REQUIRED` ("Pemesan wajib diisi: tamu atau perusahaan.") juga menyesatkan, karena konfirmasi hanya memeriksa tamu (`lifecycle.go:140`). Perusahaan tidak dihitung sebagai pemesan.
- **Profil tamu ganda tidak bisa digabung**, baik di UI maupun di API.
- **Tidak ada tombol menambah kamar** ke reservasi yang sudah ada. Reservasi baru dari layar Reservasi selalu satu kamar.
- **Walk-in** tidak punya langkah deposit dan isian identitas (berbeda dengan check-in biasa).

### 5.2 Celah UX: angka, tanggal, dan teks yang tidak terformat

- **Toast perpanjang** menampilkan tanggal mentah: "Keberangkatan sekarang 2026-11-26.", bukan format tanggal lokal (misalnya 26 Nov 2026).
- **Pesan deposit** menampilkan saldo tanpa pemisah ribuan: "Deposit PAY000001 diterima; saldo folio -1000000.". Pesan "Biaya {amount} diposting ke folio {folio}." kemungkinan sama (belum dilihat di layar).
- **Kotak persetujuan Ubah tarif** selalu berbunyi "Malam yang sudah ditagih dikoreksi dengan adjustment…", juga saat yang diturunkan hanya malam yang belum ditagih.
- **Halaman tanpa akses:** halaman Reservasi tetap menampilkan tombol **Bagan kamar** kepada pengguna yang tidak boleh melihat reservasi.

### 5.3 Celah UX: tombol yang tidak mencegah penolakan server

- **Pindah kamar:** tombol **Pindahkan** tetap aktif untuk kamar yang belum siap, walaupun pengguna tidak berhak melewatinya. Server lalu menolak dengan ROOM_NOT_READY.
- **Menetapkan kamar dan check-in:** tipe lain yang ditandai "(naik kelas)" ditawarkan juga kepada pengguna tanpa `reservation.upgrade`. Server lalu menolak dengan PERMISSION_DENIED.
- **Check-out:** folio perusahaan yang belum nol tidak menonaktifkan tombol **Check-out**. Server lalu menolak dengan FOLIO_NOT_BALANCED.
- **Bagan kamar, jendela pindah:** LINE_NOT_CONFIRMED menampilkan teks tentang check-in ("Hanya kamar yang terkonfirmasi yang bisa check-in.").

## 6. Catatan screenshot

- **Kode properti `E2E`.** Pemilih properti menampilkan "E2E · Hotel Kamara Ubud", dan riwayat tamu menampilkan `E2E`. Kode itu dibuat oleh setup E2E bersama (`web/e2e/setup/seed.setup.ts`), yang juga dipakai test lain. Kode properti juga tidak bisa diubah setelah dibuat. Jadi kode itu dibiarkan. Hanya nama properti yang diubah oleh `web/e2e/docs/world.ts`.
- **Cara membuat ulang gambar:** `E2E_DOCS=1 E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh docs-screenshots.spec.ts`. Nama file selalu sama, jadi run baru menimpa gambar lama.

## 7. Kandidat video

Diisi di Fase 5.
