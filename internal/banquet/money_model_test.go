package banquet_test

import (
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func sum(ds []decimal.Decimal) decimal.Decimal {
	t := decimal.Zero
	for _, d := range ds {
		t = t.Add(d)
	}
	return t
}

// 10.1: the package of 363,000 nett for 100 guests, shared by the list weights 150, 80, 50 and 20 thousand.
func TestThePackageIsSplitByTheListWeightsOfItsComponents(t *testing.T) {
	got := banquet.SplitPackage(dec("36300000"), []decimal.Decimal{dec("150000"), dec("80000"), dec("50000"), dec("20000")}, 0)
	want := []string{"18150000", "9680000", "6050000", "2420000"}
	for i := range want {
		if !got[i].Equal(dec(want[i])) {
			t.Fatalf("share %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

// 10.1, the rounding case: 100,000 for three equal components is 33,333 each and the difference of 1 goes to the first of the largest.
func TestTheRoundingDifferenceGoesToTheLargestComponent(t *testing.T) {
	got := banquet.SplitPackage(dec("100000"), []decimal.Decimal{dec("10"), dec("10"), dec("10")}, 0)
	want := []string{"33334", "33333", "33333"}
	for i := range want {
		if !got[i].Equal(dec(want[i])) {
			t.Fatalf("share %d: got %s, want %s", i, got[i], want[i])
		}
	}
	// the largest takes it, not the first
	got = banquet.SplitPackage(dec("100"), []decimal.Decimal{dec("1"), dec("2"), dec("1")}, 0)
	if !got[1].Equal(dec("50")) || !sum(got).Equal(dec("100")) {
		t.Fatalf("got %v", got)
	}
}

// For any price, guests and weights the parts add up to the total exactly, at every number of decimals.
func TestTheSharesOfAPackageAlwaysAddUpToTheTotal(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for n := 0; n < 3000; n++ {
		decimals := int32(r.Intn(4))
		k := 1 + r.Intn(6)
		w := make([]decimal.Decimal, k)
		for i := range w {
			w[i] = decimal.NewFromInt(int64(r.Intn(500000)))
		}
		total := decimal.New(int64(1+r.Intn(90000000)), -decimals)
		got := banquet.SplitPackage(total, w, decimals)
		if !sum(got).Equal(total) {
			t.Fatalf("total %s weights %v decimals %d: shares %v add up to %s", total, w, decimals, got, sum(got))
		}
		for i, g := range got {
			if g.IsNegative() || !g.Equal(g.Round(decimals)) {
				t.Fatalf("share %d is %s", i, g)
			}
		}
	}
}

func TestEveryWeightZeroSharesEqually(t *testing.T) {
	got := banquet.SplitPackage(dec("90"), []decimal.Decimal{dec("0"), dec("0"), dec("0")}, 0)
	if !sum(got).Equal(dec("90")) || !got[1].Equal(dec("30")) {
		t.Fatalf("got %v", got)
	}
}

// 10.5: the billed guests are the larger of the guaranteed and the actual, the actual alone when nothing was guaranteed, and an AUTO session on the guarantee is flagged.
func TestTheBilledGuestsAreTheLargerOfGuaranteedAndActual(t *testing.T) {
	cases := []struct {
		g, a    *int
		pax     int
		basis   string
		flagged bool
		ok      bool
	}{
		{ptr(100), ptr(92), 100, banquet.BasisGuaranteed, false, true},
		{ptr(100), ptr(104), 104, banquet.BasisActual, false, true},
		{ptr(100), nil, 100, banquet.BasisGuaranteed, true, true},
		{nil, ptr(80), 80, banquet.BasisActual, false, true},
		{nil, nil, 0, "", false, false},
	}
	for i, c := range cases {
		pax, basis, flagged, ok := banquet.BilledPax(c.g, c.a)
		if pax != c.pax || basis != c.basis || flagged != c.flagged || ok != c.ok {
			t.Fatalf("case %d: got %d %s %v %v", i, pax, basis, flagged, ok)
		}
	}
}

func fullday() banquet.PlanLine {
	return banquet.PlanLine{ID: 1, Name: "Fullday Meeting", IsPackage: true, Basis: "PER_PAX", Quantity: dec("1"), UnitPrice: dec("363000"), Mode: banquet.PriceInclusive,
		Components: []banquet.PlanComponent{
			{ItemID: 11, Name: "Lunch", ChargeCodeID: 1, QtyPerPax: dec("1"), ListPrice: dec("150000")},
			{ItemID: 12, Name: "Coffee break", ChargeCodeID: 1, QtyPerPax: dec("2"), ListPrice: dec("40000")},
			{ItemID: 13, Name: "Room", ChargeCodeID: 2, QtyPerPax: dec("1"), ListPrice: dec("50000")},
			{ItemID: 14, Name: "Equipment", ChargeCodeID: 3, QtyPerPax: dec("1"), ListPrice: dec("20000")},
		}}
}

func TestAPackageLineBecomesOneChargeForEachComponent(t *testing.T) {
	plan := banquet.PlanSession([]banquet.PlanLine{fullday()}, 100, 0)
	if len(plan) != 4 {
		t.Fatalf("got %d charges", len(plan))
	}
	total := decimal.Zero
	for _, c := range plan {
		if !c.Quantity.Equal(dec("1")) || c.Mode != banquet.PriceInclusive || c.ComponentID == 0 {
			t.Fatalf("charge %+v", c)
		}
		total = total.Add(c.UnitPrice)
	}
	if !total.Equal(dec("36300000")) || !plan[0].UnitPrice.Equal(dec("18150000")) {
		t.Fatalf("total %s, first %s", total, plan[0].UnitPrice)
	}
	if got := plan[0].Key(9); got != "banquet:session:9:1:11" {
		t.Fatalf("key %s", got)
	}
}

func TestADiscountOnAPackageIsSharedByTheComponents(t *testing.T) {
	l := fullday()
	l.DiscountPct = ptr(dec("10"))
	total := decimal.Zero
	for _, c := range banquet.PlanSession([]banquet.PlanLine{l}, 100, 0) {
		total = total.Add(c.Discount)
	}
	if !total.Equal(dec("3630000")) {
		t.Fatalf("discount %s", total)
	}
	l.DiscountPct, l.DiscountAmount = nil, ptr(dec("999999999"))
	total = decimal.Zero
	for _, c := range banquet.PlanSession([]banquet.PlanLine{l}, 100, 0) {
		total = total.Add(c.Discount)
	}
	if !total.Equal(dec("36300000")) {
		t.Fatalf("a discount above the base is limited to it: %s", total)
	}
}

func TestTheQuantityOfALineFollowsItsBasis(t *testing.T) {
	lines := []banquet.PlanLine{
		{ID: 1, Name: "Lunch", Basis: "PER_PAX", Quantity: dec("1"), UnitPrice: dec("135000"), Mode: banquet.PriceExclusive, ChargeCodeID: 1},
		{ID: 2, Name: "Coffee", Basis: "PER_PAX", Quantity: dec("2"), UnitPrice: dec("45000"), Mode: banquet.PriceExclusive, ChargeCodeID: 1},
		{ID: 3, Name: "Projector", Basis: "PER_SESSION", Quantity: dec("1"), UnitPrice: dec("750000"), Mode: banquet.PriceExclusive, ChargeCodeID: 2},
		{ID: 4, Name: "Room", Basis: "PER_HOUR", Quantity: dec("4"), UnitPrice: dec("500000"), Mode: banquet.PriceExclusive, ChargeCodeID: 2},
		{ID: 5, Name: "Free", Basis: "PER_UNIT", Quantity: dec("3"), UnitPrice: dec("0"), Mode: banquet.PriceExclusive, ChargeCodeID: 2},
	}
	plan := banquet.PlanSession(lines, 50, 0)
	if len(plan) != 4 {
		t.Fatalf("a free line is not posted: %d charges", len(plan))
	}
	for i, q := range []string{"50", "100", "1", "4"} {
		if !plan[i].Quantity.Equal(dec(q)) {
			t.Fatalf("charge %d quantity %s, want %s", i, plan[i].Quantity, q)
		}
	}
	if got := banquet.PlanSession(lines[:2], 0, 0); len(got) != 0 {
		t.Fatalf("no guests, nothing to post: %v", got)
	}
}

// 10.3: 60 days or more 0%, 30 to 59 25%, 7 to 29 50%, less than 7 100%.
func TestTheTierInForceIsFoundByTheDaysBeforeTheFirstSession(t *testing.T) {
	tiers := []banquet.Tier{
		{DaysFrom: 60, Percent: dec("0"), Action: banquet.ActionFee},
		{DaysFrom: 30, DaysTo: ptr(59), Percent: dec("25"), Action: banquet.ActionFee},
		{DaysFrom: 7, DaysTo: ptr(29), Percent: dec("50"), Action: banquet.ActionFee},
		{DaysFrom: 0, DaysTo: ptr(6), Percent: dec("100"), Action: banquet.ActionFee},
	}
	for days, want := range map[int]string{90: "0", 60: "0", 59: "25", 30: "25", 29: "50", 20: "50", 7: "50", 6: "100", 0: "100"} {
		got := banquet.TierFor(tiers, days)
		if got == nil || !got.Percent.Equal(dec(want)) {
			t.Fatalf("%d days: got %v, want %s%%", days, got, want)
		}
	}
	if banquet.TierFor(tiers[1:], 90) != nil {
		t.Fatal("no tier covers 90 days here")
	}
	if i := banquet.CheckTiers(tiers); i >= 0 {
		t.Fatalf("these tiers do not overlap, got %d", i)
	}
	if i := banquet.CheckTiers([]banquet.Tier{{DaysFrom: 10, DaysTo: ptr(20)}, {DaysFrom: 15}}); i != 1 {
		t.Fatalf("overlap at %d", i)
	}
	if i := banquet.CheckTiers([]banquet.Tier{{DaysFrom: 0}, {DaysFrom: 5, DaysTo: ptr(9)}}); i != 1 {
		t.Fatalf("an open end overlaps what follows: %d", i)
	}
}

func TestTheFeeIsAPercentOfTheBase(t *testing.T) {
	if got := banquet.FeeOf(dec("30000000"), dec("50"), 0); !got.Equal(dec("15000000")) {
		t.Fatalf("NET: %s", got)
	}
	if got := banquet.FeeOf(dec("36300000"), dec("50"), 0); !got.Equal(dec("18150000")) {
		t.Fatalf("GROSS: %s", got)
	}
	if got := banquet.DaysBefore(day("2026-11-01"), day("2026-11-21")); got != 20 {
		t.Fatalf("days before: %d", got)
	}
	if got := banquet.DaysBefore(day("2026-11-21"), day("2026-11-01")); got != 0 {
		t.Fatalf("after the event: %d", got)
	}
}

// 10.2: 30%, 50% and 20% of 36,300,000.
func TestTheDepositScheduleAddsUpToTheContract(t *testing.T) {
	rows := []banquet.DepositRow{{Seq: 1, Percent: ptr(dec("30"))}, {Seq: 2, Percent: ptr(dec("50"))}, {Seq: 3, Percent: ptr(dec("20"))}}
	got := banquet.ScheduleAmounts(rows, dec("36300000"), 0)
	for i, w := range []string{"10890000", "18150000", "7260000"} {
		if !got[i].Amount.Equal(dec(w)) {
			t.Fatalf("row %d: %s", i, got[i].Amount)
		}
	}
	// the last row takes the rounding
	got = banquet.ScheduleAmounts([]banquet.DepositRow{{Percent: ptr(dec("33.33"))}, {Percent: ptr(dec("33.33"))}, {Percent: ptr(dec("33.34"))}}, dec("1000"), 0)
	if !got[0].Amount.Add(got[1].Amount).Add(got[2].Amount).Equal(dec("1000")) {
		t.Fatalf("rounding: %v", got)
	}
	// a fixed amount stays
	got = banquet.ScheduleAmounts([]banquet.DepositRow{{Amount: dec("5000")}, {Percent: ptr(dec("10"))}}, dec("1000"), 0)
	if !got[0].Amount.Equal(dec("5000")) || !got[1].Amount.Equal(dec("100")) {
		t.Fatalf("fixed: %v", got)
	}
}

// A row is paid when the deposits received reach the cumulative amount due; nothing links a row to a payment.
func TestADepositRowIsPaidWhenTheCumulativeAmountIsReceived(t *testing.T) {
	rows := []banquet.DepositRow{{Amount: dec("100"), DueDate: day("2026-10-01")}, {Amount: dec("200"), DueDate: day("2026-10-20")}, {Amount: dec("300"), DueDate: day("2026-12-01")}}
	today := day("2026-10-15")
	for received, want := range map[string][3]string{
		"0":   {"OVERDUE", "DUE_SOON", "PENDING"},
		"100": {"PAID", "DUE_SOON", "PENDING"},
		"299": {"PAID", "DUE_SOON", "PENDING"},
		"300": {"PAID", "PAID", "PENDING"},
		"600": {"PAID", "PAID", "PAID"},
	} {
		for i := range rows {
			if got := banquet.RowStatus(rows, i, dec(received), today); got != want[i] {
				t.Fatalf("received %s row %d: got %s, want %s", received, i, got, want[i])
			}
		}
	}
}
