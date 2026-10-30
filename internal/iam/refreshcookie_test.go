package iam

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kamarapms/internal/platform/clock"
)

// The lifetime of the refresh cookie is measured against the injected clock, so it is the same on any day the test runs. It used to be measured with time.Until: a fake
// clock set in the past plus the 30 days of the token ended before the real date, MaxAge went negative and the cookie looked deleted.
func TestRefreshCookieMaxAgeFollowsTheInjectedClock(t *testing.T) {
	const thirtyDays = 30 * 24 * time.Hour
	for name, now := range map[string]time.Time{
		"a fake clock far in the past":   time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC),
		"a fake clock in the near past":  time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC),
		"a fake clock far in the future": time.Date(2999, 12, 31, 23, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			h := &Handler{svc: &Service{clock: clock.NewFake(now)}, cookieSecure: true}
			rec := httptest.NewRecorder()
			h.setRefreshCookie(rec, Session{RefreshToken: "token", RefreshExpiresAt: now.Add(thirtyDays)})

			var got *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == RefreshCookie {
					got = c
				}
			}
			if got == nil {
				t.Fatal("no refresh cookie")
			}
			if got.MaxAge != 2592000 { // 30 days in seconds
				t.Fatalf("MaxAge = %d, want 2592000", got.MaxAge)
			}
			if !got.Expires.Equal(now.Add(thirtyDays)) {
				t.Fatalf("Expires = %v, want %v", got.Expires, now.Add(thirtyDays))
			}
		})
	}
}

func TestRefreshCookieMaxAgeOfATokenAlreadyExpired(t *testing.T) {
	now := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)
	if got := refreshCookieMaxAge(now.Add(-time.Minute), now); got >= 0 {
		t.Fatalf("MaxAge = %d, want a negative number (the browser drops the cookie)", got)
	}
}
