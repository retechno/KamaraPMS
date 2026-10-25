// Package banquet is the meeting rooms, the events and their sessions (docs/architecture/36-banquet.md), PR 1: the masters and the schedule, no money.
// Every route is of the BANQUET module of the plan.
package banquet

import (
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The statuses of an event (36 4.3).
const (
	StatusInquiry   = "INQUIRY"
	StatusTentative = "TENTATIVE"
	StatusDefinite  = "DEFINITE"
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"
	StatusLost      = "LOST"
)

// The kinds of a hold: an option queues, a definite hold is exclusive (the exclusion constraint of the database).
const (
	HoldOption   = "OPTION"
	HoldDefinite = "DEFINITE"
)

// The conflict modes of the options (the setting).
const (
	ModeReject = "REJECT"
	ModeQueue  = "QUEUE"
)

// The kinds of a notice.
const (
	NoticePromoted = "OPTION_PROMOTED"
	NoticeDue      = "OPTION_DUE"
	NoticeLost     = "OPTION_LOST"
)

// The kinds of a reason.
const (
	ReasonLost   = "LOST"
	ReasonCancel = "CANCEL"
)

// SetupStyles are the arrangements of a room.
var SetupStyles = []string{"THEATER", "CLASSROOM", "U_SHAPE", "ROUND_TABLE", "COCKTAIL", "BOARDROOM"}

var (
	itemKinds  = []string{"MENU", "DRINK_PACKAGE", "ROOM_RENTAL", "EQUIPMENT", "DECOR", "OTHER"}
	unitBasis  = []string{"PER_PAX", "PER_SESSION", "PER_HOUR", "PER_UNIT"}
	fbClasses  = []string{"FOOD", "BEVERAGE", "OTHER"}
	codePatten = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)
)

const (
	maxName = 100
	maxSort = 100000
	maxPax  = 100000
	maxRows = 500
)

func normalCode(c string) string { return strings.ToUpper(strings.TrimSpace(c)) }

// HoldRange is the local time a session holds on a room: from the start of the set-up to the end of the tear-down (36 4.2). The end is on the next day when the session ends after midnight.
func HoldRange(date civil.Date, start, end civil.TimeOfDay, endsNextDay bool, setupMinutes, teardownMinutes int) (from, to time.Time) {
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	from = day.Add(time.Duration(start.Hour()*60+start.Minute()-setupMinutes) * time.Minute)
	toDay := day
	if endsNextDay {
		toDay = day.AddDate(0, 0, 1)
	}
	to = toDay.Add(time.Duration(end.Hour()*60+end.Minute()+teardownMinutes) * time.Minute)
	return from, to
}

// Overlap reports whether two half-open ranges share time.
func Overlap(aFrom, aTo, bFrom, bTo time.Time) bool { return aFrom.Before(bTo) && bFrom.Before(aTo) }

// ---------------------------------------------------------------- masters

// Settings are the banquet settings of the property; no row means the defaults.
type Settings struct {
	TentativeConflictMode string           `json:"tentative_conflict_mode"`
	OperatingFrom         *civil.TimeOfDay `json:"operating_from"`
	OperatingTo           *civil.TimeOfDay `json:"operating_to"`
	// The money (PR 2): who posts the sessions (AUTO at the close, MANUAL by the office), the mode a new line starts with, and the discount (percent) that needs no approval.
	PostingMode              string          `json:"posting_mode"`
	DefaultPriceMode         string          `json:"default_price_mode"`
	MaxDiscountPctNoApproval decimal.Decimal `json:"max_discount_pct_no_approval"`
}

// SettingsInput changes the settings.
type SettingsInput struct {
	TentativeConflictMode string           `json:"tentative_conflict_mode"`
	OperatingFrom         *civil.TimeOfDay `json:"operating_from"`
	OperatingTo           *civil.TimeOfDay `json:"operating_to"`
	// A money setting that is left out keeps its value.
	PostingMode              string           `json:"posting_mode"`
	DefaultPriceMode         string           `json:"default_price_mode"`
	MaxDiscountPctNoApproval *decimal.Decimal `json:"max_discount_pct_no_approval"`
}

// RoomPart is a base room of a combined room.
type RoomPart struct {
	RoomID int64  `json:"room_id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
}

// RoomSetup is the capacity of a room in a style of set-up.
type RoomSetup struct {
	SetupStyle string `json:"setup_style"`
	Capacity   int    `json:"capacity"`
}

// Room is a meeting room, base or combined.
type Room struct {
	ID              int64            `json:"id"`
	Code            string           `json:"code"`
	Name            string           `json:"name"`
	AreaM2          *decimal.Decimal `json:"area_m2"`
	SetupMinutes    int              `json:"setup_minutes"`
	TeardownMinutes int              `json:"teardown_minutes"`
	IsCombo         bool             `json:"is_combo"`
	IsActive        bool             `json:"is_active"`
	SortOrder       int              `json:"sort_order"`
	Version         int32            `json:"version"`
	Parts           []RoomPart       `json:"parts"`
	Setups          []RoomSetup      `json:"setups"`
}

// RoomInput creates or changes a room. A change carries the version it was read at.
type RoomInput struct {
	Code            string           `json:"code"`
	Name            string           `json:"name"`
	AreaM2          *decimal.Decimal `json:"area_m2"`
	SetupMinutes    int              `json:"setup_minutes"`
	TeardownMinutes int              `json:"teardown_minutes"`
	IsCombo         bool             `json:"is_combo"`
	IsActive        *bool            `json:"is_active"`
	SortOrder       int              `json:"sort_order"`
	Version         int32            `json:"version"`
}

// PartsInput sets the base rooms of a combined room.
type PartsInput struct {
	RoomIDs []int64 `json:"room_ids"`
}

// SetupsInput sets the capacities of a room.
type SetupsInput struct {
	Setups []RoomSetup `json:"setups"`
}

// EventType is a kind of event (wedding, meeting, gala dinner).
type EventType struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

// EventTypeInput creates or changes an event type.
type EventTypeInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Reason is why an event was lost or cancelled.
type Reason struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

// ReasonInput creates or changes a reason.
type ReasonInput struct {
	Kind     string `json:"kind"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Item is something an event can be sold: a menu, a drink package, the rental of a room, equipment, decoration.
type Item struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	UnitBasis      string `json:"unit_basis"`
	ChargeCodeID   int64  `json:"charge_code_id"`
	ChargeCode     string `json:"charge_code"`
	ChargeCodeName string `json:"charge_code_name"`
	// PriceMode is the mode of the charge code, the mode a line of this item starts with: INCLUSIVE (nett) or EXCLUSIVE (++).
	PriceMode    string          `json:"price_mode"`
	ListPrice    decimal.Decimal `json:"list_price"`
	FbClass      *string         `json:"fb_class"`
	PosProductID *int64          `json:"pos_product_id"`
	Description  *string         `json:"description"`
	IsActive     bool            `json:"is_active"`
}

// ItemInput creates or changes an item.
type ItemInput struct {
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	UnitBasis    string          `json:"unit_basis"`
	ChargeCodeID int64           `json:"charge_code_id"`
	ListPrice    decimal.Decimal `json:"list_price"`
	FbClass      *string         `json:"fb_class"`
	PosProductID *int64          `json:"pos_product_id"`
	Description  *string         `json:"description"`
	IsActive     *bool           `json:"is_active"`
}

// PackageComponent is an item of a package, for each guest.
type PackageComponent struct {
	ItemID   int64  `json:"item_id"`
	ItemCode string `json:"item_code"`
	ItemName string `json:"item_name"`
	ItemKind string `json:"item_kind"`
	// PriceMode is the mode of the charge code of the item.
	PriceMode string          `json:"price_mode"`
	QtyPerPax decimal.Decimal `json:"qty_per_pax"`
	SortOrder int             `json:"sort_order"`
}

// Package is a price for each guest made of items.
type Package struct {
	ID          int64           `json:"id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	PricePerPax decimal.Decimal `json:"price_per_pax"`
	// PriceMode is the mode all the components share (INCLUSIVE or EXCLUSIVE), MIXED when their charge codes differ, nothing for a package without components.
	PriceMode   *string            `json:"price_mode"`
	Description *string            `json:"description"`
	IsActive    bool               `json:"is_active"`
	Components  []PackageComponent `json:"components"`
}

// ComponentInput is a component of a package in a change.
type ComponentInput struct {
	ItemID    int64           `json:"item_id"`
	QtyPerPax decimal.Decimal `json:"qty_per_pax"`
}

// PackageInput creates or changes a package; the components replace the ones it has.
type PackageInput struct {
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	PricePerPax decimal.Decimal  `json:"price_per_pax"`
	Description *string          `json:"description"`
	IsActive    *bool            `json:"is_active"`
	Components  []ComponentInput `json:"components"`
}

// ---------------------------------------------------------------- events

// Session is a day, a time and a room of an event.
type Session struct {
	ID            int64            `json:"id"`
	SessionNo     int              `json:"session_no"`
	Name          string           `json:"name"`
	SessionDate   civil.Date       `json:"session_date"`
	StartTime     *civil.TimeOfDay `json:"start_time"`
	EndTime       *civil.TimeOfDay `json:"end_time"`
	EndsNextDay   bool             `json:"ends_next_day"`
	RoomID        *int64           `json:"room_id"`
	RoomCode      *string          `json:"room_code"`
	RoomName      *string          `json:"room_name"`
	SetupStyle    *string          `json:"setup_style"`
	PaxEstimated  int              `json:"pax_estimated"`
	PaxGuaranteed *int             `json:"pax_guaranteed"`
	PaxActual     *int             `json:"pax_actual"`
	Status        string           `json:"status"`
	Notes         *string          `json:"notes"`
	// The queue (an option of a tentative event): 1 holds the room; room_taken says that a definite event of someone else covers the slot.
	Rank      *int `json:"rank"`
	RoomTaken bool `json:"room_taken"`
	// HoldFrom and HoldTo are the local time held, with the set-up and the tear-down, "2026-11-07T11:00" (empty when nothing is held).
	HoldFrom *string `json:"hold_from"`
	HoldTo   *string `json:"hold_to"`
}

// Event is an event of the property.
type Event struct {
	ID               int64       `json:"id"`
	Number           string      `json:"number"`
	Status           string      `json:"status"`
	Name             string      `json:"name"`
	EventTypeID      *int64      `json:"event_type_id"`
	EventTypeName    *string     `json:"event_type_name"`
	BookerGuestID    *int64      `json:"booker_guest_id"`
	BookerCompanyID  *int64      `json:"booker_company_id"`
	BookerName       string      `json:"booker_name"`
	ContactName      *string     `json:"contact_name"`
	ContactPhone     *string     `json:"contact_phone"`
	ContactEmail     *string     `json:"contact_email"`
	SalesUserID      int64       `json:"sales_user_id"`
	SalesName        string      `json:"sales_name"`
	OptionUntil      *civil.Date `json:"option_until"`
	OptionOverdue    bool        `json:"option_overdue"`
	ReasonID         *int64      `json:"reason_id"`
	ReasonName       *string     `json:"reason_name"`
	ReasonNote       *string     `json:"reason_note"`
	BookingGroupID   *int64      `json:"booking_group_id"`
	BookingGroupName *string     `json:"booking_group_name"`
	Notes            *string     `json:"notes"`
	InquiryAt        time.Time   `json:"inquiry_at"`
	DefiniteAt       *time.Time  `json:"definite_at"`
	ClosedAt         *time.Time  `json:"closed_at"`
	Version          int32       `json:"version"`
	Sessions         []Session   `json:"sessions"`
}

// EventRow is an event in the list.
type EventRow struct {
	ID            int64       `json:"id"`
	Number        string      `json:"number"`
	Status        string      `json:"status"`
	Name          string      `json:"name"`
	EventTypeID   *int64      `json:"event_type_id"`
	EventTypeName *string     `json:"event_type_name"`
	BookerName    string      `json:"booker_name"`
	SalesUserID   int64       `json:"sales_user_id"`
	SalesName     string      `json:"sales_name"`
	OptionUntil   *civil.Date `json:"option_until"`
	OptionOverdue bool        `json:"option_overdue"`
	FirstDate     *civil.Date `json:"first_date"`
	LastDate      *civil.Date `json:"last_date"`
	Sessions      int         `json:"sessions"`
	PaxEstimated  int         `json:"pax_estimated"`
	// Pax is the number of guests counted for the event: the sum over its sessions of the guaranteed number where there is one, else the estimate; PaxGuaranteed says every session has a guarantee.
	Pax           int   `json:"pax"`
	PaxGuaranteed bool  `json:"pax_guaranteed"`
	Version       int32 `json:"version"`
}

// EventFilter narrows the list of events.
type EventFilter struct {
	Statuses      []string
	SalesUserID   *int64
	EventTypeID   *int64
	From, To      *civil.Date
	OptionOverdue bool
	Query         string
	Limit         int
}

// EventInput creates or changes an event. A change carries the version it was read at and replaces the fields.
type EventInput struct {
	Name            string      `json:"name"`
	EventTypeID     *int64      `json:"event_type_id"`
	BookerGuestID   *int64      `json:"booker_guest_id"`
	BookerCompanyID *int64      `json:"booker_company_id"`
	ContactName     *string     `json:"contact_name"`
	ContactPhone    *string     `json:"contact_phone"`
	ContactEmail    *string     `json:"contact_email"`
	SalesUserID     *int64      `json:"sales_user_id"`
	BookingGroupID  *int64      `json:"booking_group_id"`
	Notes           *string     `json:"notes"`
	OptionUntil     *civil.Date `json:"option_until"`
	Version         int32       `json:"version"`
}

// SessionInput adds or changes a session. AcceptCapacity confirms a number of guests above the capacity of the room in the set-up (a business decision, not an error).
type SessionInput struct {
	Name           string           `json:"name"`
	SessionDate    civil.Date       `json:"session_date"`
	StartTime      *civil.TimeOfDay `json:"start_time"`
	EndTime        *civil.TimeOfDay `json:"end_time"`
	EndsNextDay    bool             `json:"ends_next_day"`
	RoomID         *int64           `json:"room_id"`
	SetupStyle     *string          `json:"setup_style"`
	PaxEstimated   int              `json:"pax_estimated"`
	PaxGuaranteed  *int             `json:"pax_guaranteed"`
	Notes          *string          `json:"notes"`
	AcceptCapacity bool             `json:"accept_capacity"`
}

// TentativeInput makes an inquiry tentative: the date the option is held until.
type TentativeInput struct {
	OptionUntil civil.Date `json:"option_until"`
}

// CloseInput loses or cancels an event: the reason is required. The money of the close (PR 2) is optional: the amount of the fee or of the forfeit the person confirms (nothing: the fee of the policy
// for a definite event, nothing for the others), the action (FEE, or FORFEIT of the deposits received) and, for an amount below the policy, why.
type CloseInput struct {
	ReasonID   int64            `json:"reason_id"`
	ReasonNote *string          `json:"reason_note"`
	Amount     *decimal.Decimal `json:"amount"`
	Action     string           `json:"action"`
	FeeReason  *string          `json:"fee_reason"`
}

// AvailabilityInput asks for the room check of a session before it is saved (a dry run).
type AvailabilityInput struct {
	SessionID   *int64          `json:"session_id"`
	RoomID      int64           `json:"room_id"`
	SessionDate civil.Date      `json:"session_date"`
	StartTime   civil.TimeOfDay `json:"start_time"`
	EndTime     civil.TimeOfDay `json:"end_time"`
	EndsNextDay bool            `json:"ends_next_day"`
	SetupStyle  *string         `json:"setup_style"`
	Pax         *int            `json:"pax"`
}

// Conflict is a hold that a session would meet.
type Conflict struct {
	EventID     int64  `json:"event_id"`
	EventNumber string `json:"event_number"`
	EventName   string `json:"event_name"`
	EventStatus string `json:"event_status"`
	SessionID   int64  `json:"session_id"`
	SessionName string `json:"session_name"`
	HoldKind    string `json:"hold_kind"`
	RoomCode    string `json:"room_code"`
	// Rank is the place of an option in the queue of its slot (1 is the first); empty for a confirmed event.
	Rank      *int `json:"rank"`
	SameEvent bool `json:"same_event"`
}

// Availability is the answer of the room check: the holds in the way, the mode, and the rank the session would have (and the one it gives up when it moves).
type Availability struct {
	Mode            string     `json:"mode"`
	Available       bool       `json:"available"`
	Conflicts       []Conflict `json:"conflicts"`
	RankInNewSlot   int        `json:"rank_in_new_slot"`
	RankGivenUp     *int       `json:"rank_given_up"`
	Capacity        *int       `json:"capacity"`
	CapacityExceeds bool       `json:"capacity_exceeds"`
	SetupOffered    bool       `json:"setup_offered"`
	HoldFrom        string     `json:"hold_from"`
	HoldTo          string     `json:"hold_to"`
}

// CalendarSession is a session on the calendar of the rooms.
type CalendarSession struct {
	SessionID     int64           `json:"session_id"`
	EventID       int64           `json:"event_id"`
	EventNumber   string          `json:"event_number"`
	EventName     string          `json:"event_name"`
	EventStatus   string          `json:"event_status"`
	EventTypeName string          `json:"event_type_name"`
	BookerName    string          `json:"booker_name"`
	SalesUserID   int64           `json:"sales_user_id"`
	OptionUntil   *civil.Date     `json:"option_until"`
	OptionOverdue bool            `json:"option_overdue"`
	SessionName   string          `json:"session_name"`
	SessionDate   civil.Date      `json:"session_date"`
	StartTime     civil.TimeOfDay `json:"start_time"`
	EndTime       civil.TimeOfDay `json:"end_time"`
	EndsNextDay   bool            `json:"ends_next_day"`
	RoomID        int64           `json:"room_id"`
	RoomCode      string          `json:"room_code"`
	RoomName      string          `json:"room_name"`
	SetupMinutes  int             `json:"setup_minutes"`
	TeardownMin   int             `json:"teardown_minutes"`
	SetupStyle    *string         `json:"setup_style"`
	PaxEstimated  int             `json:"pax_estimated"`
	Rank          *int            `json:"rank"`
	RoomTaken     bool            `json:"room_taken"`
}

// CalendarLock is the room a hold locks without the session being booked in it: the parts of a combined room, the combined rooms of a part. HoldFrom and HoldTo are local, with the set-up and
// the tear-down, "2026-11-07T11:00".
type CalendarLock struct {
	RoomID      int64  `json:"room_id"`
	SessionID   int64  `json:"session_id"`
	EventID     int64  `json:"event_id"`
	EventNumber string `json:"event_number"`
	EventName   string `json:"event_name"`
	EventStatus string `json:"event_status"`
	HoldKind    string `json:"hold_kind"`
	ViaRoomID   int64  `json:"via_room_id"`
	ViaRoomCode string `json:"via_room_code"`
	ViaRoomName string `json:"via_room_name"`
	HoldFrom    string `json:"hold_from"`
	HoldTo      string `json:"hold_to"`
}

// Calendar is the rooms and the sessions of a period.
type Calendar struct {
	From     civil.Date        `json:"from"`
	To       civil.Date        `json:"to"`
	Rooms    []Room            `json:"rooms"`
	Sessions []CalendarSession `json:"sessions"`
	Locks    []CalendarLock    `json:"locks"`
}

// OverdueOption is a tentative event whose option date has passed.
type OverdueOption struct {
	EventID     int64      `json:"event_id"`
	Number      string     `json:"number"`
	Name        string     `json:"name"`
	OptionUntil civil.Date `json:"option_until"`
	SalesUserID int64      `json:"sales_user_id"`
	SalesName   string     `json:"sales_name"`
}

// Today is what the banquet office looks at in the morning: the sessions of the business date and the options that are due.
type Today struct {
	Date     civil.Date        `json:"date"`
	Sessions []CalendarSession `json:"sessions"`
	Overdue  []OverdueOption   `json:"overdue_options"`
}

// Notice is a message of the bell of a user.
type Notice struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	EventID     int64      `json:"event_id"`
	EventNumber string     `json:"event_number"`
	EventName   string     `json:"event_name"`
	SessionID   *int64     `json:"session_id"`
	NoticeDate  civil.Date `json:"notice_date"`
	CreatedAt   time.Time  `json:"created_at"`
	SeenAt      *time.Time `json:"seen_at"`
}

// NoticesSeenInput marks notices as seen: the ids, or all when it is empty.
type NoticesSeenInput struct {
	IDs []int64 `json:"ids"`
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}
