package guests

import (
	"testing"

	"kamarapms/internal/platform/civil"
)

func TestSearchTokensEscapeAndLimit(t *testing.T) {
	got := SearchTokens("  John  50%_off\\x ")
	want := []string{"john", `50\%\_off\\x`}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q want %q", got, want)
	}
	if n := len(SearchTokens("a b c d e f g h")); n != 6 {
		t.Fatalf("tokens capped at 6, got %d", n)
	}
	if SearchTokens("   ") != nil {
		t.Fatal("blank query has no tokens")
	}
}

func TestPhoneDigitsAndDuplicateReasons(t *testing.T) {
	if PhoneDigits("+62 812-3456 (0)") != "6281234560" {
		t.Fatal(PhoneDigits("+62 812-3456 (0)"))
	}
	dob := civil.MustParseDate("1990-05-01")
	p := Profile{FirstName: "Ann", LastName: "Lee", Email: "Ann@X.com", Phone: "0812 3456", IDType: "PASSPORT", IDNumber: "A1", DateOfBirth: &dob}
	c := Guest{FirstName: "ann", LastName: "LEE", Email: "ann@x.com", Phone: "0812-3456", IDType: "PASSPORT", IDNumber: "A1", DateOfBirth: &dob}
	if r := DuplicateReasons(p, c); len(r) != 4 {
		t.Fatalf("all four reasons expected, got %v", r)
	}
	if r := DuplicateReasons(p, Guest{LastName: "Other"}); len(r) != 0 {
		t.Fatalf("unrelated guest: %v", r)
	}
	// Same document number with another document type is not the same document.
	if r := DuplicateReasons(Profile{IDType: "KTP", IDNumber: "A1"}, Guest{IDType: "PASSPORT", IDNumber: "A1"}); len(r) != 0 {
		t.Fatalf("different id type: %v", r)
	}
}

func TestProfileValidation(t *testing.T) {
	today := civil.MustParseDate("2026-09-30")
	future := civil.MustParseDate("2030-01-01")
	old := civil.MustParseDate("1850-01-01")
	bad := Profile{
		LastName: "", Email: "nope", Phone: "abc", Nationality: "IDN", Gender: "X", DateOfBirth: &future, IDType: "KTP",
	}
	got := map[string]bool{}
	for _, f := range bad.Validate(today) {
		got[f.Field] = true
	}
	for _, f := range []string{"last_name", "email", "phone", "nationality", "gender", "date_of_birth", "id_number"} {
		if !got[f] {
			t.Errorf("expected a field error for %s", f)
		}
	}
	if fe := (Profile{LastName: "X", DateOfBirth: &old}).Validate(today); len(fe) != 1 || fe[0].Field != "date_of_birth" {
		t.Fatalf("too old: %v", fe)
	}
	ok := Profile{LastName: "Lee", Email: "a@b.co", Phone: "+62 812-3456-789", Nationality: "ID", Gender: "MALE", IDType: "KTP", IDNumber: "123"}
	if fe := ok.Validate(today); len(fe) != 0 {
		t.Fatalf("valid profile rejected: %v", fe)
	}
}
