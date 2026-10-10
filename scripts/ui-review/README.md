# Review UI: data contoh + screenshot

Tiga skrip untuk database DEVELOPMENT (jangan dipakai di produksi):

- `seed-demo.mjs`: membuat properti **UIREV** dengan 50 kamar dan data yang realistis: hari kemarin yang sudah ditutup lewat night audit, 30 tamu menginap, kedatangan dan keberangkatan hari ini, sekitar 120 reservasi 14 hari ke depan (termasuk draf dan pembatalan), pembayaran berbagai metode, status housekeeping campuran, kamar out of order, permintaan maintenance, barang temuan, dan satu perusahaan (city ledger). Skrip ini sudah diuji terhadap API KamaraPMS.
- `overflow.mjs`: membuka semua rute di layar ponsel (390 px) dan melaporkan halaman yang bisa digeser ke samping, beserta elemen penyebabnya.
- `screenshots.mjs`: login lewat layar login, memilih bahasa Indonesia dan properti UIREV, lalu memotret semua halaman dalam 3 ukuran (desktop 1440, laptop 1366, ponsel 390), mode gelap untuk halaman utama, serta beberapa keadaan khusus (Ctrl K, drawer menu ponsel, sheet kamar, dialog check-in). Hasilnya disimpan di `screenshots/<label>/` beserta `index.md`.

## Langkah (dari root repo, Windows PowerShell atau bash)

1. Jalankan database, API (`go run ./cmd/api`), dan web (`cd web; npm run dev`).
2. Isi kredensial admin tenant DEMO:
   - PowerShell: `$env:SHOT_USER="admin@..."; $env:SHOT_PASS="..."`
   - bash: `export SHOT_USER=admin@... SHOT_PASS=...`
3. Data contoh:
   ```
   node scripts/ui-review/seed-demo.mjs property
   go run ./cmd/pms-seed rooms -tenant DEMO -property UIREV -floors 5 -per-floor 10
   go run ./cmd/pms-seed rates -tenant DEMO -property UIREV -days 60
   node scripts/ui-review/seed-demo.mjs data
   ```
   Baris "lewati: ..." itu normal: misalnya tipe kamar yang sudah penuh pada tanggal tertentu.
4. Screenshot:
   ```
   cd web
   npm i -D playwright
   npx playwright install chromium
   node ../scripts/ui-review/screenshots.mjs
   ```
   Untuk putaran kedua dengan akun resepsionis atau housekeeping: ganti `SHOT_USER`/`SHOT_PASS` dan set `SHOT_LABEL=resepsionis`.
   Pemeriksaan lebar di ponsel (tidak boleh ada halaman yang lebih lebar dari 390 px; keluar dengan kode 1 bila ada):
   ```
   node ../scripts/ui-review/overflow.mjs
   ```
   Di Git Bash, nilai yang diawali `/` (mis. `SHOT_ONLY="/,/arrivals"`) diubah menjadi path Windows: awali perintah dengan `MSYS_NO_PATHCONV=1`. Bila `SHOT_WEB` tidak diisi, skrip memakai http://localhost:5173; vite memilih port lain bila 5173 terpakai.
5. Zip folder `web/screenshots/` lalu kirim ke Claude di percakapan review.

Tambahkan `screenshots/` ke `.gitignore`. `playwright` boleh dihapus lagi dari devDependencies setelah selesai.
