// Package reports is the read-only reporting module (docs/architecture/07-milestones.md, M14). Money reports read the
// ledger rows and the component snapshots by business date (never the masters), so a report of a day does not change
// when a rate, name or account is edited later. Nothing here writes, locks or posts.
package reports

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/civil"
)

// MaxRangeDays bounds a report range.
const MaxRangeDays = 366

// Table is what a report exports as CSV.
type Table interface {
	CSV() (header []string, rows [][]string)
}

// ---------------------------------------------------------------- daily summary

// DailySummary is the closing summary of a business date: stored for a closed day, live for the open one.
type DailySummary struct {
	BusinessDate civil.Date          `json:"business_date"`
	Status       string              `json:"status"`
	Live         bool                `json:"live"`
	Summary      *nightaudit.Summary `json:"summary"`
}

func (d DailySummary) CSV() ([]string, [][]string) {
	h := []string{"business_date", "status", "rooms_total", "rooms_out_of_order", "rooms_out_of_service", "rooms_sellable", "rooms_occupied", "room_nights_sold",
		"arrivals", "departures", "no_shows", "room_net", "room_service", "room_tax", "occupancy_percent", "adr", "revpar"}
	if d.Summary == nil {
		return h, nil
	}
	s := d.Summary
	return h, [][]string{{d.BusinessDate.String(), d.Status, itoa(s.Rooms.Total), itoa(s.Rooms.OutOfOrder), itoa(s.Rooms.OutOfService), itoa(s.Rooms.Sellable),
		itoa(s.Rooms.Occupied), itoa(s.Rooms.Sold), itoa(s.Arrivals), itoa(s.Departures), itoa(s.NoShows), s.RoomRevenue.Net, s.RoomRevenue.Service, s.RoomRevenue.Tax,
		s.OccupancyPercent, s.ADR, s.RevPAR}}
}

// ---------------------------------------------------------------- revenue

// RevenueLine is the revenue of one charge code (and revenue account) in a range. Corrections net out.
type RevenueLine struct {
	ChargeCodeID       int64   `json:"charge_code_id"`
	ChargeCode         string  `json:"charge_code"`
	Name               string  `json:"name"`
	ChargeType         string  `json:"charge_type"`
	RevenueAccountCode *string `json:"revenue_account_code"`
	Items              int     `json:"items"`
	Base               string  `json:"base_amount"`
	Discount           string  `json:"discount_amount"`
	Net                string  `json:"net_amount"`
	Service            string  `json:"service_charge"`
	Tax                string  `json:"tax"`
	Total              string  `json:"total"`
}

// RevenueTotals sums lines.
type RevenueTotals struct {
	Items   int    `json:"items"`
	Net     string `json:"net_amount"`
	Service string `json:"service_charge"`
	Tax     string `json:"tax"`
	Total   string `json:"total"`
}

// TypeTotal is the revenue of one charge type.
type TypeTotal struct {
	ChargeType string `json:"charge_type"`
	RevenueTotals
}

// Revenue is the revenue report.
type Revenue struct {
	From   civil.Date    `json:"from"`
	To     civil.Date    `json:"to"`
	ByCode []RevenueLine `json:"by_charge_code"`
	ByType []TypeTotal   `json:"by_charge_type"`
	Totals RevenueTotals `json:"totals"`
}

func (r Revenue) CSV() ([]string, [][]string) {
	h := []string{"charge_type", "charge_code", "name", "revenue_account_code", "items", "base_amount", "discount_amount", "net_amount", "service_charge", "tax", "total"}
	var rows [][]string
	for _, l := range r.ByCode {
		rows = append(rows, []string{l.ChargeType, l.ChargeCode, l.Name, deref(l.RevenueAccountCode), itoa(l.Items), l.Base, l.Discount, l.Net, l.Service, l.Tax, l.Total})
	}
	rows = append(rows, []string{"TOTAL", "", "", "", itoa(r.Totals.Items), "", "", r.Totals.Net, r.Totals.Service, r.Totals.Tax, r.Totals.Total})
	return h, rows
}

// ---------------------------------------------------------------- tax

// TaxLine is a tax or service charge as it was posted: code, name, rate and account are the snapshots.
type TaxLine struct {
	ComponentType string  `json:"component_type"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Rate          string  `json:"rate"`
	GLAccountCode *string `json:"gl_account_code"`
	Items         int     `json:"items"`
	Base          string  `json:"base_amount"`
	Amount        string  `json:"amount"`
}

// TaxReport lists taxes and service charges collected in a range. A rate edited in the range gives two lines.
type TaxReport struct {
	From           civil.Date `json:"from"`
	To             civil.Date `json:"to"`
	Taxes          []TaxLine  `json:"taxes"`
	ServiceCharges []TaxLine  `json:"service_charges"`
	TaxTotal       string     `json:"tax_total"`
	ServiceTotal   string     `json:"service_charge_total"`
}

func (r TaxReport) CSV() ([]string, [][]string) {
	h := []string{"component_type", "code", "name", "rate", "gl_account_code", "items", "base_amount", "amount"}
	var rows [][]string
	for _, l := range append(append([]TaxLine{}, r.Taxes...), r.ServiceCharges...) {
		rows = append(rows, []string{l.ComponentType, l.Code, l.Name, l.Rate, deref(l.GLAccountCode), itoa(l.Items), l.Base, l.Amount})
	}
	return h, rows
}

// ---------------------------------------------------------------- cashier

// CashierLine is one payment method on one business date.
type CashierLine struct {
	BusinessDate civil.Date `json:"business_date"`
	Method       string     `json:"payment_method"`
	Payments     string     `json:"payments"`
	Refunds      string     `json:"refunds"`
	Net          string     `json:"net"`
	Count        int        `json:"count"`
	Voided       string     `json:"voided"`
	VoidedCount  int        `json:"voided_count"`
}

// MethodSum totals a method over the range.
type MethodSum struct {
	Method   string `json:"payment_method"`
	Payments string `json:"payments"`
	Refunds  string `json:"refunds"`
	Net      string `json:"net"`
}

// Cashier is the cashier report: payments by method and business date.
type Cashier struct {
	From     civil.Date    `json:"from"`
	To       civil.Date    `json:"to"`
	Lines    []CashierLine `json:"lines"`
	ByMethod []MethodSum   `json:"by_method"`
	Net      string        `json:"net"`
}

func (r Cashier) CSV() ([]string, [][]string) {
	h := []string{"business_date", "payment_method", "payments", "refunds", "net", "count", "voided", "voided_count"}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{l.BusinessDate.String(), l.Method, l.Payments, l.Refunds, l.Net, itoa(l.Count), l.Voided, itoa(l.VoidedCount)})
	}
	return h, rows
}

// ---------------------------------------------------------------- statistics

// StatDay is one closed business day of the statistics.
type StatDay struct {
	BusinessDate     civil.Date `json:"business_date"`
	RoomsTotal       int        `json:"rooms_total"`
	OutOfOrder       int        `json:"rooms_out_of_order"`
	Sellable         int        `json:"rooms_sellable"`
	Occupied         int        `json:"rooms_occupied"`
	RoomNightsSold   int        `json:"room_nights_sold"`
	Arrivals         int        `json:"arrivals"`
	Departures       int        `json:"departures"`
	NoShows          int        `json:"no_shows"`
	RoomRevenue      string     `json:"room_revenue"`
	OccupancyPercent string     `json:"occupancy_percent"`
	ADR              string     `json:"adr"`
	RevPAR           string     `json:"revpar"`
}

// StatTotals aggregates the range: occupancy over all available room nights, ADR and RevPAR from the summed revenue.
type StatTotals struct {
	Days             int    `json:"days"`
	AvailableNights  int    `json:"available_room_nights"`
	OccupiedNights   int    `json:"occupied_room_nights"`
	RoomNightsSold   int    `json:"room_nights_sold"`
	RoomRevenue      string `json:"room_revenue"`
	OccupancyPercent string `json:"occupancy_percent"`
	ADR              string `json:"adr"`
	RevPAR           string `json:"revpar"`
}

// Statistics is the occupancy and statistics report over closed days.
type Statistics struct {
	From   civil.Date `json:"from"`
	To     civil.Date `json:"to"`
	Days   []StatDay  `json:"days"`
	Totals StatTotals `json:"totals"`
}

func (r Statistics) CSV() ([]string, [][]string) {
	h := []string{"business_date", "rooms_total", "rooms_out_of_order", "rooms_sellable", "rooms_occupied", "room_nights_sold", "arrivals", "departures", "no_shows", "room_revenue", "occupancy_percent", "adr", "revpar"}
	var rows [][]string
	for _, d := range r.Days {
		rows = append(rows, []string{d.BusinessDate.String(), itoa(d.RoomsTotal), itoa(d.OutOfOrder), itoa(d.Sellable), itoa(d.Occupied), itoa(d.RoomNightsSold),
			itoa(d.Arrivals), itoa(d.Departures), itoa(d.NoShows), d.RoomRevenue, d.OccupancyPercent, d.ADR, d.RevPAR})
	}
	return h, rows
}

// ---------------------------------------------------------------- lists

// StayRow is a row of the in-house and departures reports.
type StayRow struct {
	StayID             int64      `json:"stay_id"`
	StayNumber         string     `json:"stay_number"`
	Status             string     `json:"status,omitempty"`
	ConfirmationNumber string     `json:"confirmation_number"`
	Guest              string     `json:"guest"`
	Room               string     `json:"room,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	Adults             int        `json:"adult_count"`
	Children           int        `json:"child_count"`
	Balance            string     `json:"balance"`
}

// StayList is the in-house or departures report.
type StayList struct {
	Date *civil.Date `json:"date,omitempty"`
	Rows []StayRow   `json:"rows"`
}

func (r StayList) CSV() ([]string, [][]string) {
	h := []string{"stay_number", "status", "confirmation_number", "guest", "room", "arrival_date", "departure_date", "adults", "children", "balance"}
	var rows [][]string
	for _, s := range r.Rows {
		rows = append(rows, []string{s.StayNumber, s.Status, s.ConfirmationNumber, s.Guest, s.Room, s.ArrivalDate.String(), s.DepartureDate.String(), itoa(s.Adults), itoa(s.Children), s.Balance})
	}
	return h, rows
}

// ArrivalRow is a row of the arrivals report (any status).
type ArrivalRow struct {
	ReservationRoomID  int64      `json:"reservation_room_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	Status             string     `json:"status"`
	Guest              string     `json:"guest"`
	RoomType           string     `json:"room_type"`
	Room               string     `json:"room,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	Adults             int        `json:"adult_count"`
	Children           int        `json:"child_count"`
}

// ArrivalList is the arrivals report.
type ArrivalList struct {
	Date civil.Date   `json:"date"`
	Rows []ArrivalRow `json:"rows"`
}

func (r ArrivalList) CSV() ([]string, [][]string) {
	h := []string{"confirmation_number", "status", "guest", "room_type", "room", "arrival_date", "departure_date", "adults", "children"}
	var rows [][]string
	for _, a := range r.Rows {
		rows = append(rows, []string{a.ConfirmationNumber, a.Status, a.Guest, a.RoomType, a.Room, a.ArrivalDate.String(), a.DepartureDate.String(), itoa(a.Adults), itoa(a.Children)})
	}
	return h, rows
}

// ---------------------------------------------------------------- helpers

func itoa(n int) string { return strconv.Itoa(n) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func name(first *string, last string) string {
	if first != nil && *first != "" {
		return *first + " " + last
	}
	return last
}

// SafeCell neutralises spreadsheet formulas in an exported cell: text starting with = + - @ or a control
// character is prefixed with an apostrophe, except plain numbers (amounts can be negative).
func SafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if _, err := decimal.NewFromString(strings.TrimSpace(s)); err == nil {
			return s
		}
		return "'" + s
	}
	return s
}

func parseSummary(raw []byte) (*nightaudit.Summary, bool) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, false
	}
	var s nightaudit.Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return &s, true
}
