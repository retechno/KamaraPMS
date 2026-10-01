package auth

// Permission is a property-scoped capability granted through a role.
// Tenant-level administration (users, roles, creating properties) requires a
// tenant administrator instead of a permission.
type Permission string

// PermissionInfo describes a permission for the role editor.
type PermissionInfo struct {
	Code        Permission `json:"code"`
	Group       string     `json:"group"`
	Description string     `json:"description"`
	Milestone   string     `json:"milestone"` // when the guarded feature arrives
}

const (
	PermPropertyManage Permission = "property.manage"

	PermRoomManage          Permission = "room.manage"
	PermRoomBlockManage     Permission = "room_block.manage"
	PermHousekeepingUpdate  Permission = "housekeeping.update"
	PermHousekeepingInspect Permission = "housekeeping.inspect"
	PermHousekeepingAssign  Permission = "housekeeping.assign"

	PermGuestRead                 Permission = "guest.read"
	PermGuestWrite                Permission = "guest.write"
	PermGuestSearchAll            Permission = "guest.search_all"
	PermGuestHistoryAllProperties Permission = "guest.history_all_properties"

	PermBillingConfigManage Permission = "billing_config.manage"
	PermRateManage          Permission = "rate.manage"

	PermReservationRead         Permission = "reservation.read"
	PermReservationCreate       Permission = "reservation.create"
	PermReservationUpdate       Permission = "reservation.update"
	PermReservationCancel       Permission = "reservation.cancel"
	PermReservationReinstate    Permission = "reservation.reinstate"
	PermReservationOverrideRate Permission = "reservation.override_rate"
	PermReservationUpgrade      Permission = "reservation.upgrade"

	PermFrontdeskCheckin            Permission = "frontdesk.checkin"
	PermFrontdeskCheckinUnreadyRoom Permission = "frontdesk.checkin_unready_room"
	PermFrontdeskCheckout           Permission = "frontdesk.checkout"
	PermFrontdeskRoomMove           Permission = "frontdesk.room_move"
	PermFrontdeskRateChange         Permission = "frontdesk.rate_change"
	PermFrontdeskReverseCheckin     Permission = "frontdesk.reverse_checkin"

	PermFolioRead              Permission = "folio.read"
	PermFolioPostCharge        Permission = "folio.post_charge"
	PermFolioAdjust            Permission = "folio.adjust"
	PermFolioReverse           Permission = "folio.reverse"
	PermFolioPostAfterCheckout Permission = "folio.post_after_checkout"
	PermFolioReopen            Permission = "folio.reopen"

	PermPaymentPost   Permission = "payment.post"
	PermPaymentVoid   Permission = "payment.void"
	PermPaymentRefund Permission = "payment.refund"

	PermCorrectionApprove Permission = "correction.approve"

	PermNightAuditRun    Permission = "nightaudit.run"
	PermNightAuditNoShow Permission = "nightaudit.no_show"

	PermReportView Permission = "report.view"

	PermCompanyManage      Permission = "company.manage"
	PermGroupManage        Permission = "group.manage"
	PermCityLedgerRead     Permission = "cityledger.read"
	PermCityLedgerTransfer Permission = "cityledger.transfer"
	PermCityLedgerReceive  Permission = "cityledger.receive"
	PermCityLedgerInvoice  Permission = "cityledger.invoice"

	PermAuditRead Permission = "audit.read"
)

// Catalogue is the complete, ordered list of permissions (the only valid codes).
var Catalogue = []PermissionInfo{
	{PermPropertyManage, "Property", "Edit property settings", "M1"},

	{PermRoomManage, "Rooms", "Manage room types and rooms", "M3"},
	{PermRoomBlockManage, "Rooms", "Create and release OOO/OOS blocks", "M3"},
	{PermHousekeepingUpdate, "Rooms", "Change housekeeping status", "M3"},
	{PermHousekeepingInspect, "Rooms", "Mark rooms INSPECTED (supervisor)", "M3"},
	{PermHousekeepingAssign, "Rooms", "Generate the daily cleaning list and assign it to housekeepers", "S2"},

	{PermGuestRead, "Guests", "View guest profiles", "M4"},
	{PermGuestWrite, "Guests", "Create and edit guest profiles", "M4"},
	{PermGuestSearchAll, "Guests", "Search guest profiles across the whole tenant", "M4"},
	{PermGuestHistoryAllProperties, "Guests", "See guest history at other properties", "M4"},

	{PermBillingConfigManage, "Configuration", "Manage taxes, service charges and charge codes", "M5"},
	{PermRateManage, "Configuration", "Manage rate plans and rates", "M7"},

	{PermReservationRead, "Reservations", "View reservations and availability", "M8"},
	{PermReservationCreate, "Reservations", "Create and confirm reservations", "M8"},
	{PermReservationUpdate, "Reservations", "Amend reservations and assign rooms", "M8"},
	{PermReservationCancel, "Reservations", "Cancel reservations", "M8"},
	{PermReservationReinstate, "Reservations", "Reinstate cancelled reservations", "M8"},
	{PermReservationOverrideRate, "Reservations", "Override nightly rates", "M8"},
	{PermReservationUpgrade, "Reservations", "Assign upgraded rooms", "M8"},

	{PermFrontdeskCheckin, "Front desk", "Check guests in (including walk-ins)", "M10"},
	{PermFrontdeskCheckinUnreadyRoom, "Front desk", "Check in to a room that is not clean/inspected", "M10"},
	{PermFrontdeskCheckout, "Front desk", "Check guests out", "M12"},
	{PermFrontdeskRoomMove, "Front desk", "Move guests between rooms", "M12"},
	{PermFrontdeskRateChange, "Front desk", "Change the rate of in-house stays", "M12"},
	{PermFrontdeskReverseCheckin, "Front desk", "Reverse a check-in on the same business day", "M10"},

	{PermFolioRead, "Billing", "View folios", "M9"},
	{PermFolioPostCharge, "Billing", "Post charges", "M9"},
	{PermFolioAdjust, "Billing", "Post adjustments", "M9"},
	{PermFolioReverse, "Billing", "Reverse same-day postings", "M9"},
	{PermFolioPostAfterCheckout, "Billing", "Post late charges after check-out", "M9"},
	{PermFolioReopen, "Billing", "Reopen closed folios", "M9"},

	{PermPaymentPost, "Billing", "Take payments and deposits", "M9"},
	{PermPaymentVoid, "Billing", "Void same-day payments", "M9"},
	{PermPaymentRefund, "Billing", "Refund payments", "M9"},
	{PermCorrectionApprove, "Billing", "Approve corrections (adjustments, reversals, voids and refunds) with own credentials", "M9"},

	{PermNightAuditRun, "End of day", "Run night audit and post room charges", "M13"},
	{PermNightAuditNoShow, "End of day", "Mark arrivals as no-show", "M13"},

	{PermReportView, "Reports", "View and export reports", "M14"},

	{PermCompanyManage, "Accounts", "Create and edit companies (corporate accounts) and their credit terms", "S1"},
	{PermGroupManage, "Accounts", "Create and edit group bookings and add their rooms", "S1"},
	{PermCityLedgerRead, "Accounts", "View city ledger accounts, statements and ageing", "S1"},
	{PermCityLedgerTransfer, "Accounts", "Transfer a guest folio balance to a company's city ledger account", "S1"},
	{PermCityLedgerReceive, "Accounts", "Record and void what a company pays against its account", "S1"},
	{PermCityLedgerInvoice, "Accounts", "Issue and void invoices to a company for checked-out transfers", "S1"},

	{PermAuditRead, "Administration", "Read the audit trail", "M15"},
}

var validPermissions = func() map[Permission]bool {
	m := make(map[Permission]bool, len(Catalogue))
	for _, p := range Catalogue {
		m[p.Code] = true
	}
	return m
}()

// ValidPermission reports whether code is in the catalogue.
func ValidPermission(code Permission) bool { return validPermissions[code] }
