// Package nightaudit is NightAuditService (docs/architecture/04-operations.md Step 12): an orchestrator. It asks
// checks for blockers, delegates posting to the room charge service, housekeeping to the housekeeping service and
// closing of the day to the business day service. It contains no calculation or posting code and never changes
// guest, reservation or stay status (the bulk no-show is an explicit staff action carried out by reservations).
package nightaudit

import (
	"time"

	"kamarapms/internal/accounting"
	"kamarapms/internal/expected"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/shifts"
)

// Arrival is an unresolved arrival: a CONFIRMED room whose arrival date has come.
type Arrival struct {
	ReservationRoomID  int64      `json:"reservation_room_id"`
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	Guest              string     `json:"guest"`
	RoomType           string     `json:"room_type"`
	Room               string     `json:"room,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
}

// Departure is an unresolved departure: an OPEN stay that should have left.
type Departure struct {
	StayID        int64      `json:"stay_id"`
	StayNumber    string     `json:"stay_number"`
	Room          string     `json:"room,omitempty"`
	Guest         string     `json:"guest"`
	DepartureDate civil.Date `json:"departure_date"`
}

// Blockers are the findings that stop the run (checks 2, 3, 4 and 6, a cashier shift still open when the property asks for the cash to be counted first, and an accounting setup that could not make the journal of the day).
type Blockers struct {
	UnresolvedArrivals   []Arrival           `json:"unresolved_arrivals"`
	UnresolvedDepartures []Departure         `json:"unresolved_departures"`
	ChargeErrors         []roomcharge.Result `json:"charge_errors"`
	InvalidCharges       []expected.Invalid  `json:"invalid_charges"`
	OpenShifts           []shifts.OpenShift  `json:"open_shifts"`
	// AccountingReadiness are the reasons the day close could not make its journal (accounting not set up, the system accounts, the room revenue account, the departments).
	AccountingReadiness []accounting.ReadinessBlocker `json:"accounting_readiness"`
	// OutletBills are the bills of the outlets that are still open with items sold, for an outlet that asks for every bill to be closed before the day closes (34-pos.md 13.1).
	OutletBills []OutletIssue `json:"outlet_bills"`
	// Stock are the findings of the inventory that stop the close: a balance that disagrees with the ledger, a stock value that disagrees with the general ledger (35-inventory.md 5.7).
	Stock []OutletIssue `json:"stock"`
}

// OutletIssue is a finding about the bills of the outlets (and, for the stock, about the inventory).
type OutletIssue struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Ref     string            `json:"ref,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

// Any reports whether something blocks the run.
func (b Blockers) Any() bool {
	return len(b.UnresolvedArrivals)+len(b.UnresolvedDepartures)+len(b.ChargeErrors)+len(b.InvalidCharges)+len(b.OpenShifts)+len(b.AccountingReadiness)+len(b.OutletBills)+len(b.Stock) > 0
}

// MissingCharges are READY nights before the business date (the run posts them).
type MissingCharges struct {
	Count int                 `json:"count"`
	Items []roomcharge.Result `json:"items"`
}

// TonightCharges are READY nights of the business date.
type TonightCharges struct {
	Count int    `json:"count"`
	Total string `json:"total"`
}

// StaleDraft is a draft room line whose arrival date has passed.
type StaleDraft struct {
	ReservationRoomID  int64      `json:"reservation_room_id"`
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	ArrivalDate        civil.Date `json:"arrival_date"`
}

// ClosedMaster is a group that routes to a master folio that is closed (or was never opened): the rooms in house go to their own folios until it is reopened.
type ClosedMaster struct {
	GroupID     int64  `json:"group_id"`
	GroupCode   string `json:"group_code"`
	GroupName   string `json:"group_name"`
	FolioNumber string `json:"folio_number"`
	Stays       int    `json:"stays"`
}

// OpenMaster is the master folio of a group that has left, still open, with its balance.
type OpenMaster struct {
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
	GroupCode   string `json:"group_code"`
	GroupName   string `json:"group_name"`
	Balance     string `json:"balance"`
}

// DeadFolio is an open folio with a balance on a reservation without any live room.
type DeadFolio struct {
	FolioID            int64  `json:"folio_id"`
	FolioNumber        string `json:"folio_number"`
	ConfirmationNumber string `json:"confirmation_number"`
	Balance            string `json:"balance"`
}

// EndingBlock is a room block that ends on the business date.
type EndingBlock struct {
	BlockID   int64      `json:"block_id"`
	BlockType string     `json:"block_type"`
	Room      string     `json:"room"`
	EndDate   civil.Date `json:"end_date"`
}

// Warnings never stop the run.
type Warnings struct {
	StaleDrafts []StaleDraft `json:"stale_drafts"`
	OpenFolios  []DeadFolio  `json:"open_folios_of_cancelled_reservations"`
	// OpenMasters are master folios of groups that have left (every room over) and are still open: a hotel may bill a group days later, so it is a warning, never a blocker.
	OpenMasters []OpenMaster `json:"open_group_masters"`
	// ClosedMasters are groups whose instructions point at a master folio that is closed: their nights go to the guest folios, which the audit allows and says.
	ClosedMasters []ClosedMaster `json:"group_masters_closed"`
	BlocksEnding  []EndingBlock  `json:"blocks_ending"`
	// OutletBills are open bills of the outlets with items sold, or with lines that were never sent: the sale is revenue of the day and the money is not counted yet.
	OutletBills []OutletIssue `json:"outlet_bills"`
	// Stock are the findings of the inventory that do not stop the close (stock sold below zero).
	Stock []OutletIssue `json:"stock"`
}

// Preview is the full pre-check (nothing is written).
type Preview struct {
	BusinessDate      civil.Date     `json:"business_date"`
	PropertyLocalTime string         `json:"property_local_time"`
	TimeGuardOK       bool           `json:"time_guard_ok"`
	AllowedFrom       time.Time      `json:"night_audit_allowed_from"`
	CanRun            bool           `json:"can_run"`
	Blockers          Blockers       `json:"blockers"`
	MissingCharges    MissingCharges `json:"missing_charges"`
	TonightCharges    TonightCharges `json:"tonight_charges"`
	Warnings          Warnings       `json:"warnings"`
}

// RoomCounts of the closing summary.
type RoomCounts struct {
	Total        int `json:"total"`
	OutOfOrder   int `json:"out_of_order"`
	OutOfService int `json:"out_of_service"`
	Sellable     int `json:"sellable"`
	// Occupied is the rooms occupied by guests, complimentary ones included; the rooms the hotel uses itself (HouseUse)
	// are neither occupied nor sellable nor available. Sold is the paid room nights.
	Occupied      int `json:"occupied"`
	Complimentary int `json:"complimentary"`
	HouseUse      int `json:"house_use"`
	Sold          int `json:"sold"`
}

// Money is a net, service charge and tax triple.
type Money struct {
	Net     string `json:"net"`
	Service string `json:"service"`
	Tax     string `json:"tax"`
}

// TypeRevenue is revenue posted on the day for one charge type.
type TypeRevenue struct {
	ChargeType string `json:"charge_type"`
	Money
}

// MethodTotal is the cashier total of one payment method on the day.
type MethodTotal struct {
	Method   string `json:"method"`
	Payments string `json:"payments"`
	Refunds  string `json:"refunds"`
	Net      string `json:"net"`
}

// CityLedger is the day's movement of the company accounts: transfers from folios, receipts, and the balance owed
// at the end of the day. Transfers are not in PaymentsByMethod (they are not money received).
type CityLedger struct {
	Transferred string `json:"transferred"`
	Received    string `json:"received"`
	// Adjusted is what credit notes and write-offs of the day took off what companies owe.
	Adjusted    string `json:"adjusted,omitempty"`
	Outstanding string `json:"outstanding"`
}

// Summary is the daily closing summary stored in business_days.summary.
type Summary struct {
	BusinessDate        civil.Date    `json:"business_date"`
	Rooms               RoomCounts    `json:"rooms"`
	Arrivals            int           `json:"arrivals"`
	Departures          int           `json:"departures"`
	NoShows             int           `json:"no_shows"`
	RoomRevenue         Money         `json:"room_revenue"`
	RevenueByChargeType []TypeRevenue `json:"revenue_by_charge_type"`
	PaymentsByMethod    []MethodTotal `json:"payments_by_method"`
	CityLedger          CityLedger    `json:"city_ledger"`
	OccupancyPercent    string        `json:"occupancy_percent"`
	ADR                 string        `json:"adr"`
	RevPAR              string        `json:"revpar"`
	RoomChargesPosted   int           `json:"room_charges_posted"`
}

// RunResult is a completed night audit.
type RunResult struct {
	ClosedBusinessDate civil.Date `json:"closed_business_date"`
	NewBusinessDate    civil.Date `json:"new_business_date"`
	RoomChargesPosted  int        `json:"room_charges_posted"`
	Summary            Summary    `json:"summary"`
}

// NoShowResult is the answer of a bulk no-show.
type NoShowResult struct {
	Marked            any      `json:"marked"`
	RemainingBlockers Blockers `json:"remaining_blockers"`
}

func name(first *string, last string) string {
	if first != nil && *first != "" {
		return *first + " " + last
	}
	return last
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
