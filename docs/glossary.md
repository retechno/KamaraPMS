# Glosarium istilah antarmuka (Indonesia)

Satu istilah untuk satu konsep, dipakai di menu, judul halaman, tombol, dan pesan (`web/src/i18n/locales/id.ts`). Bila menambah teks baru, pakai istilah di bawah; bila perlu istilah lain, ubah di sini dulu.

## Menu dan halaman

| English | Indonesia | Catatan |
|---|---|---|
| Front desk | Front Desk | Nama bagian menu dan judul halaman yang berisi tiga tab. Tab dan item menunya: Kedatangan, Menginap, Keberangkatan. |
| Arrivals / In-house / Departures | Kedatangan / Menginap / Keberangkatan | "In-house" tidak dipakai lagi. |
| Walk-in | Walk-in | Tamu tanpa reservasi. |
| Tape chart | Bagan kamar | |
| Night audit | Audit malam | |
| City ledger | Piutang perusahaan | Akun piutang perusahaan yang tagihannya dipindahkan dari folio tamu. |
| Housekeeping | Housekeeping | Tetap: istilah baku di hotel. Daftar kerjanya "Daftar pembersihan". |
| Rate plan | Paket tarif | |
| Fiscal year | Tahun buku | "Tahun fiskal" tidak dipakai. |
| Chart of accounts | Bagan akun | |
| Bed type | Tipe tempat tidur | "Tipe ranjang" tidak dipakai. |
| Tax return (SPT) | SPT Masa PPN | "Pengembalian pajak" keliru: itu restitusi. Di teks, satu SPT disebut "SPT". |

## Istilah di dalam teks

| English | Indonesia |
|---|---|
| Credit note | Nota kredit |
| Write-off | Penghapusan piutang |
| Complimentary | Komplimen |
| House use | Pemakaian hotel |
| Stop sell | Tutup jual (CTA / CTD tetap singkatan, dengan penjelasannya) |
| No-show | Tidak datang |
| Upgrade | Naik kelas |
| Housekeeper | Petugas kebersihan |
| Check-in / Check-out | Check-in / Check-out (dengan tanda hubung) |

## Yang sengaja tetap bahasa Inggris

Istilah serapan yang dipakai staf hotel sehari-hari dan tidak punya padanan yang dikenal: *folio, deposit, void, refund, reversal, shift, drawer (laci), ADR, RevPAR, PKP, PDF, CSV*.

## Kode dan data dari server

- Kode API (CASH, BANK_TRANSFER, `payment.posted`, ALREADY_POSTED, kode error) tidak pernah tampil sebagai teks. Labelnya ada di i18n (`payMethod`, `roomChargeStatus`, `auditAction`, `auditEntity`, `status`, `errorActions`, `systemAccount`, `permissionLabel`, `statementLine`); kode tanpa label tampil sebagai teks terbaca dan `console.warn` sekali (`labelOf`, `src/i18n/labels.ts`).
- Tes `src/i18n/noRawCodes.test.ts` memindai template dan file bahasa untuk kode mentah.
- Nama akun di bagan akun, kode tagihan, dan nama perusahaan adalah data yang diketik pengguna: tidak diterjemahkan.
