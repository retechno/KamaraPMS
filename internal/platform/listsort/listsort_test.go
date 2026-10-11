package listsort

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

var spec = Spec{Columns: map[string]Kind{"name": Text, "balance": Num, "arrival": Date}, DefaultKind: Num}

func req(query string) *http.Request { return httptest.NewRequest(http.MethodGet, "/x?"+query, nil) }

func wantField(t *testing.T, err error, field string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_FAILED" {
		t.Fatalf("want VALIDATION_FAILED, got %v", err)
	}
	for _, f := range ae.Fields {
		if f.Field == field {
			return
		}
	}
	t.Fatalf("no error on field %q: %+v", field, ae.Fields)
}

func wantInvalidCursor(t *testing.T, err error) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "INVALID_CURSOR" {
		t.Fatalf("want INVALID_CURSOR, got %v", err)
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		query string
		want  Sort
	}{
		{"", Sort{}},
		{"sort=name", Sort{Key: "name"}},
		{"sort=name&order=asc", Sort{Key: "name"}},
		{"sort=balance&order=desc", Sort{Key: "balance", Desc: true}},
	} {
		got, err := spec.Parse(req(c.query))
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.query, got, err, c.want)
		}
	}
	if got, _ := (Spec{Columns: spec.Columns, DefaultDesc: true}).Parse(req("")); !got.Desc || got.Key != "" {
		t.Errorf("the default order carries its direction: %+v", got)
	}
}

func TestParseRefusesWhatIsNotAllowed(t *testing.T) {
	_, err := spec.Parse(req("sort=password"))
	wantField(t, err, "sort")
	_, err = spec.Parse(req("sort=name%3B+drop+table&order=asc"))
	wantField(t, err, "sort")
	_, err = spec.Parse(req("sort=name&order=sideways"))
	wantField(t, err, "order")
	_, err = spec.Parse(req("order=desc")) // an order that says nothing
	wantField(t, err, "order")
}

func TestCursorIsBoundToItsOrder(t *testing.T) {
	v := "Siti"
	asc := Sort{Key: "name"}
	tok, err := Encode(asc, Position{Value: &v, ID: 42, Ref: "2026-10-10"})
	if err != nil {
		t.Fatal(err)
	}
	pos, err := Decode(tok, asc)
	if err != nil || pos == nil || *pos.Value != "Siti" || pos.ID != 42 || pos.Ref != "2026-10-10" {
		t.Fatalf("round trip: %+v, %v", pos, err)
	}
	for _, other := range []Sort{{Key: "name", Desc: true}, {Key: "balance"}, {}} {
		_, err := Decode(tok, other)
		wantInvalidCursor(t, err)
	}
	if pos, err := Decode("", asc); pos != nil || err != nil {
		t.Errorf("no cursor is the first page: %+v, %v", pos, err)
	}
	for _, bad := range []string{"%%%", "e30", "bm90LWpzb24"} { // not base64, an empty object (no id), not JSON
		_, err := Decode(bad, asc)
		wantInvalidCursor(t, err)
	}
}

func TestAfterIsTypedAndCarriesNull(t *testing.T) {
	text := "Siti"
	if a, err := AfterOf(&Position{Value: &text, ID: 5}, Text); err != nil || !a.Has || a.Null || *a.Text != "Siti" || *a.ID != 5 {
		t.Errorf("text: %+v %v", a, err)
	}
	num := "-12.500"
	a, err := AfterOf(&Position{Value: &num, ID: 5}, Num)
	if err != nil || !a.Num.Equal(decimal.RequireFromString("-12.5")) {
		t.Errorf("num: %+v %v", a, err)
	}
	day := "2026-10-10"
	if a, err := AfterOf(&Position{Value: &day, ID: 5}, Date); err != nil || a.Date.String() != "2026-10-10" {
		t.Errorf("date: %+v %v", a, err)
	}
	if a, err := AfterOf(&Position{ID: 9}, Text); err != nil || !a.Has || !a.Null || a.Text != nil {
		t.Errorf("null: %+v %v", a, err)
	}
	if a, err := AfterOf(nil, Text); err != nil || a.Has {
		t.Errorf("none: %+v %v", a, err)
	}
	_, err = AfterOf(&Position{Value: &text, ID: 1}, Num) // a text where a number is expected is a cursor that is not ours
	wantInvalidCursor(t, err)
	_, err = AfterOf(&Position{Value: &text, ID: 1}, Date)
	wantInvalidCursor(t, err)
}

func TestValueHelpers(t *testing.T) {
	if NumValue(nil) != nil || DateValue(nil) != nil {
		t.Error("NULL stays NULL")
	}
	d := decimal.RequireFromString("10.50")
	day := civil.NewDate(2026, 10, 10)
	if *NumValue(&d) != "10.5" || *DateValue(&day) != "2026-10-10" {
		t.Errorf("%s %s", *NumValue(&d), *DateValue(&day))
	}
}
