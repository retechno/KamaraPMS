package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
)

// MaxBodyBytes caps request bodies. PMS requests are small; anything larger is
// either a bug or abuse.
const MaxBodyBytes = 1 << 20 // 1 MiB

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// DecodeJSON strictly decodes a single JSON object into dst: the content type
// must be JSON, unknown fields are rejected (typos must not be silently
// ignored in financial requests), and trailing data is rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
			return apperr.BadRequest("UNSUPPORTED_MEDIA_TYPE", "the request body must be application/json")
		}
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperr.BadRequest("INVALID_JSON", "the request body must contain a single JSON object")
	}
	return nil
}

func decodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)
	switch {
	case errors.Is(err, io.EOF):
		return apperr.BadRequest("EMPTY_BODY", "the request body is empty")
	case errors.As(err, &maxErr):
		return apperr.BadRequest("BODY_TOO_LARGE", fmt.Sprintf("the request body exceeds %d bytes", MaxBodyBytes))
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return apperr.BadRequest("INVALID_JSON", "the request body is not valid JSON")
	case errors.As(err, &typeErr):
		return apperr.Invalid("the request body has a field of the wrong type",
			apperr.FieldError{Field: typeErr.Field, Code: "INVALID_TYPE", Message: "expected " + typeErr.Type.String()})
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return apperr.Invalid("the request body has an unknown field",
			apperr.FieldError{Field: field, Code: "UNKNOWN_FIELD"})
	default:
		return apperr.BadRequest("INVALID_JSON", "the request body could not be decoded")
	}
}
