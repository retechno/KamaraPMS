package cityledger

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

func TestBucketOf(t *testing.T) {
	for days, want := range map[int]int{0: 0, 30: 0, 31: 1, 60: 1, 61: 2, 90: 2, 91: 3, 400: 3} {
		if got := bucketOf(days); got != want {
			t.Errorf("bucketOf(%d) = %d, want %d", days, got, want)
		}
	}
}

func TestAgeTransfersSettlesTheOldestFirst(t *testing.T) {
	d := civil.MustParseDate
	dec := decimal.NewFromInt
	asOf := d("2026-10-31")
	transfers := []transfer{
		{d("2026-07-01"), dec(100)}, // 122 days: 90+
		{d("2026-08-15"), dec(200)}, // 77 days: 61-90
		{d("2026-10-01"), dec(300)}, // 30 days: 0-30
	}
	// nothing paid: every transfer is open in its bucket
	got := ageTransfers(transfers, dec(0), asOf)
	if !got[0].Equal(dec(300)) || !got[1].IsZero() || !got[2].Equal(dec(200)) || !got[3].Equal(dec(100)) {
		t.Fatalf("unpaid: %v", got)
	}
	// 150 paid: the oldest is settled and half of the next one
	got = ageTransfers(transfers, dec(150), asOf)
	if !got[0].Equal(dec(300)) || !got[2].Equal(dec(150)) || !got[3].IsZero() {
		t.Fatalf("partly paid: %v", got)
	}
	// everything paid
	got = ageTransfers(transfers, dec(600), asOf)
	for i, b := range got {
		if !b.IsZero() {
			t.Fatalf("bucket %d: %v", i, b)
		}
	}
}
