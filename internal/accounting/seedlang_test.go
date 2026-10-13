package accounting_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/tenancy"
)

// names reads the (code, name) pairs of a names function.
func (f *fx) names(t *testing.T, fn, lang string) map[string]string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), fmt.Sprintf(`SELECT code, name FROM %s($1)`, fn), lang)
	must(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, n string
		must(t, rows.Scan(&c, &n))
		out[c] = n
	}
	must(t, rows.Err())
	return out
}

func sameCodes(t *testing.T, what string, en, id map[string]string) {
	t.Helper()
	for c := range en {
		if _, ok := id[c]; !ok {
			t.Errorf("%s: %s has no Indonesian name", what, c)
		}
	}
	for c := range id {
		if _, ok := en[c]; !ok {
			t.Errorf("%s: %s has an Indonesian name and is not a standard code", what, c)
		}
	}
}

// The two languages have the same codes and only the names differ: the standard chart is one definition (usali_chart), the names of a language are a function of the code. A new account in the standard
// chart without an Indonesian name stops here.
func TestTheNamesOfEveryLanguageAreOfTheSameCodes(t *testing.T) {
	f := setup(t)
	for _, c := range []struct {
		fn    string
		count int
	}{{"usali_chart_names", 201}, {"department_names", 8}, {"charge_code_names", 11}} {
		en, id := f.names(t, c.fn, "en"), f.names(t, c.fn, "id")
		if len(en) != c.count || len(id) != c.count {
			t.Fatalf("%s: %d English and %d Indonesian names, want %d", c.fn, len(en), len(id), c.count)
		}
		sameCodes(t, c.fn, en, id)
		for code, n := range id {
			if strings.TrimSpace(n) == "" {
				t.Errorf("%s: the Indonesian name of %s is empty", c.fn, code)
			}
			if len(n) > 150 {
				t.Errorf("%s: the Indonesian name of %s is too long: %d", c.fn, code, len(n))
			}
		}
	}
	// the English names are the standard definition, word for word
	var differ int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM usali_chart() c JOIN usali_chart_names('en') n ON n.code = c.code WHERE n.name <> c.name`).Scan(&differ))
	if differ != 0 || f.Count(t, `SELECT count(*) FROM usali_chart()`) != 201 {
		t.Fatalf("the English names are those of usali_chart(): %d differ", differ)
	}
	// a language that has no names is refused, not seeded in English
	if _, err := f.Pool.Exec(context.Background(), `SELECT seed_chart_of_accounts(1, 1, NULL, DATE '2026-09-30', 'fr')`); err == nil {
		t.Fatal("a language without names must be refused")
	}
}

// What the Indonesian names are, for the names that matter most: the accounts and departments the screens and the reports show first, and the charge codes that print on a folio.
func TestAPropertyInIndonesianHasIndonesianNames(t *testing.T) {
	f := setup(t)
	id := f.PropertyData(t, f.tenantID, "UBUD", "IDR", 0, "id")
	en := f.PropertyData(t, f.tenantID, "KUTA", "IDR", 0, "en")
	name := func(sql string, args ...any) string {
		var n string
		must(t, f.Pool.QueryRow(context.Background(), sql, args...).Scan(&n))
		return n
	}
	for code, want := range map[string]string{
		"1110": "Kas di front office", "1155": "Akun penampung QRIS", "1220": "Piutang perusahaan (city ledger)", "1210": "Piutang tamu menginap (guest ledger)", "2310": "Deposit tamu (advance deposit)",
		"2410": "Utang PBJT (pajak hotel dan restoran)", "2420": "Utang PPN", "2440": "Utang PPh dipotong/dipungut", "2450": "Utang PPh badan", "3200": "Laba ditahan (tahun sebelumnya)",
		"4110": "Pendapatan kamar – individu", "4510": "Pendapatan no-show dan pembatalan", "4590": "Pendapatan lainnya", "6180": "Biaya izin dan lisensi", "6400": "Teknik dan pemeliharaan (engineering)",
		"7100": "Beban sewa", "7900": "Beban pajak penghasilan", "7910": "Beban pajak kini", "7920": "Beban pajak tangguhan", "2220": "Utang imbalan kerja",
	} {
		if got := name(`SELECT name FROM gl_accounts WHERE property_id = $1 AND code = $2`, id.ID, code); got != want {
			t.Errorf("account %s in Indonesian: %q, want %q", code, got, want)
		}
	}
	if got := name(`SELECT name FROM gl_accounts WHERE property_id = $1 AND code = '1110'`, en.ID); got != "Cash on hand - front desk" {
		t.Errorf("the English property keeps the standard name: %q", got)
	}
	for code, want := range map[string]string{"ROOMS": "Kamar", "FB": "Makanan dan minuman", "AG": "Administrasi dan umum", "POM": "Teknik dan pemeliharaan (engineering)", "UTIL": "Utilitas"} {
		if got := name(`SELECT name FROM departments WHERE property_id = $1 AND code = $2`, id.ID, code); got != want {
			t.Errorf("department %s in Indonesian: %q, want %q", code, got, want)
		}
	}
	for code, want := range map[string]string{"ROOM": "Biaya kamar", "ROOM_EXEMPT": "Biaya kamar (tanpa pajak dan service charge)", "BREAKFAST": "Sarapan", "NO_SHOW_FEE": "Biaya no-show", "CANCEL_FEE": "Biaya pembatalan", "OTHER": "Biaya lain-lain"} {
		if got := name(`SELECT name FROM charge_codes WHERE property_id = $1 AND code = $2`, id.ID, code); got != want {
			t.Errorf("charge code %s in Indonesian: %q, want %q", code, got, want)
		}
		if got := name(`SELECT name FROM charge_codes WHERE property_id = $1 AND code = $2`, en.ID, code); got == want {
			t.Errorf("charge code %s of the English property is in Indonesian", code)
		}
	}
	// the bed types and the document prefixes are not translated
	if got := name(`SELECT name FROM bed_types WHERE property_id = $1 AND code = 'KING'`, id.ID); got != "King" {
		t.Errorf("bed type: %q", got)
	}
	if got := name(`SELECT prefix FROM document_sequences WHERE property_id = $1 AND sequence_type = 'FOLIO'`, id.ID); got != "FOL" {
		t.Errorf("document prefix: %q", got)
	}
}

// Everything but the name is the same in the two languages: the structure of the chart (parent, type, side, group, whether it takes postings), the system map, the revenue account of every
// charge code, the department of every charge code, the order of the departments.
func TestTheTwoLanguagesDifferOnlyInTheNames(t *testing.T) {
	f := setup(t)
	id := f.PropertyData(t, f.tenantID, "UBUD", "IDR", 0, "id")
	en := f.PropertyData(t, f.tenantID, "KUTA", "IDR", 0, "en")
	dump := func(query string, propID int64) []string {
		rows, err := f.Pool.Query(context.Background(), query, propID)
		must(t, err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			must(t, rows.Scan(&s))
			out = append(out, s)
		}
		must(t, rows.Err())
		sort.Strings(out)
		return out
	}
	for what, q := range map[string]string{
		"the chart": `SELECT concat_ws('|', a.code, a.account_type, a.normal_side, a.is_postable, coalesce(a.statement_group, ''), coalesce(p.code, ''), a.is_active, a.department_requirement)
		                FROM gl_accounts a LEFT JOIN gl_accounts p ON p.id = a.parent_id WHERE a.property_id = $1`,
		"the system map":   `SELECT concat_ws('|', m.map_key, a.code) FROM gl_account_map m JOIN gl_accounts a ON a.id = m.account_id WHERE m.property_id = $1`,
		"the departments":  `SELECT concat_ws('|', d.code, d.sort_order, d.is_active) FROM departments d WHERE d.property_id = $1`,
		"the charge codes": `SELECT concat_ws('|', c.code, c.charge_type, c.price_mode, c.is_system, coalesce(c.gl_account_code, ''), coalesce(d.code, '')) FROM charge_codes c LEFT JOIN departments d ON d.id = c.department_id WHERE c.property_id = $1`,
	} {
		a, b := dump(q, id.ID), dump(q, en.ID)
		if len(a) == 0 || strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Errorf("%s differ between the languages (%d and %d rows)", what, len(a), len(b))
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM gl_accounts WHERE property_id = $1`, id.ID); n != 201 {
		t.Fatalf("the chart has %d accounts", n)
	}
}

// A property that exists is not touched when another is made in another language, and what a person changed is never overwritten, not even by the seed called again.
func TestTheSeedNeverTouchesWhatExistsOrWhatWasChanged(t *testing.T) {
	f := setup(t) // BALI, English
	if f.names(t, "usali_chart_names", "en")["1110"] != f.accountName(t, f.propID, "1110") {
		t.Fatal("the fixture property starts in English")
	}
	must(t, f.Exec(t, `UPDATE gl_accounts SET name = 'Kas depan kustom' WHERE property_id = $1 AND code = '1110'`, f.propID))
	id := f.PropertyData(t, f.tenantID, "UBUD", "IDR", 0, "id")
	if got := f.accountName(t, f.propID, "1110"); got != "Kas depan kustom" {
		t.Errorf("the account of the property that existed: %q", got)
	}
	if got := f.accountName(t, f.propID, "1120"); got != "Cash on hand - petty cash" {
		t.Errorf("the property that existed stays in English: %q", got)
	}
	// a person changed a name of the new property, and the seed is called again for it in Indonesian: that name stays, the rest does not change
	must(t, f.Exec(t, `UPDATE gl_accounts SET name = 'Kas laci depan' WHERE property_id = $1 AND code = '1110'`, id.ID))
	must(t, f.Exec(t, `UPDATE charge_codes SET name = 'Kamar menginap' WHERE property_id = $1 AND code = 'ROOM'`, id.ID))
	var created int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT seed_chart_of_accounts($1, $2, NULL, DATE '2026-09-30', 'id')`, f.tenantID, id.ID).Scan(&created))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT seed_charge_codes($1, $2, NULL, 'id')`, f.tenantID, id.ID).Scan(&created))
	if created != 0 {
		t.Errorf("the seed called again created %d", created)
	}
	if got := f.accountName(t, id.ID, "1110"); got != "Kas laci depan" {
		t.Errorf("a name that a person changed is overwritten: %q", got)
	}
	var room string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT name FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, id.ID).Scan(&room))
	if room != "Kamar menginap" {
		t.Errorf("a charge code that a person renamed is overwritten: %q", room)
	}
	if got := f.accountName(t, id.ID, "1120"); got != "Kas kecil" {
		t.Errorf("the other accounts stay: %q", got)
	}
}

func (f *fx) accountName(t *testing.T, propID int64, code string) string {
	t.Helper()
	var n string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT name FROM gl_accounts WHERE property_id = $1 AND code = $2`, propID, code).Scan(&n))
	return n
}

// The language is chosen when the property is created: "en" when none is given, "id" for Indonesian, anything else is refused (422 on the field).
func TestTheLanguageOfTheDataIsChosenWhenThePropertyIsCreated(t *testing.T) {
	f := setup(t)
	def := f.Property(t, f.tenantID, "KUTA") // none given
	if got := f.accountName(t, def.ID, "1110"); got != "Cash on hand - front desk" {
		t.Errorf("the default is English: %q", got)
	}
	_, err := f.Tenancy.CreateProperty(roomstest.Admin(f.tenantID), tenancy.CreatePropertyInput{
		Code: "FRNC", DataLanguage: "fr",
		Settings:            tenancy.PropertySettings{Name: "Hotel", Timezone: "Asia/Jakarta", CurrencyCode: "IDR", CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"), NightAuditEarliestTime: civil.MustParseTimeOfDay("20:00"), RefundMethods: []string{"CASH"}},
		OpeningBusinessDate: roomstest.BD,
	})
	e := roomstest.Code(t, err, "VALIDATION_FAILED")
	var field string
	for _, fe := range e.Fields {
		if fe.Field == "data_language" && fe.Code == "INVALID_VALUE" {
			field = fe.Field
		}
	}
	if field == "" {
		t.Fatalf("the field data_language is named: %+v", e.Fields)
	}
	if n := f.Count(t, `SELECT count(*) FROM properties WHERE code = 'FRNC'`); n != 0 {
		t.Fatal("a refused property is not created")
	}
}

// QRIS is received into an account of its own, beside the card (1150) and the e-wallet (1160) clearing accounts: a new property has it, in the language of its data, and the system key points at it.
func TestANewPropertyHasTheQRISClearingAccountAndItsKey(t *testing.T) {
	f := setup(t)
	for _, c := range []struct{ code, lang, name string }{{"KUTA", "en", "QRIS clearing"}, {"UBUD", "id", "Akun penampung QRIS"}} {
		p := f.PropertyData(t, f.tenantID, c.code, "IDR", 0, c.lang)
		var code, name, typ, side, parent, group string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT a.code, a.name, a.account_type, a.normal_side, COALESCE(pa.code, ''), COALESCE(a.statement_group, '')
			FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id LEFT JOIN gl_accounts pa ON pa.property_id = a.property_id AND pa.id = a.parent_id
			WHERE m.property_id = $1 AND m.map_key = 'QRIS'`, p.ID).Scan(&code, &name, &typ, &side, &parent, &group))
		if code != "1155" || name != c.name || typ != "ASSET" || side != "DEBIT" || parent != "1100" || group != "CASH" {
			t.Errorf("%s: the key QRIS points at %s %q (%s, %s, under %s, %s)", c.lang, code, name, typ, side, parent, group)
		}
	}
}
