// Package httpx contains the HTTP kernel: error responses (RFC 9457), JSON
// encoding/decoding, middleware and pagination helpers. Handlers stay thin:
// decode -> call service -> encode.
package httpx

import (
	"encoding/json"
	"net/http"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/logging"
)

// ProblemContentType is the RFC 9457 media type.
const ProblemContentType = "application/problem+json"

// Problem is the RFC 9457 problem-details body, extended with a stable code.
type Problem struct {
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Status    int                 `json:"status"`
	Code      string              `json:"code"`
	Detail    string              `json:"detail,omitempty"`
	Errors    []apperr.FieldError `json:"errors,omitempty"`
	Context   map[string]any      `json:"context,omitempty"`
	Retryable bool                `json:"retryable,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
}

// HandlerFunc is an HTTP handler that returns an error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP runs f and renders a returned error as a problem response.
func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// WriteError renders err as application/problem+json. Errors that are not
// *apperr.Error are treated as internal: logged in full, shown generically.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := apperr.As(err)
	if !ok {
		e = apperr.Internal(err)
	}

	log := logging.FromContext(r.Context())
	switch {
	case e.Kind == apperr.KindInternal:
		log.Error("request failed", "code", e.Code, "error", err)
	case e.Err != nil:
		log.Debug("request rejected", "code", e.Code, "error", err)
	}

	status := e.Kind.HTTPStatus()
	p := Problem{
		Type:      "urn:kamarapms:problem:" + e.Code,
		Title:     http.StatusText(status),
		Status:    status,
		Code:      e.Code,
		Detail:    e.Message,
		Errors:    e.Fields,
		Context:   e.Context,
		Retryable: e.Retryable,
		RequestID: RequestIDFrom(r.Context()),
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}
