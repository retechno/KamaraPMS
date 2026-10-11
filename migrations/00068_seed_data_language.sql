-- +goose Up
-- The language of the data that a new property starts with: the names of the standard chart of accounts (199), departments (8) and charge codes (10). The CODES are the same in every language
-- (and so are the parent, the type, the normal side, the statement group and the system map: they come from usali_chart() and the seed functions that exist, which are not changed); only
-- the name differs. The names of a language are one function each (usali_chart_names, department_names, charge_code_names): the English ones are the standard definitions, the Indonesian ones are
-- written here.
--
-- A seed with a language calls the seed that exists and then gives the rows it has just made their name in that language: only the rows that still have the English standard name, so it never
-- touches a name that a person has changed, and a second call changes nothing. A property that exists is not touched: the seed runs only when a property is created. The names are data that
-- the people of the property keep (they can be changed at any time).

-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT c.code, c.name FROM usali_chart() c WHERE p_lang = 'en'
    UNION ALL
    SELECT n.code, n.name FROM (VALUES
        ('1000', 'Aset'),
        ('1100', 'Kas dan setara kas'),
        ('1110', 'Kas di front office'),
        ('1120', 'Kas kecil'),
        ('1130', 'Bank – rekening operasional'),
        ('1140', 'Bank – rekening penggajian'),
        ('1150', 'Akun penampung kartu kredit'),
        ('1160', 'Akun penampung pembayaran lain (e-wallet, voucher)'),
        ('1200', 'Piutang'),
        ('1210', 'Piutang tamu menginap (guest ledger)'),
        ('1220', 'Piutang perusahaan (city ledger)'),
        ('1230', 'Piutang lain-lain'),
        ('1240', 'Cadangan kerugian piutang'),
        ('1300', 'Persediaan'),
        ('1310', 'Persediaan makanan'),
        ('1320', 'Persediaan minuman'),
        ('1330', 'Persediaan perlengkapan operasional'),
        ('1340', 'Persediaan lain-lain'),
        ('1400', 'Biaya dibayar di muka dan aset lancar lainnya'),
        ('1410', 'Asuransi dibayar di muka'),
        ('1420', 'Pajak dibayar di muka'),
        ('1425', 'PPN masukan (dapat dikreditkan atau ditangguhkan)'),
        ('1430', 'Biaya dibayar di muka lainnya'),
        ('1440', 'Uang jaminan yang dibayarkan'),
        ('1500', 'Aset tetap'),
        ('1510', 'Tanah'),
        ('1520', 'Bangunan'),
        ('1530', 'Furnitur, perlengkapan, dan peralatan'),
        ('1540', 'Peralatan operasional (porselen, gelas, linen)'),
        ('1550', 'Kendaraan'),
        ('1560', 'Aset dalam penyelesaian'),
        ('1590', 'Akumulasi penyusutan'),
        ('1600', 'Aset lain-lain'),
        ('1610', 'Biaya pra-operasi'),
        ('1620', 'Izin dan aset takberwujud'),
        ('2000', 'Liabilitas'),
        ('2100', 'Utang'),
        ('2110', 'Utang usaha'),
        ('2120', 'Utang lain-lain'),
        ('2200', 'Beban yang masih harus dibayar'),
        ('2210', 'Utang gaji'),
        ('2220', 'Utang imbalan kerja'),
        ('2230', 'Utang utilitas'),
        ('2240', 'Utang bunga'),
        ('2250', 'Beban yang masih harus dibayar lainnya'),
        ('2300', 'Deposit tamu dan pendapatan diterima di muka'),
        ('2310', 'Deposit tamu (advance deposit)'),
        ('2320', 'Voucher dan sertifikat hadiah yang belum digunakan'),
        ('2330', 'Pendapatan diterima di muka (grup dan acara)'),
        ('2400', 'Utang pajak dan service charge'),
        ('2410', 'Utang PBJT (pajak hotel dan restoran)'),
        ('2420', 'Utang PPN'),
        ('2430', 'Utang service charge'),
        ('2440', 'Utang PPh dipotong/dipungut'),
        ('2450', 'Utang PPh badan'),
        ('2460', 'Utang pajak lainnya'),
        ('2500', 'Liabilitas lancar lainnya'),
        ('2510', 'Bagian lancar utang jangka panjang'),
        ('2520', 'Utang kepada pemilik dan perusahaan pengelola'),
        ('2530', 'Liabilitas lancar lain-lain'),
        ('2600', 'Liabilitas jangka panjang'),
        ('2610', 'Utang bank'),
        ('2620', 'Pinjaman pemegang saham'),
        ('2630', 'Liabilitas jangka panjang lainnya'),
        ('2990', 'Akun penampung sementara – transaksi belum dipetakan'),
        ('3000', 'Ekuitas'),
        ('3100', 'Modal disetor'),
        ('3200', 'Laba ditahan (tahun sebelumnya)'),
        ('3300', 'Prive dan dividen'),
        ('3900', 'Ekuitas saldo awal'),
        ('4000', 'Pendapatan operasional'),
        ('4100', 'Pendapatan kamar'),
        ('4110', 'Pendapatan kamar – individu'),
        ('4120', 'Pendapatan kamar – grup'),
        ('4130', 'Pendapatan kamar – korporat dan kontrak'),
        ('4140', 'Pendapatan kamar – paket'),
        ('4150', 'Pendapatan kamar lainnya (extra bed, early check-in, late check-out)'),
        ('4160', 'Kamar – potongan dan rabat'),
        ('4200', 'Pendapatan makanan dan minuman'),
        ('4210', 'Pendapatan makanan'),
        ('4220', 'Pendapatan minuman'),
        ('4230', 'Pendapatan minibar'),
        ('4240', 'Pendapatan makanan dan minuman banquet dan rapat'),
        ('4250', 'Pendapatan room service'),
        ('4260', 'Makanan dan minuman – pendapatan lainnya'),
        ('4290', 'Makanan dan minuman – potongan'),
        ('4300', 'Departemen operasional lainnya'),
        ('4310', 'Spa dan kebugaran'),
        ('4320', 'Telekomunikasi dan internet'),
        ('4330', 'Parkir'),
        ('4340', 'Rekreasi dan hiburan'),
        ('4350', 'Laundry dan valet'),
        ('4360', 'Transportasi'),
        ('4370', 'Business center'),
        ('4390', 'Departemen operasional lainnya – potongan'),
        ('4400', 'Sewa dan pendapatan lainnya'),
        ('4410', 'Sewa ruang rapat'),
        ('4420', 'Sewa toko dan ruang'),
        ('4430', 'Komisi yang diterima'),
        ('4440', 'Konsesi'),
        ('4500', 'Pendapatan lain-lain'),
        ('4510', 'Pendapatan no-show dan pembatalan'),
        ('4520', 'Deposit hangus'),
        ('4530', 'Service charge yang menjadi pendapatan hotel'),
        ('4540', 'Keuntungan selisih kurs'),
        ('4590', 'Pendapatan lainnya'),
        ('5000', 'Beban departemen'),
        ('5100', 'Beban departemen kamar'),
        ('5110', 'Gaji dan upah'),
        ('5120', 'Tunjangan karyawan'),
        ('5130', 'Laundry dan linen'),
        ('5140', 'Perlengkapan kebersihan'),
        ('5150', 'Perlengkapan tamu'),
        ('5160', 'Komisi (agen perjalanan dan OTA)'),
        ('5170', 'Jasa kontrak'),
        ('5180', 'Beban reservasi (GDS, channel manager)'),
        ('5190', 'Beban kamar lainnya'),
        ('5200', 'Beban departemen makanan dan minuman'),
        ('5210', 'Harga pokok penjualan makanan'),
        ('5220', 'Harga pokok penjualan minuman'),
        ('5230', 'Gaji dan upah'),
        ('5240', 'Tunjangan karyawan'),
        ('5250', 'Porselen, gelas, perak, dan linen'),
        ('5260', 'Bahan bakar dapur'),
        ('5270', 'Musik dan hiburan'),
        ('5280', 'Beban makanan dan minuman lainnya'),
        ('5300', 'Beban departemen operasional lainnya'),
        ('5310', 'Harga pokok penjualan'),
        ('5320', 'Gaji dan upah'),
        ('5330', 'Tunjangan karyawan'),
        ('5340', 'Beban lainnya'),
        ('6000', 'Beban operasional tidak terdistribusi'),
        ('6100', 'Administrasi dan umum'),
        ('6110', 'Gaji dan upah'),
        ('6120', 'Tunjangan karyawan'),
        ('6130', 'Komisi kartu kredit'),
        ('6140', 'Beban piutang tak tertagih'),
        ('6150', 'Jasa profesional'),
        ('6160', 'Alat tulis kantor dan percetakan'),
        ('6170', 'Perjalanan dinas dan jamuan'),
        ('6180', 'Biaya izin dan lisensi'),
        ('6190', 'Administrasi dan umum lainnya'),
        ('6195', 'Selisih kas (lebih dan kurang)'),
        ('6200', 'Sistem informasi dan telekomunikasi'),
        ('6210', 'Gaji dan upah'),
        ('6220', 'Perangkat lunak dan lisensi'),
        ('6230', 'Perangkat keras dan dukungan teknis'),
        ('6240', 'Saluran telepon dan internet'),
        ('6300', 'Penjualan dan pemasaran'),
        ('6310', 'Gaji dan upah'),
        ('6320', 'Tunjangan karyawan'),
        ('6330', 'Iklan'),
        ('6340', 'Promosi dan program loyalitas'),
        ('6350', 'Biaya pemasaran waralaba'),
        ('6360', 'Pemasaran online dan digital'),
        ('6390', 'Penjualan dan pemasaran lainnya'),
        ('6400', 'Teknik dan pemeliharaan (engineering)'),
        ('6410', 'Gaji dan upah'),
        ('6420', 'Tunjangan karyawan'),
        ('6430', 'Perbaikan dan pemeliharaan – gedung'),
        ('6440', 'Perbaikan dan pemeliharaan – peralatan'),
        ('6450', 'Taman dan lanskap'),
        ('6460', 'Pengendalian hama dan pembuangan sampah'),
        ('6470', 'Perlengkapan pemeliharaan'),
        ('6480', 'Jasa kontrak'),
        ('6500', 'Utilitas'),
        ('6510', 'Listrik'),
        ('6520', 'Air dan air limbah'),
        ('6530', 'Gas dan bahan bakar'),
        ('6540', 'Utilitas lainnya'),
        ('6700', 'Biaya manajemen dan waralaba'),
        ('6710', 'Biaya manajemen dasar'),
        ('6720', 'Biaya manajemen insentif'),
        ('6730', 'Royalti waralaba'),
        ('7000', 'Pendapatan dan beban nonoperasional'),
        ('7100', 'Beban sewa'),
        ('7110', 'Sewa tanah dan bangunan'),
        ('7120', 'Sewa peralatan'),
        ('7200', 'Pajak properti dan pajak lainnya'),
        ('7210', 'Pajak bumi dan bangunan (PBB)'),
        ('7220', 'Pajak lainnya (bukan pajak penghasilan)'),
        ('7300', 'Asuransi'),
        ('7310', 'Asuransi properti'),
        ('7320', 'Asuransi tanggung gugat dan lainnya'),
        ('7400', 'Pos nonoperasional lainnya'),
        ('7410', 'Beban nonoperasional lainnya'),
        ('7420', 'Laba atau rugi pelepasan aset'),
        ('7430', 'Pendapatan nonoperasional lainnya'),
        ('7500', 'Penyusutan dan amortisasi'),
        ('7510', 'Penyusutan – bangunan'),
        ('7520', 'Penyusutan – furnitur, perlengkapan, dan peralatan'),
        ('7530', 'Amortisasi'),
        ('7600', 'Bunga'),
        ('7610', 'Beban bunga – pinjaman bank'),
        ('7620', 'Beban bunga – lainnya'),
        ('7630', 'Pendapatan bunga'),
        ('7900', 'Beban pajak penghasilan'),
        ('7910', 'Beban pajak kini'),
        ('7920', 'Beban pajak tangguhan')
    ) AS n (code, name) WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION department_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT n.code, n.name FROM (VALUES
        ('ROOMS', 'Rooms'),
        ('FB', 'Food and beverage'),
        ('OOD', 'Other operated departments'),
        ('AG', 'Administrative and general'),
        ('IT', 'Information and telecommunications'),
        ('SM', 'Sales and marketing'),
        ('POM', 'Property operations and maintenance'),
        ('UTIL', 'Utilities')
    ) AS n (code, name) WHERE p_lang = 'en'
    UNION ALL
    SELECT n.code, n.name FROM (VALUES
        ('ROOMS', 'Kamar'),
        ('FB', 'Makanan dan minuman'),
        ('OOD', 'Departemen operasional lainnya'),
        ('AG', 'Administrasi dan umum'),
        ('IT', 'Teknologi informasi dan telekomunikasi'),
        ('SM', 'Penjualan dan pemasaran'),
        ('POM', 'Teknik dan pemeliharaan (engineering)'),
        ('UTIL', 'Utilitas')
    ) AS n (code, name) WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION charge_code_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT n.code, n.name FROM (VALUES
        ('ROOM', 'Room charge'),
        ('ROOM_EXEMPT', 'Room charge (no tax or service)'),
        ('BREAKFAST', 'Breakfast'),
        ('RESTAURANT', 'Restaurant'),
        ('LAUNDRY', 'Laundry'),
        ('MINIBAR', 'Minibar'),
        ('EXTRA_BED', 'Extra bed'),
        ('NO_SHOW_FEE', 'No-show fee'),
        ('CANCEL_FEE', 'Cancellation fee'),
        ('OTHER', 'Other charges')
    ) AS n (code, name) WHERE p_lang = 'en'
    UNION ALL
    SELECT n.code, n.name FROM (VALUES
        ('ROOM', 'Biaya kamar'),
        ('ROOM_EXEMPT', 'Biaya kamar (tanpa pajak dan service charge)'),
        ('BREAKFAST', 'Sarapan'),
        ('RESTAURANT', 'Restoran'),
        ('LAUNDRY', 'Laundry'),
        ('MINIBAR', 'Minibar'),
        ('EXTRA_BED', 'Extra bed'),
        ('NO_SHOW_FEE', 'Biaya no-show'),
        ('CANCEL_FEE', 'Biaya pembatalan'),
        ('OTHER', 'Biaya lain-lain')
    ) AS n (code, name) WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The language is one that has names: a seed with another language is refused, not seeded in English.
-- +goose StatementBegin
CREATE FUNCTION seed_language_check(p_lang text) RETURNS void
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF p_lang IS NULL OR p_lang NOT IN ('en', 'id') THEN
        RAISE EXCEPTION 'the language of the seeded data is en or id, not %', coalesce(p_lang, 'NULL') USING ERRCODE = '22023';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION seed_chart_of_accounts(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint, p_start_date date, p_lang text) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    PERFORM seed_language_check(p_lang);
    v_inserted := seed_chart_of_accounts(p_tenant_id, p_property_id, p_actor_id, p_start_date);
    IF p_lang <> 'en' THEN
        UPDATE gl_accounts a SET name = n.name
          FROM usali_chart_names('en') e
          JOIN usali_chart_names(p_lang) n ON n.code = e.code
         WHERE a.property_id = p_property_id AND a.code = e.code AND a.name = e.name AND n.name <> e.name;
    END IF;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION seed_departments(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint, p_lang text) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    PERFORM seed_language_check(p_lang);
    v_inserted := seed_departments(p_tenant_id, p_property_id, p_actor_id);
    IF p_lang <> 'en' THEN
        UPDATE departments d SET name = n.name
          FROM department_names('en') e
          JOIN department_names(p_lang) n ON n.code = e.code
         WHERE d.property_id = p_property_id AND d.code = e.code AND d.name = e.name AND n.name <> e.name;
    END IF;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION seed_charge_codes(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint, p_lang text) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    PERFORM seed_language_check(p_lang);
    v_inserted := seed_charge_codes(p_tenant_id, p_property_id, p_actor_id);
    IF p_lang <> 'en' THEN
        UPDATE charge_codes c SET name = n.name
          FROM charge_code_names('en') e
          JOIN charge_code_names(p_lang) n ON n.code = e.code
         WHERE c.property_id = p_property_id AND c.code = e.code AND c.is_system AND c.name = e.name AND n.name <> e.name;
    END IF;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS seed_charge_codes(bigint, bigint, bigint, text);
DROP FUNCTION IF EXISTS seed_departments(bigint, bigint, bigint, text);
DROP FUNCTION IF EXISTS seed_chart_of_accounts(bigint, bigint, bigint, date, text);
DROP FUNCTION IF EXISTS seed_language_check(text);
DROP FUNCTION IF EXISTS charge_code_names(text);
DROP FUNCTION IF EXISTS department_names(text);
DROP FUNCTION IF EXISTS usali_chart_names(text);
