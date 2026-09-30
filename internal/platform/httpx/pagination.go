package httpx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
)

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// PageRequest is the parsed ?limit=&cursor= of a list endpoint.
type PageRequest struct {
	Limit  int
	Cursor string // opaque; decode with DecodeCursor
}

// Page is the envelope of every list response.
type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// ParsePage reads limit (default 50, max 200) and cursor from the query string.
func ParsePage(r *http.Request) (PageRequest, error) {
	q := r.URL.Query()
	p := PageRequest{Limit: DefaultPageLimit, Cursor: q.Get("cursor")}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxPageLimit {
			return PageRequest{}, apperr.Invalid("invalid pagination",
				apperr.FieldError{Field: "limit", Code: "OUT_OF_RANGE", Message: "must be between 1 and " + strconv.Itoa(MaxPageLimit)})
		}
		p.Limit = n
	}
	return p, nil
}

// EncodeCursor turns a keyset position (for example {"id": 123}) into an opaque token.
func EncodeCursor(position any) (string, error) {
	b, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor restores a keyset position produced by EncodeCursor.
func DecodeCursor(cursor string, position any) error {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err == nil {
		err = json.Unmarshal(b, position)
	}
	if err != nil {
		return apperr.BadRequest("INVALID_CURSOR", "the pagination cursor is invalid")
	}
	return nil
}
