package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kamarapms/internal/platform/apperr"
)

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != ProblemContentType {
		t.Fatalf("content type %q", ct)
	}
	var p Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteErrorRendersAppError(t *testing.T) {
	h := HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return apperr.Conflict("ROOM_NOT_AVAILABLE", "room 201 is not available").
			WithContext("nights", []string{"2026-10-01"}).
			WithCause(errors.New("pg: exclusion violation on reservation_rooms_no_overlap_ex"))
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	p := decodeProblem(t, rec)
	if p.Code != "ROOM_NOT_AVAILABLE" || p.Status != 409 || p.Type != "urn:kamarapms:problem:ROOM_NOT_AVAILABLE" ||
		p.Detail != "room 201 is not available" || p.Context["nights"] == nil {
		t.Fatalf("unexpected problem: %+v", p)
	}
	if strings.Contains(rec.Body.String(), "reservation_rooms_no_overlap_ex") {
		t.Fatal("the cause (schema detail) leaked to the client")
	}
}

func TestWriteErrorHidesInternalErrors(t *testing.T) {
	h := HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("dial tcp 10.0.0.5:5432: password authentication failed")
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	p := decodeProblem(t, rec)
	if rec.Code != 500 || p.Code != "INTERNAL" || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("internal error leaked or mis-rendered: %s", rec.Body.String())
	}
}

func TestWriteErrorBusyIsRetryable(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), apperr.Busy("RESOURCE_BUSY", "retry"))
	if p := decodeProblem(t, rec); !p.Retryable || rec.Code != 409 {
		t.Fatalf("got %+v", p)
	}
}

func TestDecodeJSON(t *testing.T) {
	type req struct {
		Amount string `json:"amount"`
		Count  int    `json:"count"`
	}
	cases := []struct {
		name, body, contentType, code string
		kind                          apperr.Kind
	}{
		{"valid", `{"amount":"100.00","count":2}`, "application/json", "", 0},
		{"unknown field", `{"amount":"1","amount_typo":"2"}`, "application/json", "VALIDATION_FAILED", apperr.KindInvalid},
		{"wrong type", `{"count":"two"}`, "application/json", "VALIDATION_FAILED", apperr.KindInvalid},
		{"syntax", `{"amount":`, "application/json", "INVALID_JSON", apperr.KindBadRequest},
		{"empty", ``, "application/json", "EMPTY_BODY", apperr.KindBadRequest},
		{"two objects", `{"count":1}{"count":2}`, "application/json", "INVALID_JSON", apperr.KindBadRequest},
		{"media type", `{"count":1}`, "text/plain", "UNSUPPORTED_MEDIA_TYPE", apperr.KindBadRequest},
		{"too large", `{"amount":"` + strings.Repeat("9", MaxBodyBytes) + `"}`, "application/json", "BODY_TOO_LARGE", apperr.KindBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(c.body))
			r.Header.Set("Content-Type", c.contentType)
			var dst req
			err := DecodeJSON(httptest.NewRecorder(), r, &dst)
			if c.code == "" {
				if err != nil || dst.Amount != "100.00" || dst.Count != 2 {
					t.Fatalf("got %v %+v", err, dst)
				}
				return
			}
			e, ok := apperr.As(err)
			if !ok || e.Code != c.code || e.Kind != c.kind {
				t.Fatalf("got %v, want %s", err, c.code)
			}
		})
	}
}

func TestUnknownFieldIsNamed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"amount_typo":"1"}`))
	var dst struct {
		Amount string `json:"amount"`
	}
	e, _ := apperr.As(DecodeJSON(httptest.NewRecorder(), r, &dst))
	if e == nil || len(e.Fields) != 1 || e.Fields[0].Field != "amount_typo" || e.Fields[0].Code != "UNKNOWN_FIELD" {
		t.Fatalf("got %+v", e)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	var seen string
	h := RequestID(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(RequestIDHeader, "abc-123")
	h.ServeHTTP(rec, r)
	if seen != "abc-123" || rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("client id not propagated: %q", seen)
	}

	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(RequestIDHeader, "bad id with spaces\n")
	h.ServeHTTP(rec, r)
	if seen == "" || strings.Contains(seen, " ") || rec.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("invalid client id should be replaced, got %q", seen)
	}
}

func TestRecoverAndAccessLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }),
		RequestID(logger), AccessLog, Recover)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	p := decodeProblem(t, rec)
	if rec.Code != 500 || p.Code != "INTERNAL" || p.RequestID == "" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
	out := logs.String()
	if !strings.Contains(out, `"msg":"panic in handler"`) || !strings.Contains(out, `"status":500`) ||
		!strings.Contains(out, `"request_id":"`+p.RequestID+`"`) {
		t.Fatalf("logs missing panic/access entries:\n%s", out)
	}
}

func TestParsePage(t *testing.T) {
	p, err := ParsePage(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil || p.Limit != DefaultPageLimit {
		t.Fatalf("default: %+v %v", p, err)
	}
	p, err = ParsePage(httptest.NewRequest(http.MethodGet, "/x?limit=10&cursor=abc", nil))
	if err != nil || p.Limit != 10 || p.Cursor != "abc" {
		t.Fatalf("explicit: %+v %v", p, err)
	}
	for _, bad := range []string{"0", "201", "x", "-1"} {
		if _, err := ParsePage(httptest.NewRequest(http.MethodGet, "/x?limit="+bad, nil)); err == nil {
			t.Errorf("limit=%s should be rejected", bad)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	type pos struct {
		ArrivalDate string `json:"a"`
		ID          int64  `json:"id"`
	}
	c, err := EncodeCursor(pos{"2026-10-01", 42})
	if err != nil {
		t.Fatal(err)
	}
	var got pos
	if err := DecodeCursor(c, &got); err != nil || got.ID != 42 || got.ArrivalDate != "2026-10-01" {
		t.Fatalf("got %+v %v", got, err)
	}
	if e, _ := apperr.As(DecodeCursor("%%%", &got)); e == nil || e.Code != "INVALID_CURSOR" {
		t.Fatal("garbage cursor must be rejected")
	}
}
