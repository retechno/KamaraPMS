package clock

import (
	"testing"
	"time"
)

func TestSystemIsUTC(t *testing.T) {
	if loc := (System{}).Now().Location(); loc != time.UTC {
		t.Fatalf("got location %v", loc)
	}
}

func TestFake(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*3600)
	f := NewFake(time.Date(2026, 10, 1, 2, 30, 0, 0, jakarta))
	if got := f.Now(); !got.Equal(time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Fatalf("got %v", got)
	}
	f.Advance(90 * time.Minute)
	if got := f.Now(); !got.Equal(time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("after Advance got %v", got)
	}
	f.Set(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if f.Now().Year() != 2027 {
		t.Fatal("Set did not apply")
	}
}
