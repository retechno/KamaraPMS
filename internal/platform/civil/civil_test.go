package civil

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseDate(t *testing.T) {
	d, err := ParseDate("2026-09-30")
	if err != nil || d.String() != "2026-09-30" || d.Year() != 2026 || d.Month() != time.September || d.Day() != 30 {
		t.Fatalf("got %v %v", d, err)
	}
	for _, bad := range []string{"", "2026-9-30", "30-09-2026", "2026-02-30", "2026-09-30T00:00:00Z", "2026/09/30"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) should fail", bad)
		}
	}
}

func TestDateArithmetic(t *testing.T) {
	d := MustParseDate("2026-09-30")
	if d.AddDays(1).String() != "2026-10-01" || d.AddDays(-30).String() != "2026-08-31" {
		t.Fatal("AddDays across month boundaries")
	}
	if !MustParseDate("2028-02-28").AddDays(1).Equal(MustParseDate("2028-02-29")) {
		t.Fatal("leap day")
	}
	if !d.Before(d.AddDays(1)) || !d.AddDays(1).After(d) || d.Compare(d) != 0 {
		t.Fatal("ordering")
	}
	if d.DaysUntil(MustParseDate("2026-10-03")) != 3 {
		t.Fatal("DaysUntil")
	}
}

// The calendar day depends on the zone the instant is viewed in; DateOf never guesses.
func TestDateOfDependsOnLocation(t *testing.T) {
	instant := time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC) // 02:30 on 1 Oct in Jakarta
	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	if DateOf(instant).String() != "2026-09-30" || DateOf(instant.In(jakarta)).String() != "2026-10-01" {
		t.Fatalf("utc=%s jakarta=%s", DateOf(instant), DateOf(instant.In(jakarta)))
	}
}

func TestDateJSON(t *testing.T) {
	type payload struct {
		BD  Date  `json:"business_date"`
		Opt *Date `json:"closed_date,omitempty"`
	}
	b, err := json.Marshal(payload{BD: MustParseDate("2026-09-30")})
	if err != nil || string(b) != `{"business_date":"2026-09-30"}` {
		t.Fatalf("got %s %v", b, err)
	}
	var p payload
	if err := json.Unmarshal([]byte(`{"business_date":"2026-10-01"}`), &p); err != nil || p.BD.String() != "2026-10-01" {
		t.Fatalf("got %v %v", p, err)
	}
	if err := json.Unmarshal([]byte(`{"business_date":"01/10/2026"}`), &p); err == nil {
		t.Fatal("non-ISO date must be rejected")
	}
	if _, err := json.Marshal(payload{}); err == nil {
		t.Fatal("zero Date must not serialise silently")
	}
}

func TestDatePgRoundTrip(t *testing.T) {
	d := MustParseDate("2026-09-30")
	v, err := d.DateValue()
	if err != nil || !v.Valid {
		t.Fatal(err)
	}
	var back Date
	if err := back.ScanDate(v); err != nil || !back.Equal(d) {
		t.Fatalf("got %v %v", back, err)
	}
	if err := back.ScanDate(pgtype.Date{}); err == nil {
		t.Fatal("NULL must not scan into Date")
	}
	if zero, _ := (Date{}).DateValue(); zero.Valid {
		t.Fatal("zero Date must be written as NULL")
	}
}

func TestTimeOfDay(t *testing.T) {
	tod := MustParseTimeOfDay("14:05")
	if tod.Hour() != 14 || tod.Minute() != 5 || tod.String() != "14:05" {
		t.Fatalf("got %v", tod)
	}
	for _, bad := range []string{"", "24:00", "9:00", "14:60", "14:05:00", "2pm"} {
		if _, err := ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) should fail", bad)
		}
	}
	if !MustParseTimeOfDay("12:00").Before(MustParseTimeOfDay("14:00")) {
		t.Fatal("ordering")
	}
	v, _ := tod.TimeValue()
	var back TimeOfDay
	if err := back.ScanTime(v); err != nil || back != tod {
		t.Fatalf("pg round trip: %v %v", back, err)
	}
	b, _ := json.Marshal(struct {
		T TimeOfDay `json:"t"`
	}{tod})
	if string(b) != `{"t":"14:05"}` {
		t.Fatalf("json: %s", b)
	}
}

func TestAtUsesPropertyZone(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	at := MustParseDate("2026-09-30").At(MustParseTimeOfDay("20:00"), jakarta)
	if !at.Equal(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v", at.UTC())
	}
}
