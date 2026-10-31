package notifications

import (
	"strings"
	"testing"
	"time"

	"kamarapms/internal/auditpack"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
)

func TestGroupThousands(t *testing.T) {
	for in, want := range map[string]string{
		"0": "0", "12": "12", "780": "780", "1000": "1,000", "780000": "780,000", "-1234567": "-1,234,567",
		"3.00": "3.00", "1234567.50": "1,234,567.50", "-999.5": "-999.5", "": "",
	} {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%q) = %q, want %q", in, got, want)
		}
	}
}

func testFlash() *reports.DailyFlash {
	line := func(key, label, today string) reports.FlashLine {
		return reports.FlashLine{Key: key, Label: label, Today: today}
	}
	return &reports.DailyFlash{
		Rooms:    []reports.FlashLine{line("rooms_sold", "Rooms sold", "3"), line("occupancy_pct", "Occupancy % (with complimentary)", "3.00"), line("arr", "ARR", "780000")},
		Activity: []reports.FlashLine{line("no_shows", "No-show", "1"), line("arrivals", "Arrivals", "12"), line("departures", "Departures", "1200")},
		Revenue:  reports.FlashRevenueSection{TotalNet: line("total_net", "Total net revenue", "2340000"), GTR: line("gtr", "Gross total revenue (GTR)", "2750000")},
		Payments: reports.FlashPaymentsSection{GTP: line("gtp", "Money taken (GTP)", "1800000")},
	}
}

func TestThePackMessageWritesTheFiguresAndClosesWithWhy(t *testing.T) {
	view := auditpack.MailView{Date: civil.MustParseDate("2026-10-30"), Flash: testFlash()}
	m := packMessage("gm@example.test", "Hotel Bali", "IDR", view, packGroup{sections: []string{auditpack.SecFlash}, name: "pack.pdf", pdf: []byte("%PDF-")}, 1, 1)
	for _, want := range []string{
		"Rooms sold: 3\n", "Occupancy: 3.00%\n", "ARR: IDR 780,000\n", "Total net revenue: IDR 2,340,000\n", "Money taken (GTP): IDR 1,800,000\n",
		"Arrivals \u00b7 departures \u00b7 no-shows: 12 \u00b7 1,200 \u00b7 1\n",
	} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, m.Text)
		}
	}
	if !strings.Contains(m.HTML, "<li>ARR: IDR 780,000</li>") || !strings.Contains(m.HTML, "<li>Arrivals \u00b7 departures \u00b7 no-shows: 12 \u00b7 1,200 \u00b7 1</li>") {
		t.Errorf("html: %s", m.HTML)
	}
	const footer = "You receive this because your address is a night audit recipient of this hotel. Ask the hotel to change or stop it."
	if !strings.HasSuffix(strings.TrimSpace(m.Text), footer) || !strings.HasSuffix(m.HTML, footer+"</p>") {
		t.Errorf("the message ends with the reason it was sent:\n%s\n%s", m.Text, m.HTML)
	}
	if m.FromName != "Hotel Bali" || m.Subject != "Night audit pack 2026-10-30 - Hotel Bali" {
		t.Errorf("sender %q, subject %q", m.FromName, m.Subject)
	}
	// a part after the first carries no figures but still closes with the reason
	m = packMessage("gm@example.test", "Hotel Bali", "IDR", view, packGroup{sections: []string{auditpack.SecFlash}, name: "pack.pdf"}, 2, 2)
	if strings.Contains(m.Text, "Rooms sold") || !strings.Contains(m.Text, footer) {
		t.Errorf("part 2: %s", m.Text)
	}
}

func TestTheSenderNameIsThePropertyOfTheMessage(t *testing.T) {
	m := Message{To: "gm@example.test", FromName: "Hotel Bali", Subject: "s", Text: "t"}
	raw, err := Build(m, "frontdesk@kamara.test", "Kamara Hotels", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `From: "Hotel Bali" <frontdesk@kamara.test>`) {
		t.Errorf("the name of the message wins over the one of the configuration:\n%s", raw)
	}
	m.FromName = ""
	raw, _ = Build(m, "frontdesk@kamara.test", "Kamara Hotels", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(string(raw), `"Kamara Hotels" <frontdesk@kamara.test>`) {
		t.Errorf("without a name of its own the configured one is used:\n%s", raw)
	}
}
