package tenancy

import (
	"testing"
	"time"

	"kamarapms/internal/platform/civil"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// The worked example from docs/architecture/04-operations.md §13.2.
func TestEvaluateDayNightAuditGuard(t *testing.T) {
	jakarta := mustLoc(t, "Asia/Jakarta") // UTC+7
	earliest := civil.MustParseTimeOfDay("20:00")
	at := func(local string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", local, jakarta)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cases := []struct {
		name    string
		bd      string
		now     string
		allowed bool
		overdue bool
	}{
		{"same day, before earliest time", "2026-09-30", "2026-09-30 19:59", false, false},
		{"same day, at earliest time", "2026-09-30", "2026-09-30 20:00", true, false},
		{"same day, late evening", "2026-09-30", "2026-09-30 23:00", true, false},
		{"after midnight, audit pending", "2026-09-30", "2026-10-01 02:30", true, false},
		{"just audited at 02:30: next day cannot close yet", "2026-10-01", "2026-10-01 02:30", false, false},
		{"missed a whole day", "2026-09-30", "2026-10-02 09:00", true, true},
		{"business date in the future (clock skew)", "2026-10-02", "2026-10-01 23:00", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluateDay(civil.MustParseDate(c.bd), at(c.now), jakarta, earliest)
			if got.NightAuditAllowed != c.allowed || got.NightAuditOverdue != c.overdue {
				t.Fatalf("allowed=%v overdue=%v, want %v %v", got.NightAuditAllowed, got.NightAuditOverdue, c.allowed, c.overdue)
			}
		})
	}
}

func TestEvaluateDayReportsBothClocks(t *testing.T) {
	jakarta := mustLoc(t, "Asia/Jakarta")
	now := time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC) // 02:30 on 1 Oct in Jakarta
	got := EvaluateDay(civil.MustParseDate("2026-09-30"), now, jakarta, civil.MustParseTimeOfDay("20:00"))
	if got.BusinessDate.String() != "2026-09-30" || got.PropertyLocalTime != "2026-10-01T02:30:00+07:00" ||
		!got.ServerTime.Equal(now) || got.Timezone != "Asia/Jakarta" ||
		!got.NightAuditAllowedFrom.Equal(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateOpeningDate(t *testing.T) {
	jakarta := mustLoc(t, "Asia/Jakarta")
	now := time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC) // local 1 Oct 02:30
	for date, ok := range map[string]bool{"2026-10-01": true, "2026-09-30": true, "2026-09-29": false, "2026-10-02": false} {
		if got := ValidateOpeningDate(civil.MustParseDate(date), now, jakarta) == nil; got != ok {
			t.Errorf("%s: ok=%v, want %v", date, got, ok)
		}
	}
}

func TestPropertySettingsValidate(t *testing.T) {
	valid := PropertySettings{
		Name: "Hotel Bali", Timezone: "Asia/Makassar", CurrencyCode: "IDR", CurrencyDecimals: 0,
		CountryCode: "ID", CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"),
		RefundMethods: []string{"CASH"},
	}
	if errs := valid.Validate(); len(errs) != 0 {
		t.Fatalf("valid settings rejected: %+v", errs)
	}

	bad := PropertySettings{Name: " ", Timezone: "Local", CurrencyCode: "rupiah", CurrencyDecimals: 5, CountryCode: "IDN"}
	bad.Normalize()
	fields := map[string]bool{}
	for _, e := range bad.Validate() {
		fields[e.Field] = true
	}
	for _, f := range []string{"name", "timezone", "currency_code", "currency_decimals", "country_code"} {
		if !fields[f] {
			t.Errorf("expected an error for %s", f)
		}
	}
}

func TestValidateTimezone(t *testing.T) {
	for _, ok := range []string{"Asia/Jakarta", "Asia/Makassar", "Europe/Amsterdam", "UTC"} {
		if ValidateTimezone(ok) != nil {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "Local", "WIB", "GMT+7", "Asia/Atlantis"} {
		if ValidateTimezone(bad) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestNormalizeAndValidateCode(t *testing.T) {
	if NormalizeCode("  bali-1 ") != "BALI-1" {
		t.Fatal("normalize")
	}
	if validateCode("code", "BALI-1") != nil || validateCode("code", "-BALI") == nil || validateCode("code", "HOTEL BALI") == nil {
		t.Fatal("validate")
	}
}
