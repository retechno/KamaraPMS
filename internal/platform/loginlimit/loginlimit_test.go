package loginlimit

import (
	"testing"
	"time"

	"kamarapms/internal/platform/clock"
)

func TestLimiter(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	l := New(3, 15*time.Minute, c)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("k"); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		l.Fail("k")
	}
	if ok, until := l.Allowed("k"); ok || !until.Equal(c.Now().Add(15*time.Minute)) {
		t.Fatalf("4th attempt should be blocked until the window ends, got %v %v", ok, until)
	}
	if ok, _ := l.Allowed("other"); !ok {
		t.Fatal("keys are independent")
	}
	c.Advance(15 * time.Minute)
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("window should reset")
	}
	l.Fail("k")
	l.Reset("k")
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("reset")
	}
}
