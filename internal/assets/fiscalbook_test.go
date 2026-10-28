package assets_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
)

func g1() assets.FiscalRate {
	return assets.FiscalRate{LifeYears: 4, SLRate: dec("25"), DBRate: dec("50")}
}

func book(t *testing.T, in assets.FiscalInput) *assets.FiscalBook {
	t.Helper()
	if in.TaxYearStartMonth == 0 {
		in.TaxYearStartMonth = 1
	}
	return assets.NewFiscalBook(in)
}

func yearAmount(b *assets.FiscalBook, year int) decimal.Decimal {
	return b.Accumulated(d(itoa(year) + "-12-31")).Sub(b.Accumulated(d(itoa(year-1) + "-12-31")))
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

func wantAmount(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Fatalf("%s: got %s, want %s", what, got, want)
	}
}

// 10.12: a computer of 10,800,000 acquired in March 2026, group 1.
func TestFiscalBookCommercialAgainstFiscal(t *testing.T) {
	layers := []assets.FiscalLayer{{Start: d("2026-03-10"), Cost: dec("10800000")}}
	sl := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: layers})
	wantAmount(t, "SL 2026", yearAmount(sl, 2026), "2250000")
	wantAmount(t, "SL 2027", yearAmount(sl, 2027), "2700000")
	db := book(t, assets.FiscalInput{Rate: g1(), Method: "DB", Layers: layers})
	wantAmount(t, "DB 2026", yearAmount(db, 2026), "4500000")
	wantAmount(t, "DB 2027", yearAmount(db, 2027), "3150000")
	wantAmount(t, "nothing before the acquisition", db.Accumulated(d("2026-02-28")), "0")
	// the whole cost is used up at the end of the useful life and never more
	wantAmount(t, "SL at the end of the life", sl.Accumulated(d("2040-12-31")), "10800000")
	wantAmount(t, "DB at the end of the life", db.Accumulated(d("2040-12-31")), "10800000")
}

// 10.15: an old asset whose fiscal depreciation was finished before the register: its fiscal state is the one it came with.
func TestFiscalBookOldAssetFromOpening(t *testing.T) {
	asOf := d("2026-09-30")
	b := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: []assets.FiscalLayer{{Start: d("2020-01-15"), Cost: dec("36000000")}},
		OpeningAccum: ptr(dec("36000000")), OpeningAsOf: &asOf})
	wantAmount(t, "fiscal 2027", yearAmount(b, 2027), "0")
	wantAmount(t, "fiscal 2026", yearAmount(b, 2026), "0")
	// an old asset that still has fiscal life: the opening state plus what the rates give after the date
	half := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: []assets.FiscalLayer{{Start: d("2025-01-15"), Cost: dec("12000000")}},
		OpeningAccum: ptr(dec("3000000")), OpeningAsOf: &asOf})
	wantAmount(t, "at the date", half.Accumulated(asOf), "3000000")
	// computed 2025-01 to 2026-09 is 12M x 25% x 21 / 12 = 5,250,000; 2026-12 adds three months: 750,000
	wantAmount(t, "three months later", half.Accumulated(d("2026-12-31")), "3750000")
}

// A part of the cost leaves: the part that left stops at its month, the part that stays goes on (4.9, the disposal).
func TestFiscalBookDisposals(t *testing.T) {
	layers := []assets.FiscalLayer{{Start: d("2026-01-05"), Cost: dec("12000000")}}
	whole := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: layers, Disposals: []assets.FiscalDisposalInput{{Date: d("2026-06-20"), CostRemoved: dec("12000000")}}})
	wantAmount(t, "removed", whole.RemovedAccumulated(0), "1500000")
	wantAmount(t, "frozen after the disposal", whole.Accumulated(d("2027-12-31")), "1500000")
	wantAmount(t, "nothing in 2027", yearAmount(whole, 2027), "0")

	part := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: layers, Disposals: []assets.FiscalDisposalInput{{Date: d("2026-06-20"), CostRemoved: dec("6000000")}}})
	wantAmount(t, "removed half", part.RemovedAccumulated(0), "750000")
	wantAmount(t, "2026 both parts", yearAmount(part, 2026), "2250000")
	wantAmount(t, "2027 the half that stays", yearAmount(part, 2027), "1500000")
}

// A value added is a layer that starts in the month of its date.
func TestFiscalBookValueAdded(t *testing.T) {
	b := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", Layers: []assets.FiscalLayer{{Start: d("2026-01-05"), Cost: dec("12000000")}, {Start: d("2026-07-01"), Cost: dec("4800000")}}})
	// 12M x 25% = 3,000,000 and 4.8M x 25% x 6 / 12 = 600,000
	wantAmount(t, "2026", yearAmount(b, 2026), "3600000")
}

// A tax year that starts in July.
func TestFiscalBookTaxYearStartsInJuly(t *testing.T) {
	b := book(t, assets.FiscalInput{Rate: g1(), Method: "SL", TaxYearStartMonth: 7, Layers: []assets.FiscalLayer{{Start: d("2026-03-05"), Cost: dec("12000000")}}})
	// the year July 2025 to June 2026 holds four months (March to June): 12M x 25% x 4 / 12 = 1,000,000
	wantAmount(t, "first year", b.Accumulated(d("2026-06-30")), "1000000")
	wantAmount(t, "next year", b.Accumulated(d("2027-06-30")).Sub(b.Accumulated(d("2026-06-30"))), "3000000")
}
