package app

import (
	"net/http"
	"testing"
)

// The incidental deposit proposed at check-in is a setting of the property: zero by default, an amount at the currency decimals, never negative, and another tenant cannot read or change it.
func TestCheckinIncidentalDepositSettingAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	created := abc.do(http.MethodPost, "/api/v1/properties", validProperty())
	bali := idOf(created)
	base := "/api/v1/properties/" + bali
	if created.body["checkin_incidental_deposit"] != "0" {
		t.Fatalf("nothing is proposed by default: %v", created.body["checkin_incidental_deposit"])
	}

	if r := abc.do(http.MethodPatch, base, map[string]any{"checkin_incidental_deposit": "250000"}); r.status != 200 || r.body["checkin_incidental_deposit"] != "250000" {
		t.Fatalf("set: %d %v", r.status, r.body)
	}
	if got := abc.do(http.MethodGet, base, nil).body["checkin_incidental_deposit"]; got != "250000" {
		t.Fatalf("read back: %v", got)
	}
	// the other settings are left alone by a patch that does not carry it
	if r := abc.do(http.MethodPatch, base, map[string]any{"city": "Ubud"}); r.status != 200 || r.body["checkin_incidental_deposit"] != "250000" {
		t.Fatalf("kept: %d %v", r.status, r.body)
	}

	for name, v := range map[string]string{"negative": "-1", "a fraction of a rupiah": "100.5", "not an amount": "abc", "an exponent": "1e3"} {
		r := abc.do(http.MethodPatch, base, map[string]any{"checkin_incidental_deposit": v})
		if r.status != http.StatusUnprocessableEntity || fieldsOf(r)["checkin_incidental_deposit"] == "" {
			t.Fatalf("%s: %d %v", name, r.status, r.body)
		}
	}
	if got := abc.do(http.MethodGet, base, nil).body["checkin_incidental_deposit"]; got != "250000" {
		t.Fatalf("a refused change leaves the setting: %v", got)
	}

	// it can be given when the property is created
	p := validProperty()
	p["code"], p["checkin_incidental_deposit"] = "UBUD", "100000"
	if r := abc.do(http.MethodPost, "/api/v1/properties", p); r.status != http.StatusCreated || r.body["checkin_incidental_deposit"] != "100000" {
		t.Fatalf("create with it: %d %v", r.status, r.body)
	}

	// another tenant neither reads nor changes it
	if r := xyz.do(http.MethodPatch, base, map[string]any{"checkin_incidental_deposit": "1"}); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("from another tenant: %d %v", r.status, r.body)
	}
	if got := abc.do(http.MethodGet, base, nil).body["checkin_incidental_deposit"]; got != "250000" {
		t.Fatalf("untouched: %v", got)
	}
}
