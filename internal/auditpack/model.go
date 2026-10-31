// Package auditpack builds the night audit pack: the fifteen sections of a closed business day, kept as JSON when the night audit closes the day
// (docs/architecture/38-reports-night-audit-pack.md, 5 and 8). A pack is a snapshot: it is read back as it was, never recomputed, except for a section that failed (a rebuild) and a
// day that has no pack yet (a late build, which cannot know what only the close of that night saw).
package auditpack

import "time"

// FormatVersion is the version of the JSON of the sections. A change of the shape of a section raises it; a pack keeps the version it was built with.
const FormatVersion = 1

// The sections of a pack, in the order of the pack and its PDF.
const (
	SecSummary       = "SUMMARY"
	SecFlash         = "FLASH"
	SecRevenue       = "REVENUE"
	SecTax           = "TAX"
	SecPayments      = "PAYMENTS"
	SecZReports      = "Z_REPORTS"
	SecGuestLedger   = "GUEST_LEDGER"
	SecDepositLedger = "DEPOSIT_LEDGER"
	SecCorrections   = "CORRECTIONS"
	SecCancellations = "CANCELLATIONS"
	SecOutlets       = "OUTLETS"
	SecOutOfOrder    = "OUT_OF_ORDER"
	SecInHouse       = "IN_HOUSE"
	SecNextDay       = "NEXT_DAY"
	SecAuditNotes    = "AUDIT_NOTES"
)

// The status of a section and of a pack.
const (
	StatusOK           = "OK"
	StatusFailed       = "FAILED"
	StatusOff          = "OFF"           // the section does not apply to this property (no point of sale, no cashier shift)
	StatusNotAvailable = "NOT_AVAILABLE" // a late build cannot know it: only the close of that night saw it

	PackComplete = "COMPLETE"
	PackPartial  = "PARTIAL"
)

// Def is a section of the catalog: its code, and whether the ledger can make it again for a day that is closed.
type Def struct {
	Code        string
	Rebuildable bool
}

// Catalog lists the sections in the order of the pack. IN_HOUSE, NEXT_DAY, AUDIT_NOTES and OUT_OF_ORDER are what only the close of the night saw: the stays in house, the arrivals of
// tomorrow, the findings of the audit, and the blocks of rooms of that night. They are not in the ledger, so they cannot be built again.
var Catalog = []Def{
	{SecSummary, true}, {SecFlash, true}, {SecRevenue, true}, {SecTax, true}, {SecPayments, true}, {SecZReports, true}, {SecGuestLedger, true}, {SecDepositLedger, true},
	{SecCorrections, true}, {SecCancellations, true}, {SecOutlets, true}, {SecOutOfOrder, false}, {SecInHouse, false}, {SecNextDay, false}, {SecAuditNotes, false},
}

// Known says whether a code is a section of the catalog.
func Known(code string) bool {
	for _, d := range Catalog {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Rebuildable says whether the ledger can make a section again.
func Rebuildable(code string) bool {
	for _, d := range Catalog {
		if d.Code == code {
			return d.Rebuildable
		}
	}
	return false
}

// MaxErrorLength is the length of the reason a section failed, as the column holds it.
const MaxErrorLength = 300

// Section is one section of a pack as it is stored. Data is the JSON of an OK section.
type Section struct {
	Code          string `json:"section"`
	Status        string `json:"status"`
	FormatVersion int16  `json:"format_version"`
	Data          []byte `json:"-"`
	Error         string `json:"error,omitempty"`
	BuiltLate     bool   `json:"built_late"`
}

// Pack is the header of a pack and its sections.
type Pack struct {
	ID            int64     `json:"-"`
	BusinessDate  string    `json:"business_date"`
	FormatVersion int16     `json:"format_version"`
	Status        string    `json:"status"`
	BuiltAt       time.Time `json:"built_at"`
	BuildMS       int32     `json:"build_ms"`
	BuiltLate     bool      `json:"built_late"`
	Sections      []Section `json:"sections"`
}
