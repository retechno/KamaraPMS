# D3: data bawaan berbahasa Indonesia untuk properti baru (RENCANA, belum ada kode)

Status: rencana disetujui dengan koreksi (ui-uplift-plan 2.5); daftar terjemahan disetujui dengan koreksi akhir (nama final di tabel di bawah); dikerjakan. Branch `ui-uplift-d3` dari `main` (13e39a1). Berkas ini belum di-commit.

## 1. Temuan

- **Bagan akun:** 199 akun, satu definisi di fungsi SQL `usali_chart()` (migrasi 00028, dibungkus ulang oleh 00042 dan 00047 yang menambah akun 1425, 6195, dan lainnya). `seed_chart_of_accounts(tenant, property, actor, start)` menyalin `usali_chart()` ke `gl_accounts`, mengisi `parent_id`, `gl_account_map` (10 kunci peta dari kode akun), dan `charge_codes.gl_account_code`. Dipanggil `accounting.Service.SeedProperty` (hook `tenancy.OnPropertyCreated`). Struktur (kode, induk, tipe, sisi normal, kelompok laporan) dan peta sistem hanya bergantung pada KODE; hanya kolom `name` adalah teks.
- **Departemen:** 8 departemen di `seed_departments(...)` (migrasi 00051), dipanggil `departments.Service.SeedProperty`; juga mengaitkan kode biaya ke departemen lewat KODE.
- **Di mana properti dibuat:** hanya `tenancy.Service.CreateProperty`, lewat (a) API `POST /api/v1/properties` (khusus admin tenant), (b) layar Setup → Properti → Baru (`PropertyFormView`), (c) skrip `scripts/ui-review/seed-demo.mjs property` (memanggil API yang sama). **Tidak ada CLI yang membuat properti:** `pms-admin` hanya `create-tenant`, `show-tenant`, `create-admin`; `pms-seed` hanya mengisi kamar dan harga properti yang sudah ada. (Keputusan K2: tidak ditambah.)
- **Sampai ke seed:** `CreateProperty` membuka transaksi, menyisipkan properti dan hari bisnis, lalu memanggil hook satu per satu dengan `tenancy.PropertyCreated{TenantID, PropertyID, BusinessDate, ActorID}`. Pilihan bahasa belum ada di struct itu.
- Properti belum punya kolom bahasa; tidak perlu (lihat 3).

## 2. Rancangan

**Pilihan bahasa:** `data_language` = `en` | `id`.

1. **API:** `CreatePropertyRequest.data_language` (opsional; kosong = `en`, jadi klien lama dan semua tes lama tidak berubah). Nilai lain: 422 `VALIDATION_FAILED`, field `data_language`, kode `INVALID_VALUE`.
2. **Tenancy:** `CreatePropertyInput.DataLanguage` divalidasi di `CreateProperty`, lalu `PropertyCreated.DataLanguage` dibawa ke semua hook. Hook akuntansi, departemen, dan kode biaya memakainya; tipe tempat tidur mengabaikannya (lihat 5).
3. **Migrasi 00068 (dengan Down):**
   - `usali_chart_names(p_lang text)` → (code, name): `en` dari `usali_chart()` apa adanya; `id` dari daftar terjemahan di bagian 4. Bahasa lain: kosong.
   - `department_names(p_lang text)` → (code, name), sama.
   - `seed_chart_of_accounts(tenant, property, actor, start_date, p_lang text)` dan `seed_departments(tenant, property, actor, p_lang text)`: fungsi BARU (overload) yang memanggil fungsi lama (struktur + peta + kode biaya, tidak diubah) lalu mengganti nama HANYA baris yang baru dibuat dan masih bernama bawaan Inggris (`name = nama en`). Fungsi lama tidak diubah (migrasi lama tidak diubah).
   - Down: menghapus fungsi baru.
4. **sqlc/Go:** `SeedChart` dan `SeedDepartments` memanggil fungsi baru dengan `data_language`; `accounting.SeedProperty` dan `departments.SeedProperty` meneruskannya dari `PropertyCreated`. Audit `accounting.chart_seeded` dan `department.seeded` mencatat bahasanya (`data_language`).
5. **Layar:** `PropertyFormView` (hanya mode buat) mendapat pilihan "Bahasa data bawaan" (Indonesia / English), bawaannya bahasa halaman (`locale` i18n). Teks bantu: "Nama akun, departemen, dan kode biaya yang dibuat untuk properti ini. Bisa diubah nanti. Nama kode biaya tercetak di folio dan invoice tamu. Properti yang sudah ada tidak berubah." Dikirim sebagai `data_language`.
6. **OpenAPI:** `data_language` di `CreatePropertyRequest` → `npm run gen:api`.
7. **Skrip demo:** `seed-demo.mjs property` memakai `SHOT_DATA_LANG` (bawaan `id`) agar properti review berbahasa Indonesia.

## 3. Properti yang sudah ada

Tidak disentuh: migrasi 00068 tidak mengubah baris yang ada (tanpa backfill), dan fungsi bahasa hanya dipanggil dari hook pembuatan properti. Tidak ada kolom `language` di `properties`: yang dipilih hanyalah teks awal; sesudahnya nama akun dan departemen adalah data yang dikelola pengguna (`docs/glossary.md` dicatat).

## 4. Daftar terjemahan lengkap (untuk Anda periksa)

Kode, kode induk, tipe, sisi normal, kelompok laporan, `is_postable` dan `map_key` IDENTIK di kedua bahasa; hanya kolom nama yang berbeda. Prinsip: istilah akuntansi yang lazim di Indonesia (PSAK/SAK), glosarium proyek (Piutang perusahaan, Deposit, Nota kredit, Folio), istilah hotel yang sudah baku tetap dalam bahasa Inggris (front office, guest ledger, extra bed, early check-in, late check-out, no-show, OTA, GDS, channel manager, e-wallet, business center, banquet, room service, spa), tanda pisah "–" (en dash) seperti contoh Anda. Nama yang berulang di induk berbeda (mis. "Gaji dan upah") sama dengan aslinya yang juga berulang.

### Bagan akun (199)

| Kode | English | Indonesia |
|---|---|---|
| 1000 | Assets | Aset |
| 1100 | Cash and cash equivalents | Kas dan setara kas |
| 1110 | Cash on hand - front desk | Kas di front office |
| 1120 | Cash on hand - petty cash | Kas kecil |
| 1130 | Bank - operating account | Bank – rekening operasional |
| 1140 | Bank - payroll account | Bank – rekening penggajian |
| 1150 | Credit card clearing | Akun penampung kartu kredit |
| 1160 | Other payment clearing (e-wallet, vouchers) | Akun penampung pembayaran lain (e-wallet, voucher) |
| 1200 | Receivables | Piutang |
| 1210 | Guest ledger (in-house guests) | Piutang tamu menginap (guest ledger) |
| 1220 | City ledger - accounts receivable | Piutang perusahaan (city ledger) |
| 1230 | Other receivables | Piutang lain-lain |
| 1240 | Allowance for doubtful accounts | Cadangan kerugian piutang |
| 1300 | Inventories | Persediaan |
| 1310 | Food inventory | Persediaan makanan |
| 1320 | Beverage inventory | Persediaan minuman |
| 1330 | Operating supplies inventory | Persediaan perlengkapan operasional |
| 1340 | Other inventories | Persediaan lain-lain |
| 1400 | Prepaid expenses and other current assets | Biaya dibayar di muka dan aset lancar lainnya |
| 1410 | Prepaid insurance | Asuransi dibayar di muka |
| 1420 | Prepaid taxes | Pajak dibayar di muka |
| 1425 | Input VAT (claimable or deferred) | PPN masukan (dapat dikreditkan atau ditangguhkan) |
| 1430 | Other prepaid expenses | Biaya dibayar di muka lainnya |
| 1440 | Security deposits paid | Uang jaminan yang dibayarkan |
| 1500 | Property and equipment | Aset tetap |
| 1510 | Land | Tanah |
| 1520 | Buildings | Bangunan |
| 1530 | Furniture, fixtures and equipment | Furnitur, perlengkapan, dan peralatan |
| 1540 | Operating equipment (china, glass, linen) | Peralatan operasional (porselen, gelas, linen) |
| 1550 | Vehicles | Kendaraan |
| 1560 | Construction in progress | Aset dalam penyelesaian |
| 1590 | Accumulated depreciation | Akumulasi penyusutan |
| 1600 | Other assets | Aset lain-lain |
| 1610 | Pre-opening costs | Biaya pra-operasi |
| 1620 | Licences and intangible assets | Izin dan aset takberwujud |
| 2000 | Liabilities | Liabilitas |
| 2100 | Payables | Utang |
| 2110 | Accounts payable - trade | Utang usaha |
| 2120 | Accounts payable - other | Utang lain-lain |
| 2200 | Accrued expenses | Beban yang masih harus dibayar |
| 2210 | Accrued payroll | Utang gaji |
| 2220 | Accrued employee benefits | Utang imbalan kerja |
| 2230 | Accrued utilities | Utang utilitas |
| 2240 | Accrued interest | Utang bunga |
| 2250 | Other accrued expenses | Beban yang masih harus dibayar lainnya |
| 2300 | Guest deposits and unearned revenue | Deposit tamu dan pendapatan diterima di muka |
| 2310 | Advance deposits | Deposit tamu (advance deposit) |
| 2320 | Gift certificates and vouchers outstanding | Voucher dan sertifikat hadiah yang belum digunakan |
| 2330 | Unearned revenue (groups and events) | Pendapatan diterima di muka (grup dan acara) |
| 2400 | Taxes and service charges payable | Utang pajak dan service charge |
| 2410 | Hotel and restaurant tax payable | Utang PBJT (pajak hotel dan restoran) |
| 2420 | Value added tax payable | Utang PPN |
| 2430 | Service charge payable | Utang service charge |
| 2440 | Withholding tax payable | Utang PPh dipotong/dipungut |
| 2450 | Income tax payable | Utang PPh badan |
| 2460 | Other taxes payable | Utang pajak lainnya |
| 2500 | Other current liabilities | Liabilitas lancar lainnya |
| 2510 | Current portion of long-term debt | Bagian lancar utang jangka panjang |
| 2520 | Due to owners and management company | Utang kepada pemilik dan perusahaan pengelola |
| 2530 | Other current liabilities | Liabilitas lancar lain-lain |
| 2600 | Long-term liabilities | Liabilitas jangka panjang |
| 2610 | Bank loans | Utang bank |
| 2620 | Shareholder loans | Pinjaman pemegang saham |
| 2630 | Other long-term liabilities | Liabilitas jangka panjang lainnya |
| 2990 | Suspense - unmapped items | Akun penampung sementara – transaksi belum dipetakan |
| 3000 | Equity | Ekuitas |
| 3100 | Paid-in capital | Modal disetor |
| 3200 | Retained earnings (prior years) | Laba ditahan (tahun sebelumnya) |
| 3300 | Owner drawings and dividends | Prive dan dividen |
| 3900 | Opening balance equity | Ekuitas saldo awal |
| 4000 | Operating revenue | Pendapatan operasional |
| 4100 | Rooms revenue | Pendapatan kamar |
| 4110 | Room revenue - transient | Pendapatan kamar – individu |
| 4120 | Room revenue - group | Pendapatan kamar – grup |
| 4130 | Room revenue - corporate and contract | Pendapatan kamar – korporat dan kontrak |
| 4140 | Room revenue - packages | Pendapatan kamar – paket |
| 4150 | Rooms - other revenue (extra bed, early check-in, late check-out) | Pendapatan kamar lainnya (extra bed, early check-in, late check-out) |
| 4160 | Rooms - allowances and rebates | Kamar – potongan dan rabat |
| 4200 | Food and beverage revenue | Pendapatan makanan dan minuman |
| 4210 | Food revenue | Pendapatan makanan |
| 4220 | Beverage revenue | Pendapatan minuman |
| 4230 | Minibar revenue | Pendapatan minibar |
| 4240 | Banquet and meeting F&B revenue | Pendapatan makanan dan minuman banquet dan rapat |
| 4250 | Room service revenue | Pendapatan room service |
| 4260 | F&B - other revenue | Makanan dan minuman – pendapatan lainnya |
| 4290 | F&B - allowances | Makanan dan minuman – potongan |
| 4300 | Other operated departments | Departemen operasional lainnya |
| 4310 | Spa and wellness | Spa dan kebugaran |
| 4320 | Telecommunications and internet | Telekomunikasi dan internet |
| 4330 | Parking | Parkir |
| 4340 | Recreation and leisure | Rekreasi dan hiburan |
| 4350 | Laundry and valet | Laundry dan valet |
| 4360 | Transportation | Transportasi |
| 4370 | Business centre | Business center |
| 4390 | Other operated departments - allowances | Departemen operasional lainnya – potongan |
| 4400 | Rentals and other income | Sewa dan pendapatan lainnya |
| 4410 | Meeting room rental | Sewa ruang rapat |
| 4420 | Shop and space rental | Sewa toko dan ruang |
| 4430 | Commissions earned | Komisi yang diterima |
| 4440 | Concessions | Konsesi |
| 4500 | Miscellaneous income | Pendapatan lain-lain |
| 4510 | No-show and cancellation fees | Pendapatan no-show dan pembatalan |
| 4520 | Forfeited deposits | Deposit hangus |
| 4530 | Service charge income retained | Service charge yang menjadi pendapatan hotel |
| 4540 | Foreign exchange gain | Keuntungan selisih kurs |
| 4590 | Other miscellaneous income | Pendapatan lainnya |
| 5000 | Departmental expenses | Beban departemen |
| 5100 | Rooms department expenses | Beban departemen kamar |
| 5110 | Salaries and wages | Gaji dan upah |
| 5120 | Employee benefits | Tunjangan karyawan |
| 5130 | Laundry and linen | Laundry dan linen |
| 5140 | Cleaning supplies | Perlengkapan kebersihan |
| 5150 | Guest supplies | Perlengkapan tamu |
| 5160 | Commissions (travel agents and OTAs) | Komisi (agen perjalanan dan OTA) |
| 5170 | Contract services | Jasa kontrak |
| 5180 | Reservation expenses (GDS, channel manager) | Beban reservasi (GDS, channel manager) |
| 5190 | Other rooms expenses | Beban kamar lainnya |
| 5200 | Food and beverage department expenses | Beban departemen makanan dan minuman |
| 5210 | Cost of food sales | Harga pokok penjualan makanan |
| 5220 | Cost of beverage sales | Harga pokok penjualan minuman |
| 5230 | Salaries and wages | Gaji dan upah |
| 5240 | Employee benefits | Tunjangan karyawan |
| 5250 | China, glassware, silver and linen | Porselen, gelas, perak, dan linen |
| 5260 | Kitchen fuel | Bahan bakar dapur |
| 5270 | Music and entertainment | Musik dan hiburan |
| 5280 | Other F&B expenses | Beban makanan dan minuman lainnya |
| 5300 | Other operated departments expenses | Beban departemen operasional lainnya |
| 5310 | Cost of sales | Harga pokok penjualan |
| 5320 | Salaries and wages | Gaji dan upah |
| 5330 | Employee benefits | Tunjangan karyawan |
| 5340 | Other expenses | Beban lainnya |
| 6000 | Undistributed operating expenses | Beban operasional tidak terdistribusi |
| 6100 | Administrative and general | Administrasi dan umum |
| 6110 | Salaries and wages | Gaji dan upah |
| 6120 | Employee benefits | Tunjangan karyawan |
| 6130 | Credit card commissions | Komisi kartu kredit |
| 6140 | Bad debt expense | Beban piutang tak tertagih |
| 6150 | Professional fees | Jasa profesional |
| 6160 | Office supplies and printing | Alat tulis kantor dan percetakan |
| 6170 | Travel and entertainment | Perjalanan dinas dan jamuan |
| 6180 | Licences and permits | Biaya izin dan lisensi |
| 6190 | Other administrative and general | Administrasi dan umum lainnya |
| 6195 | Cash over and short | Selisih kas (lebih dan kurang) |
| 6200 | Information and telecommunications systems | Sistem informasi dan telekomunikasi |
| 6210 | Salaries and wages | Gaji dan upah |
| 6220 | Software and licences | Perangkat lunak dan lisensi |
| 6230 | Hardware and support | Perangkat keras dan dukungan teknis |
| 6240 | Telecom lines and internet | Saluran telepon dan internet |
| 6300 | Sales and marketing | Penjualan dan pemasaran |
| 6310 | Salaries and wages | Gaji dan upah |
| 6320 | Employee benefits | Tunjangan karyawan |
| 6330 | Advertising | Iklan |
| 6340 | Promotions and loyalty programme | Promosi dan program loyalitas |
| 6350 | Franchise marketing fees | Biaya pemasaran waralaba |
| 6360 | Online and digital marketing | Pemasaran online dan digital |
| 6390 | Other sales and marketing | Penjualan dan pemasaran lainnya |
| 6400 | Property operations and maintenance | Teknik dan pemeliharaan (engineering) |
| 6410 | Salaries and wages | Gaji dan upah |
| 6420 | Employee benefits | Tunjangan karyawan |
| 6430 | Repairs and maintenance - building | Perbaikan dan pemeliharaan – gedung |
| 6440 | Repairs and maintenance - equipment | Perbaikan dan pemeliharaan – peralatan |
| 6450 | Grounds and landscaping | Taman dan lanskap |
| 6460 | Pest control and waste removal | Pengendalian hama dan pembuangan sampah |
| 6470 | Maintenance supplies | Perlengkapan pemeliharaan |
| 6480 | Contract services | Jasa kontrak |
| 6500 | Utilities | Utilitas |
| 6510 | Electricity | Listrik |
| 6520 | Water and sewage | Air dan air limbah |
| 6530 | Gas and fuel | Gas dan bahan bakar |
| 6540 | Other utilities | Utilitas lainnya |
| 6700 | Management and franchise fees | Biaya manajemen dan waralaba |
| 6710 | Base management fee | Biaya manajemen dasar |
| 6720 | Incentive management fee | Biaya manajemen insentif |
| 6730 | Franchise royalty fee | Royalti waralaba |
| 7000 | Non-operating income and expenses | Pendapatan dan beban nonoperasional |
| 7100 | Rent and leases | Beban sewa |
| 7110 | Land and building rent | Sewa tanah dan bangunan |
| 7120 | Equipment rent | Sewa peralatan |
| 7200 | Property and other taxes | Pajak properti dan pajak lainnya |
| 7210 | Property tax | Pajak bumi dan bangunan (PBB) |
| 7220 | Other taxes (not on income) | Pajak lainnya (bukan pajak penghasilan) |
| 7300 | Insurance | Asuransi |
| 7310 | Property insurance | Asuransi properti |
| 7320 | Liability and other insurance | Asuransi tanggung gugat dan lainnya |
| 7400 | Other non-operating items | Pos nonoperasional lainnya |
| 7410 | Other non-operating expense | Beban nonoperasional lainnya |
| 7420 | Gain or loss on disposal of assets | Laba atau rugi pelepasan aset |
| 7430 | Other non-operating income | Pendapatan nonoperasional lainnya |
| 7500 | Depreciation and amortization | Penyusutan dan amortisasi |
| 7510 | Depreciation - buildings | Penyusutan – bangunan |
| 7520 | Depreciation - furniture, fixtures and equipment | Penyusutan – furnitur, perlengkapan, dan peralatan |
| 7530 | Amortization | Amortisasi |
| 7600 | Interest | Bunga |
| 7610 | Interest expense - bank loans | Beban bunga – pinjaman bank |
| 7620 | Interest expense - other | Beban bunga – lainnya |
| 7630 | Interest income | Pendapatan bunga |
| 7900 | Income taxes | Beban pajak penghasilan |
| 7910 | Current income tax | Beban pajak kini |
| 7920 | Deferred income tax | Beban pajak tangguhan |

### Departemen (8)

| Kode | English | Indonesia |
|---|---|---|
| ROOMS | Rooms | Kamar |
| FB | Food and beverage | Makanan dan minuman |
| OOD | Other operated departments | Departemen operasional lainnya |
| AG | Administrative and general | Administrasi dan umum |
| IT | Information and telecommunications | Teknologi informasi dan telekomunikasi |
| SM | Sales and marketing | Penjualan dan pemasaran |
| POM | Property operations and maintenance | Teknik dan pemeliharaan (engineering) |
| UTIL | Utilities | Utilitas |

### Istilah baru untuk `docs/glossary.md`

| English | Indonesia | Catatan |
|---|---|---|
| Liabilities | Liabilitas | Bukan "Kewajiban" (PSAK). |
| Accounts payable | Utang usaha | "Utang" dengan U, bukan "Hutang". |
| Retained earnings | Laba ditahan | |
| Owner drawings | Prive | |
| Accrued expenses | Beban yang masih harus dibayar | |
| Input VAT / VAT payable | PPN masukan / Utang PPN | |
| Suspense account | Akun penampung sementara | |
| Clearing account | Akun penampung | Kartu kredit dan pembayaran lain sebelum masuk bank. |
| Cost of sales | Harga pokok penjualan | |
| Allowances (revenue) | Potongan dan rabat | |
| Undistributed operating expenses | Beban operasional tidak terdistribusi | Istilah USALI. |
| Guest ledger | Folio tamu menginap (guest ledger) | |
| Service charge | Service charge | Tetap (sudah dipakai di layar). |
| Employee benefits | Imbalan kerja | PSAK 24. "Utang imbalan kerja", "Tunjangan karyawan" tetap untuk beban. |
| Withholding tax | PPh dipotong/dipungut | "Utang PPh dipotong/dipungut". |
| Current / deferred income tax expense | Beban pajak kini / tangguhan | PSAK 46. |
| Hotel and restaurant tax | PBJT (pajak hotel dan restoran) | Sejak UU HKPD (UU 1/2022) pajak hotel dan restoran menjadi Pajak Barang dan Jasa Tertentu (PBJT). Akun yang menyebutnya memakai "PBJT" dengan penjelasan di kurung. |

## 5. Kode biaya (10) dan teks bawaan lain

**Kode biaya MASUK D3** (keputusan K3), mekanisme yang sama: `charge_code_names(lang)` dan overload `seed_charge_codes(tenant, property, actor, lang)` (migrasi 00068) yang memanggil fungsi lama (migrasi 00014, tidak diubah) lalu mengganti nama hanya pada baris yang baru dibuat dan masih bernama bawaan Inggris. Kode (`ROOM`, `BREAKFAST`, ...) tidak berubah. Nama kode biaya tercetak di baris folio dan invoice tamu. Hook `billingconfig.SeedProperty` meneruskan `DataLanguage`.

| Kode | English | Indonesia |
|---|---|---|
| ROOM | Room charge | Biaya kamar |
| ROOM_EXEMPT | Room charge (no tax or service) | Biaya kamar (tanpa pajak dan service charge) |
| BREAKFAST | Breakfast | Sarapan |
| RESTAURANT | Restaurant | Restoran |
| LAUNDRY | Laundry | Laundry |
| MINIBAR | Minibar | Minibar |
| EXTRA_BED | Extra bed | Extra bed |
| NO_SHOW_FEE | No-show fee | Biaya no-show |
| CANCEL_FEE | Cancellation fee | Biaya pembatalan |
| OTHER | Other charges | Biaya lain-lain |

Tetap seperti sekarang (keputusan K3):
- **Tipe tempat tidur (5):** King, Queen, Double, Twin, Single (istilah baku hotel).
- **Prefiks nomor dokumen** (RES, STY, FOL, PAY, CLR, CINV, CN, JV, ...): kode, bukan bahasa.

Bukan teks bahasa: `gl_account_map`, `property_tax_settings`, `property_cashier_settings`, `accounting_settings`, metode bayar (kode yang labelnya ada di i18n). Deskripsi jurnal otomatis yang ditulis program (night audit, pembayaran) berbahasa Inggris tetapi bukan data bawaan: di luar D3.

## 6. Tes (PostgreSQL nyata)

- Kedua template sama kecuali nama: `usali_chart_names('id')` dan `usali_chart_names('en')` punya himpunan kode yang SAMA, tidak ada nama kosong, dan jumlahnya 199; sama untuk departemen (8) dan kode biaya (10). Karena struktur hanya dari satu fungsi (`usali_chart()`), tes membuat properti `en` dan `id` dan membandingkan `gl_accounts` (semua kolom kecuali `name`/id/timestamp), `gl_account_map`, `charge_codes.gl_account_code`, `departments` (kode, urutan) dan kaitan departemen-kode biaya: identik.
- Terjemahan: nama `id` tidak boleh sama dengan `en` untuk akun-akun yang diketahui berbeda; daftar kecil istilah yang sengaja sama (Parkir≠Parking; "Business center" memang sama) ditulis eksplisit supaya penambahan akun baru tanpa terjemahan gagal di tes (akun baru di `usali_chart()` yang tidak punya nama `id` memutus tes).
- Pilihan bahasa: API default `en`; `id` menghasilkan nama Indonesia; nilai lain → 422; tenant lain tidak bisa memicu; properti yang sudah ada tidak berubah setelah properti baru dibuat dalam bahasa lain; seed idempoten (panggilan kedua tidak mengganti nama yang sudah diubah pengguna).
- Web: `PropertyFormView` mengirim `data_language` dari bahasa halaman, bisa diganti; tidak tampil di mode ubah.
- Pemeriksaan: `go build`, `vet`, `scripts/lint.sh`, `go test -race -count=1 ./...` PENUH (lewat container `golang:1.26-trixie`, karena mesin ini tanpa compiler C), `scripts/db-test.sh` (migrasi naik/turun/naik), `scripts/sqlc.sh diff`, `npm run gen:api`, web `npm test` penuh, `type-check`, `build`, `overflow.mjs`; screenshot layar Properti baru dan bagan akun properti berbahasa Indonesia.

## 7. Keputusan (disetujui)

- **K1.** Terjemahan di SQL (migrasi 00068); fungsi lama utuh. Disetujui.
- **K2.** **Tidak ada CLI `create-property`; `-data-lang` DIHAPUS dari cakupan.** Properti hanya dibuat lewat layar dan API (dan skrip demo yang memanggil API). `pms-admin` dan `pms-seed` tidak berubah.
- **K3.** Kode biaya (10 nama) MASUK D3 dengan mekanisme yang sama (bagian 5). Teks bantu pilihan bahasa di layar Properti baru: "Nama akun, departemen, dan kode biaya yang dibuat untuk properti ini. Bisa diubah nanti. Nama kode biaya tercetak di folio dan invoice tamu. Properti yang sudah ada tidak berubah." Tipe tempat tidur dan prefiks nomor dokumen tetap.
- Koreksi terjemahan: 2410 "Utang PBJT (pajak hotel dan restoran)" (tidak ada akun lain yang menyebut pajak hotel/restoran; PBJT masuk glosarium); departemen POM "Teknik dan pemeliharaan (engineering)"; AG "Administrasi dan umum".
