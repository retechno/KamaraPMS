// Package listsort is the sort of a list that is read a page at a time: the `sort` and `order` the client asks for, checked against the columns a list allows, and the cursor of the next
// page, which holds the sort value of the last row (and its id as the tie-break) so that a page continues exactly where the last one ended, in the order that was asked for.
//
// A cursor belongs to one order: a cursor made for another sort, or for the other direction, is refused (400 INVALID_CURSOR), never read as a position in the wrong order. NULL sorts last in
// both directions (the same as the tables on the screen). The SQL itself stays in each module's queries.sql: a list asks for its rows with the sort key, the direction and the position as parameters.
package listsort

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Kind is the type of a sort value, which decides how the cursor carries it.
type Kind string

const (
	Text Kind = "text"
	Num  Kind = "num"
	Date Kind = "date"
)

// Spec is what a list allows: its columns (name and the kind of its sort value) and the order when the client asks for none. The default order is the empty key: the list's own
// (an order that is made of several columns); `DefaultKind` is the kind of its sort value, and `DefaultDesc` its direction when a cursor is made for it.
type Spec struct {
	Columns     map[string]Kind
	DefaultKind Kind
	DefaultDesc bool
}

// Sort is the order of a list: a column and a direction. The zero Sort is the default order of the list.
type Sort struct {
	Key  string
	Desc bool
}

// Dir is "asc" or "desc".
func (s Sort) Dir() string {
	if s.Desc {
		return "desc"
	}
	return "asc"
}

// names are the allowed columns, in a stable order.
func (sp Spec) names() []string {
	out := make([]string, 0, len(sp.Columns))
	for k := range sp.Columns {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Kind of the sort value of s.
func (sp Spec) Kind(s Sort) Kind {
	if s.Key == "" {
		return sp.DefaultKind
	}
	return sp.Columns[s.Key]
}

// Parse reads ?sort= and ?order= of a request. No sort is the list's default order (and an order without a sort is refused: it would say nothing). An unknown column or direction is
// 422 VALIDATION_FAILED with the field named. The direction is "asc" when only the sort is given.
func (sp Spec) Parse(r *http.Request) (Sort, error) {
	q := r.URL.Query()
	key, order := q.Get("sort"), q.Get("order")
	var errs []apperr.FieldError
	if key == "" {
		if order != "" {
			errs = append(errs, apperr.FieldError{Field: "order", Code: "INVALID_VALUE", Message: "order needs a sort"})
		}
		if len(errs) > 0 {
			return Sort{}, apperr.Invalid("the sort is invalid", errs...)
		}
		return Sort{Desc: sp.DefaultDesc}, nil
	}
	if _, ok := sp.Columns[key]; !ok {
		errs = append(errs, apperr.FieldError{Field: "sort", Code: "INVALID_VALUE", Message: "one of: " + strings.Join(sp.names(), ", ")})
	}
	if order != "" && order != "asc" && order != "desc" {
		errs = append(errs, apperr.FieldError{Field: "order", Code: "INVALID_VALUE", Message: "asc or desc"})
	}
	if len(errs) > 0 {
		return Sort{}, apperr.Invalid("the sort is invalid", errs...)
	}
	return Sort{Key: key, Desc: order == "desc"}, nil
}

// Position is where a page ended: the sort value of its last row (nil when that value is NULL), the id of the row (the tie-break, which goes the way of the order) and, for an order that
// is relative to something (the reservations "nearest first" are relative to a date), what it was relative to, so that the next page counts from the same point.
type Position struct {
	Value *string `json:"v"`
	ID    int64   `json:"i"`
	Ref   string  `json:"x,omitempty"`
}

type token struct {
	Key  string   `json:"s"`
	Desc bool     `json:"d"`
	Pos  Position `json:"p"`
}

// Encode makes the cursor of the page that ends at pos, in the order s.
func Encode(s Sort, pos Position) (string, error) {
	b, err := json.Marshal(token{Key: s.Key, Desc: s.Desc, Pos: pos})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func invalidCursor() error {
	return apperr.BadRequest("INVALID_CURSOR", "the pagination cursor is invalid")
}

// Decode reads a cursor for the order s (nil for no cursor: the first page). A cursor that is not one, or that was made for another sort or the other direction, is 400 INVALID_CURSOR.
func Decode(cursor string, s Sort) (*Position, error) {
	if cursor == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, invalidCursor()
	}
	var t token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, invalidCursor()
	}
	if t.Key != s.Key || t.Desc != s.Desc || t.Pos.ID < 1 {
		return nil, invalidCursor()
	}
	return &t.Pos, nil
}

// After is a position as the queries ask for it: whether there is one, whether its sort value is NULL, and the value in the type of the column (only the one of the sort's kind is set).
type After struct {
	Has  bool
	Null bool
	Text *string
	Num  *decimal.Decimal
	Date *civil.Date
	ID   *int64
}

// AfterOf turns the position of a cursor (nil: the first page) into the parameters of a query, for a sort value of the kind k.
func AfterOf(p *Position, k Kind) (After, error) {
	if p == nil {
		return After{}, nil
	}
	id := p.ID
	a := After{Has: true, ID: &id}
	if p.Value == nil {
		a.Null = true
		return a, nil
	}
	switch k {
	case Text:
		v := *p.Value
		a.Text = &v
	case Num:
		d, err := decimal.NewFromString(*p.Value)
		if err != nil {
			return After{}, invalidCursor()
		}
		a.Num = &d
	case Date:
		d, err := civil.ParseDate(*p.Value)
		if err != nil {
			return After{}, invalidCursor()
		}
		a.Date = &d
	default:
		return After{}, invalidCursor()
	}
	return a, nil
}

// The sort value of a row as the cursor carries it (nil for NULL): by the kind of the sort.

// NumValue is the cursor value of a numeric sort value.
func NumValue(v *decimal.Decimal) *string {
	if v == nil {
		return nil
	}
	s := v.String()
	return &s
}

// DateValue is the cursor value of a date sort value.
func DateValue(v *civil.Date) *string {
	if v == nil {
		return nil
	}
	s := v.String()
	return &s
}
