package banquet_test

import (
	"testing"

	"kamarapms/internal/banquet"
)

// The daily flash report over a banquet (docs/architecture/38-reports-night-audit-pack.md 7.1): the package of section 10.1 of the banquet design, 100 guests, posted on the open day. Its four
// components are 15,000,000 of lunch and 8,000,000 of coffee breaks (food and beverage, account 4240, group B), 5,000,000 of room rental and 2,000,000 of equipment (account 4410, group C).
// The banquet block of each group has its own title, and the pax is the guaranteed number because no count was entered (a warning says so).
func TestTheDailyFlashShowsTheBanquetBlocksAndThePax(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Fullday", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	_, err := m.svc.PostSession(m.office, m.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)

	fl, err := m.Reports.DailyFlash(m.admin, m.propID, day("2026-09-30"))
	must(t, err)
	totals := map[string]string{}
	kinds := map[string]string{}
	for _, g := range fl.Revenue.Groups {
		totals[g.Code] = g.Total.Today
		for _, b := range g.Blocks {
			kinds[g.Code+"/"+b.Title] = b.Kind + " " + b.Total.Today
		}
	}
	if totals["A"] != "0" || totals["B"] != "23000000" || totals["C"] != "7000000" {
		t.Fatalf("groups %v blocks %v", totals, kinds)
	}
	if kinds["B/Banquet"] != "BANQUET 23000000" || kinds["C/Banquet"] != "BANQUET 7000000" {
		t.Fatalf("banquet blocks: %v", kinds)
	}
	if fl.Revenue.TotalNet.Today != "30000000" {
		t.Fatalf("total net %s", fl.Revenue.TotalNet.Today)
	}
	var bq, hotel, pos string
	for _, l := range fl.Revenue.BySource {
		switch l.Key {
		case "source_banquet":
			bq = l.Today
		case "source_hotel":
			hotel = l.Today
		case "source_pos":
			pos = l.Today
		}
	}
	if bq != "30000000" || hotel != "0" || pos != "0" {
		t.Fatalf("by source: banquet %s hotel %s pos %s", bq, hotel, pos)
	}
	// A + B + C is the income statement of the day
	rev, err := m.Reports.Revenue(m.admin, m.propID, day("2026-09-30"), day("2026-09-30"))
	must(t, err)
	if rev.Totals.Net != fl.Revenue.TotalNet.Today {
		t.Fatalf("income statement %s vs %s", rev.Totals.Net, fl.Revenue.TotalNet.Today)
	}
	// pax 100 (the guaranteed number: nobody entered the count), 30,000,000 / 100 = 300,000 a pax
	var pax, per string
	for _, c := range fl.Covers {
		switch c.Key {
		case "banquet_pax":
			pax = c.Today
		case "banquet_per_pax":
			per = c.Today
		}
	}
	if pax != "100" || per != "300000" {
		t.Fatalf("pax %q per pax %q: %+v", pax, per, fl.Covers)
	}
	fallback := false
	for _, w := range fl.Warnings {
		if w.Code == "BANQUET_PAX_FALLBACK" && w.Params["sessions"] == "1" {
			fallback = true
		}
	}
	if !fallback {
		t.Fatalf("the guaranteed number was used: %+v", fl.Warnings)
	}
}
