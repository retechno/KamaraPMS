package documents_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack"
	"kamarapms/internal/documents"
	"kamarapms/internal/iam"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

func packProp(decimals int32) tenancy.Property {
	return tenancy.Property{PropertySettings: tenancy.PropertySettings{Name: "Hotel Bali", Timezone: "Asia/Jakarta", CurrencyCode: "IDR", CurrencyDecimals: decimals}}
}

func js(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return b
}

// packInput is a pack with every section, from the types of the reports.
func packInput(t *testing.T) documents.PackInput {
	t.Helper()
	day := d("2026-10-28")
	closedAt := time.Date(2026, 10, 28, 17, 42, 0, 0, time.UTC)
	sum := nightaudit.Summary{BusinessDate: day, OccupancyPercent: "76.53", ADR: "1300000", RevPAR: "981633", Arrivals: 12, Departures: 9,
		RoomRevenue: nightaudit.Money{Net: "139480000", Service: "13948000", Tax: "16897000"}, RevenueByChargeType: []nightaudit.TypeRevenue{{ChargeType: "ROOM", Money: nightaudit.Money{Net: "139480000", Service: "13948000", Tax: "16897000"}}},
		PaymentsByMethod: []nightaudit.MethodTotal{{Method: "CASH", Payments: "148920000", Refunds: "0", Net: "148920000"}}}
	rng := reports.FlashRange{From: day, To: day}
	long := strings.Repeat("Sedimentary-Wijayakusuma ", 8)
	sections := map[string][]byte{
		auditpack.SecSummary: js(t, auditpack.DaySummary{BusinessDate: day, ClosedAt: &closedAt, ClosedBy: "Budi", Summary: js(t, sum)}),
		auditpack.SecFlash:   js(t, reports.DailyFlash{BusinessDate: day, Status: "CLOSED", Ranges: reports.FlashRanges{Today: rng, MTD: rng, LastMonth: rng, LastYear: rng, YTD: rng}, FiscalYearStart: day, Warnings: []reports.FlashWarning{}}),
		auditpack.SecRevenue: js(t, reports.Revenue{From: day, To: day, ByCode: []reports.RevenueLine{{ChargeType: "ROOM", ChargeCode: "ROOM", Name: long, Items: 3, Net: "1234567.500", Service: "10.000", Tax: "5.250", Total: "1234582.750"}},
			Totals: reports.RevenueTotals{Items: 3, Net: "1234567.500", Service: "10.000", Tax: "5.250", Total: "1234582.750"}}),
		auditpack.SecTax:      js(t, reports.TaxReport{From: day, To: day, Taxes: []reports.TaxLine{}, ServiceCharges: []reports.TaxLine{}, TaxTotal: "0.000", ServiceTotal: "0.000"}),
		auditpack.SecPayments: js(t, reports.Cashier{From: day, To: day, Lines: []reports.CashierLine{}, ByMethod: []reports.MethodSum{{Method: "CASH", Payments: "100.000", Refunds: "0.000", Net: "100.000"}}, Net: "100.000"}),
		auditpack.SecGuestLedger: js(t, reports.GuestLedgerReport{AsOf: day, Rows: []reports.GuestLedgerRow{{FolioNumber: "FOL000001", Owner: long, Room: "101", Charges: "1.000", Settlements: "0.000", Balance: "1.000", DepositHeld: "0.000"}},
			Charges: "1.000", Settlements: "0.000", Balance: "1.000", DepositHeld: "0.000", Aging: []reports.AgeTotal{}, Checks: []reports.LedgerCheck{}, Reconciled: true}),
		auditpack.SecDepositLedger: js(t, reports.DepositLedgerReport{AsOf: day, Rows: []reports.DepositLedgerRow{}, Checks: []reports.LedgerCheck{}, Reconciled: true}),
		auditpack.SecCorrections:   js(t, reports.CorrectionReport{From: day, To: day, Rows: []reports.CorrectionRow{}}),
		auditpack.SecCancellations: js(t, reports.CancellationReport{From: day, To: day, Events: []reports.CancellationEvent{}}),
		auditpack.SecOutlets: js(t, auditpack.OutletsData{Date: day, Outlets: []auditpack.OutletRow{{OutletID: 1, Code: "REST", Name: "Restaurant", Bills: 4, Net: "1000000", Service: "100000", Tax: "121000", Total: "1221000", Covers: 9,
			Voids: auditpack.Count{Count: 1, Amount: "55000"}, Comps: auditpack.Count{Amount: "0"}, Discounts: auditpack.Count{Amount: "0"}}},
			Total: auditpack.OutletRow{Name: "Total", Bills: 4, Net: "1000000", Service: "100000", Tax: "121000", Total: "1221000", Covers: 9, BillsWithoutCovers: 1, Voids: auditpack.Count{Count: 1, Amount: "55000"}, Comps: auditpack.Count{Amount: "0"}, Discounts: auditpack.Count{Amount: "0"}}}),
		auditpack.SecOutOfOrder: js(t, auditpack.OutOfOrderData{Date: day, OutOfOrder: 1, Rooms: []auditpack.OutOfOrderRoom{{Room: "101", RoomType: "DLX", Kind: "OOO", From: day, Until: day.AddDays(1), Reason: "Leak in the bathroom"}}}),
		auditpack.SecNextDay:    js(t, map[string]any{"date": day.AddDays(1), "arrivals": reports.ArrivalList{Date: day.AddDays(1), Rows: []reports.ArrivalRow{}}, "departures": reports.StayList{Rows: []reports.StayRow{}}}),
		auditpack.SecAuditNotes: js(t, nightaudit.PackNotes{RoomChargesPosted: 12, Warnings: nightaudit.Warnings{StaleDrafts: []nightaudit.StaleDraft{{ConfirmationNumber: "RES000001", ArrivalDate: day}}}}),
	}
	// 70 stays in house run over a page
	var stays []reports.StayRow
	for i := 0; i < 70; i++ {
		stays = append(stays, reports.StayRow{StayNumber: "STY" + strconv.Itoa(1000+i), Room: strconv.Itoa(100 + i), Guest: "Guest " + strconv.Itoa(i), ArrivalDate: day, DepartureDate: day.AddDays(2), Adults: 2, Balance: "1.000"})
	}
	sections[auditpack.SecInHouse] = js(t, reports.StayList{Rows: stays})
	in := documents.PackInput{BusinessDate: day, Status: auditpack.PackPartial, BuiltAt: time.Date(2026, 10, 28, 17, 42, 5, 0, time.UTC)}
	for _, def := range auditpack.Catalog {
		s := documents.PackSection{Code: def.Code, Status: auditpack.StatusOK, Data: sections[def.Code]}
		switch def.Code {
		case auditpack.SecZReports:
			s = documents.PackSection{Code: def.Code, Status: auditpack.StatusOff}
		case auditpack.SecTax:
			s = documents.PackSection{Code: def.Code, Status: auditpack.StatusFailed, Error: "the tax report broke"}
		case auditpack.SecDepositLedger:
			s = documents.PackSection{Code: def.Code, Status: auditpack.StatusNotAvailable}
		}
		in.Sections = append(in.Sections, s)
	}
	return in
}

func TestThePackPDFHoldsTheCoverAndEverySection(t *testing.T) {
	in := packInput(t)
	for _, decimals := range []int32{0, 3} {
		b, err := documents.RenderPack(documents.Hotel{Name: "Hotel Bali", Address: "Jl. Sunset 1"}, packProp(decimals), in)
		must(t, err)
		s := pdfText(t, documents.Document{Filename: "x.pdf", PDF: b})
		for _, want := range []string{"NIGHT AUDIT PACK", "SECTIONS IN THIS FILE", "Closed by", "Budi", "partial: a section failed", "Night audit summary", "Occupancy 76.53%", "Daily flash report",
			"Revenue", "Guest ledger", "Rooms out of order", "Audit notes", "Not applicable to this property.", "the tax report broke", "Not available: only the close of the night saw this.",
			"As built at the close of the business day", "section 2 of 15", "Leak in the bathroom", "RES000001"} {
			if !strings.Contains(s, want) {
				t.Errorf("decimals %d: the pack lacks %q", decimals, want)
			}
		}
		// the money keeps the decimals of the property, aligned in its column
		want := "1,234,568"
		if decimals == 3 {
			want = "1,234,567.500"
		}
		if !strings.Contains(s, want) {
			t.Errorf("decimals %d: the revenue lacks %q", decimals, want)
		}
		// a section that runs over a page is listed with its range, and the pages are those of the document
		if !strings.Contains(s, "pages ") || !strings.Contains(s, "page 2") {
			t.Errorf("decimals %d: the cover does not give the pages", decimals)
		}
		// the long name wraps inside its cell, never past it
		if !strings.Contains(s, "Sedimentary-Wijayakusuma") {
			t.Errorf("decimals %d: the long name is missing", decimals)
		}
	}
}

func TestThePackPDFIsTheSameBytesEachTime(t *testing.T) {
	in := packInput(t)
	h, p := documents.Hotel{Name: "Hotel Bali"}, packProp(0)
	a, err := documents.RenderPack(h, p, in)
	must(t, err)
	time.Sleep(1100 * time.Millisecond) // the document does not read the clock: a second later it is the same
	b, err := documents.RenderPack(h, p, in)
	must(t, err)
	if string(a) != string(b) {
		t.Fatal("two renders of one pack differ")
	}
	in.Sections = in.Sections[:1] // a pack of one section is a smaller document
	c, err := documents.RenderPack(h, p, in)
	must(t, err)
	if string(c) == string(a) || len(c) >= len(a) {
		t.Fatal("the sections asked for are not the sections printed")
	}
}

func TestAPackWithEmptySectionsStillPrints(t *testing.T) {
	day := d("2026-10-28")
	in := documents.PackInput{BusinessDate: day, Status: auditpack.PackComplete, BuiltAt: time.Date(2026, 10, 28, 17, 0, 0, 0, time.UTC)}
	empty := map[string]any{
		auditpack.SecInHouse:    reports.StayList{Rows: []reports.StayRow{}},
		auditpack.SecOutOfOrder: auditpack.OutOfOrderData{Date: day, Rooms: []auditpack.OutOfOrderRoom{}},
		auditpack.SecAuditNotes: nightaudit.PackNotes{},
		auditpack.SecOutlets:    auditpack.OutletsData{Date: day, Outlets: []auditpack.OutletRow{}},
	}
	for _, code := range []string{auditpack.SecInHouse, auditpack.SecOutOfOrder, auditpack.SecAuditNotes, auditpack.SecOutlets} {
		in.Sections = append(in.Sections, documents.PackSection{Code: code, Status: auditpack.StatusOK, Data: js(t, empty[code])})
	}
	b, err := documents.RenderPack(documents.Hotel{Name: "Hotel Bali"}, packProp(0), in)
	must(t, err)
	s := pdfText(t, documents.Document{Filename: "x.pdf", PDF: b})
	for _, want := range []string{"0 stays in house", "The audit had no findings.", "0 rooms out of order"} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %q", want)
		}
	}
	// a section whose stored JSON cannot be read says so and does not stop the document
	in.Sections[0].Data = []byte(`{"rows": "no"}`)
	b, err = documents.RenderPack(documents.Hotel{Name: "Hotel Bali"}, packProp(0), in)
	must(t, err)
	if s := pdfText(t, documents.Document{Filename: "x.pdf", PDF: b}); !strings.Contains(s, "The stored data of this section could not be read.") {
		t.Error("an unreadable section is not reported")
	}
}

// A real pack: the night audit builds it, the sections are read from the database as they are stored, and the PDF is made from them.
func TestThePDFOfARealPack(t *testing.T) {
	f := setup(t)
	pack := auditpack.NewService(f.TxM, f.Clock, audit.NewWriter(f.Clock), iam.NewAuthorizer(f.TxM), f.Tenancy, f.Reports)
	f.Audit.SetPackBuilder(pack)
	date := civil.MustParseDate("2026-09-30")
	f.Clock.Set(date.At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	_, err := f.Audit.Run(f.admin, f.propID, date)
	must(t, err)

	rows, err := f.Pool.Query(context.Background(), `SELECT s.section, s.status, s.data, coalesce(s.error, ''), s.built_late FROM nap_pack_sections s JOIN nap_packs p ON p.id = s.pack_id WHERE p.property_id = $1`, f.propID)
	must(t, err)
	defer rows.Close()
	stored := map[string]documents.PackSection{}
	for rows.Next() {
		var s documents.PackSection
		must(t, rows.Scan(&s.Code, &s.Status, &s.Data, &s.Error, &s.BuiltLate))
		stored[s.Code] = s
	}
	must(t, rows.Err())
	in := documents.PackInput{BusinessDate: date, Status: auditpack.PackComplete, BuiltAt: f.Clock.Now()}
	for _, def := range auditpack.Catalog {
		in.Sections = append(in.Sections, stored[def.Code])
	}
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	b, err := documents.RenderPack(documents.Hotel{Name: prop.Name}, prop, in)
	must(t, err)
	s := pdfText(t, documents.Document{Filename: "x.pdf", PDF: b})
	for _, want := range []string{"NIGHT AUDIT PACK", "DAILY FLASH REPORT", "GUEST LEDGER", "IN-HOUSE", "Admin"} {
		if !strings.Contains(strings.ToUpper(s), strings.ToUpper(want)) {
			t.Errorf("the pack lacks %q", want)
		}
	}
}
