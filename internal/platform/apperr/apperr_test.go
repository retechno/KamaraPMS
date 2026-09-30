package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestKindHTTPStatus(t *testing.T) {
	cases := map[Kind]int{
		KindInternal:     http.StatusInternalServerError,
		KindBadRequest:   http.StatusBadRequest,
		KindUnauthorized: http.StatusUnauthorized,
		KindForbidden:    http.StatusForbidden,
		KindNotFound:     http.StatusNotFound,
		KindConflict:     http.StatusConflict,
		KindInvalid:      http.StatusUnprocessableEntity,
		KindBusy:         http.StatusConflict,
		KindUnavailable:  http.StatusServiceUnavailable,
		KindRateLimited:  http.StatusTooManyRequests,
	}
	for kind, want := range cases {
		if got := kind.HTTPStatus(); got != want {
			t.Errorf("%s: got %d, want %d", kind, got, want)
		}
	}
}

func TestAsFindsWrappedError(t *testing.T) {
	cause := errors.New("pg: boom")
	err := fmt.Errorf("confirm reservation: %w", Conflict("ROOM_NOT_AVAILABLE", "room 201 is not available").WithCause(cause))

	e, ok := As(err)
	if !ok {
		t.Fatal("As did not find *Error")
	}
	if e.Code != "ROOM_NOT_AVAILABLE" || e.Kind != KindConflict {
		t.Fatalf("unexpected error: %+v", e)
	}
	if !errors.Is(err, cause) {
		t.Fatal("cause is not reachable through Unwrap")
	}
	if !IsCode(err, "ROOM_NOT_AVAILABLE") || IsCode(err, "OTHER") {
		t.Fatal("IsCode mismatch")
	}
}

func TestBusyIsRetryable(t *testing.T) {
	if !Busy("RESOURCE_BUSY", "try again").Retryable {
		t.Fatal("Busy errors must be retryable")
	}
	if Conflict("X", "x").Retryable {
		t.Fatal("Conflict errors are not retryable by default")
	}
}

func TestWithContextAndInvalid(t *testing.T) {
	e := Invalid("check the input", FieldError{Field: "arrival_date", Code: "REQUIRED"}).
		WithContext("nights", []string{"2026-10-01"})
	if e.Code != "VALIDATION_FAILED" || len(e.Fields) != 1 || e.Context["nights"] == nil {
		t.Fatalf("unexpected error: %+v", e)
	}
}

func TestInternalHidesCauseInMessage(t *testing.T) {
	e := Internal(errors.New("secret connection string"))
	if e.Message != "an unexpected error occurred" {
		t.Fatalf("internal message leaks detail: %q", e.Message)
	}
}
