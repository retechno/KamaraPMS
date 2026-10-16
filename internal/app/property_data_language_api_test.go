package app

import (
	"net/http"
	"testing"
)

// namesOf reads the code -> name pairs of a list of the API (accounts, departments, charge codes).
func namesOf(c *client, path string) map[string]string {
	c.env.t.Helper()
	r := c.do(http.MethodGet, path, nil)
	if r.status != http.StatusOK {
		c.env.t.Fatalf("%s: %d %v", path, r.status, r.body)
	}
	out := map[string]string{}
	for _, row := range r.body["data"].([]any) {
		m := row.(map[string]any)
		out[m["code"].(string)] = m["name"].(string)
	}
	return out
}

// The language of the data is chosen when the property is created, through the API: Indonesian names for the chart of accounts, the departments and the charge codes; English when none is given;
// anything else is refused with the field named.
func TestThePropertyAPITakesTheLanguageOfTheSeededData(t *testing.T) {
	e := newAPI(t)
	c := e.login("ABC")

	p := validProperty()
	p["data_language"] = "id"
	created := c.do(http.MethodPost, "/api/v1/properties", p)
	if created.status != http.StatusCreated {
		t.Fatalf("create in Indonesian: %d %v", created.status, created.body)
	}
	base := "/api/v1/properties/" + idOf(created)
	accounts := namesOf(c, base+"/accounting/accounts?limit=500")
	if len(accounts) != 207 || accounts["4591"] != "Pembulatan" || accounts["6196"] != "Komplimen dan pemakaian internal" || accounts["1215"] != "Transfer antar folio" || accounts["1155"] != "Akun penampung QRIS" || accounts["1110"] != "Kas di front office" || accounts["2410"] != "Utang PBJT (pajak hotel dan restoran)" {
		t.Fatalf("the chart in Indonesian: %d accounts, 1110 %q, 2410 %q", len(accounts), accounts["1110"], accounts["2410"])
	}
	if d := namesOf(c, base+"/departments"); d["ROOMS"] != "Kamar" || d["POM"] != "Teknik dan pemeliharaan (engineering)" || len(d) != 8 {
		t.Fatalf("the departments in Indonesian: %v", d)
	}
	if cc := namesOf(c, base+"/charge-codes?limit=200"); cc["ROOM"] != "Biaya kamar" || cc["NO_SHOW_FEE"] != "Biaya no-show" || cc["POS_ROUNDING"] != "Pembulatan POS" {
		t.Fatalf("the charge codes in Indonesian: %v", cc)
	}

	// none given: English, as before
	en := validProperty()
	en["code"] = "KUTA"
	created = c.do(http.MethodPost, "/api/v1/properties", en)
	if created.status != http.StatusCreated {
		t.Fatalf("create: %d %v", created.status, created.body)
	}
	base = "/api/v1/properties/" + idOf(created)
	if a := namesOf(c, base+"/accounting/accounts?limit=500"); a["1110"] != "Cash on hand - front desk" {
		t.Fatalf("the default is English: %q", a["1110"])
	}
	if cc := namesOf(c, base+"/charge-codes?limit=200"); cc["ROOM"] != "Room charge" {
		t.Fatalf("the charge codes in English: %v", cc)
	}

	// a language that has no names is refused, and nothing is created
	bad := validProperty()
	bad["code"] = "FRNC"
	bad["data_language"] = "fr"
	r := c.do(http.MethodPost, "/api/v1/properties", bad)
	if f := fieldsOf(r); r.status != http.StatusUnprocessableEntity || f["data_language"] != "INVALID_VALUE" {
		t.Fatalf("a language without names: %d %v", r.status, r.body)
	}
	if l := c.do(http.MethodGet, "/api/v1/properties", nil); len(l.body["data"].([]any)) != 2 {
		t.Fatalf("the refused property was created: %v", l.body)
	}
}
