package pos

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos/posdb"
	"kamarapms/internal/tenancy"
)

// DepartmentChecker says whether a department of the property takes a new use (internal/departments).
type DepartmentChecker interface {
	Check(ctx context.Context, tenantID, propertyID, id int64, field string) error
}

// Service is the application service of the POS masters. Reading needs the permission that manages the data or one of the screens that use it; changing it needs pos.manage.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	depts DepartmentChecker
	// the ledger, the approvals and the charge calculation of the bills (SetBilling)
	ledger  *folios.Service
	iam     *iam.Service
	billing *billingconfig.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, depts DepartmentChecker) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, depts: depts}
}

func (s *Service) q(ctx context.Context) *posdb.Queries { return posdb.New(s.txm.DB(ctx)) }

// readers are the permissions that may read the catalogue and the floor plan: who manages it and who works with it.
var readers = []auth.Permission{auth.PermPOSManage, auth.PermPOSOrder, auth.PermPOSCashier, auth.PermPOSKDS}

// outletReaders may also read the list of outlets (the front desk chooses them in the lock of a folio).
var outletReaders = []auth.Permission{auth.PermPOSManage, auth.PermPOSOrder, auth.PermPOSCashier, auth.PermPOSKDS, auth.PermFolioOutletChargeOpen}

func (s *Service) canRead(ctx context.Context, propertyID int64, perms ...auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, perms...)
}

func (s *Service) manage(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermPOSManage)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func invalid(what string, fields ...apperr.FieldError) *apperr.Error {
	return apperr.Invalid("the "+what+" is invalid", fields...)
}

func notFound(code, what string) *apperr.Error {
	return apperr.NotFound(code, "the "+what+" does not exist in this property")
}

func (s *Service) entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

// write runs a change in one transaction: the open business day in share mode first (the date of the audit entry), then fn.
func (s *Service) write(ctx context.Context, propertyID int64, fn func(ctx context.Context, day civil.Date) error) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		return fn(ctx, day.BusinessDate)
	})
}

func checkName(f string, name string) []apperr.FieldError {
	if n := len([]rune(strings.TrimSpace(name))); n < 1 || n > maxName {
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "1 to 100 characters")}
	}
	return nil
}

func checkCode(f, code string) []apperr.FieldError {
	if !codePattern.MatchString(code) {
		return []apperr.FieldError{fieldErr(f, "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, . _ -")}
	}
	return nil
}

func checkSort(sort int) []apperr.FieldError {
	if sort < 0 || sort > maxSort {
		return []apperr.FieldError{fieldErr("sort_order", "INVALID_VALUE", "0 to 100000")}
	}
	return nil
}

func active(in *bool) bool { return in == nil || *in }

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

// decimals is the currency precision of the property: prices have no more decimals than the currency.
func (s *Service) decimals(ctx context.Context, tenantID, propertyID int64) (int32, error) {
	var d int32
	err := s.txm.DB(ctx).QueryRow(ctx, "SELECT currency_decimals FROM properties WHERE tenant_id = $1 AND id = $2", tenantID, propertyID).Scan(&d)
	return d, err
}

func checkMoney(f string, v decimal.Decimal, decimals int32, allowNegative bool) []apperr.FieldError {
	switch {
	case v.IsNegative() && !allowNegative:
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "not below zero")}
	case v.Exponent() < -decimals:
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "more decimals than the currency has")}
	case v.Abs().GreaterThan(decimal.NewFromInt(1_000_000_000_000)):
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "too large")}
	}
	return nil
}

// ---------------------------------------------------------------- outlets

func toOutlet(r posdb.ListOutletsRow) Outlet {
	return Outlet{
		ID: r.ID, Code: r.Code, Name: r.Name, OutletType: r.OutletType, OrderMode: r.OrderMode, DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName,
		RuleProfileID: r.RuleProfileID, RuleProfileName: r.RuleProfileName, PriceListID: r.PriceListID, PriceListName: r.PriceListName, Drawer: r.Drawer,
		ReceiptPrinterID: r.ReceiptPrinterID, ReceiptPrinterName: r.ReceiptPrinterName, UsesTables: r.UsesTables, GuestCountRequired: r.GuestCountRequired,
		RoundingUnit: int(r.RoundingUnit), AllowRoomCharge: r.AllowRoomCharge, AllowCompanyCharge: r.AllowCompanyCharge, AllowComp: r.AllowComp,
		CompTreatment: r.CompTreatment, MaxDiscountPctNoApproval: r.MaxDiscountPctNoApproval, BlockNightAuditWithOpenBills: r.BlockNightAuditWithOpenBills,
		IsActive: r.IsActive, SortOrder: int(r.SortOrder), Version: r.Version,
	}
}

func (s *Service) outlets(ctx context.Context, tenantID, propertyID int64, id *int64, activeOnly *bool) ([]Outlet, error) {
	rows, err := s.q(ctx).ListOutlets(ctx, posdb.ListOutletsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]Outlet, len(rows))
	for i, r := range rows {
		out[i] = toOutlet(r)
	}
	return out, nil
}

// ListOutlets answers the outlets of the property.
func (s *Service) ListOutlets(ctx context.Context, propertyID int64, activeOnly *bool) ([]Outlet, error) {
	p, err := s.canRead(ctx, propertyID, outletReaders...)
	if err != nil {
		return nil, err
	}
	return s.outlets(ctx, p.TenantID, propertyID, nil, activeOnly)
}

// GetOutlet answers one outlet.
func (s *Service) GetOutlet(ctx context.Context, propertyID, id int64) (Outlet, error) {
	p, err := s.canRead(ctx, propertyID, outletReaders...)
	if err != nil {
		return Outlet{}, err
	}
	list, err := s.outlets(ctx, p.TenantID, propertyID, &id, nil)
	if err != nil {
		return Outlet{}, err
	}
	if len(list) == 0 {
		return Outlet{}, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	return list[0], nil
}

func validateOutlet(in OutletInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		code := normalCode(in.Code)
		switch {
		case !outletCodePattern.MatchString(code):
			f = append(f, fieldErr("code", "INVALID_FORMAT", "1 to 6 letters A-Z: it is the start of the bill number"))
		case reservedPrefixes[code]:
			f = append(f, fieldErr("code", "RESERVED", "this prefix is used by another document"))
		}
	}
	f = append(f, checkName("name", in.Name)...)
	if !outletTypes[in.OutletType] {
		f = append(f, fieldErr("outlet_type", "INVALID_VALUE", "restaurant, bar, cafe, room service, spa, laundry, business center, minibar or other"))
	}
	if !orderModes[in.OrderMode] {
		f = append(f, fieldErr("order_mode", "INVALID_VALUE", "PRINT, KDS or DIRECT"))
	}
	if !compModes[in.CompTreatment] {
		f = append(f, fieldErr("comp_treatment", "INVALID_VALUE", "PAYMENT_COMP or DISCOUNT_100"))
	}
	switch in.RoundingUnit {
	case 0, 100, 500, 1000:
	default:
		f = append(f, fieldErr("rounding_unit", "INVALID_VALUE", "0, 100, 500 or 1000"))
	}
	if in.MaxDiscountPctNoApproval.IsNegative() || in.MaxDiscountPctNoApproval.GreaterThan(decimal.NewFromInt(100)) || in.MaxDiscountPctNoApproval.Exponent() < -2 {
		f = append(f, fieldErr("max_discount_pct_no_approval", "INVALID_VALUE", "0 to 100, two decimals"))
	}
	if in.DepartmentID < 1 {
		f = append(f, fieldErr("department_id", "REQUIRED", "the department the sales of the outlet are reported under"))
	}
	if in.RuleProfileID < 1 {
		f = append(f, fieldErr("rule_profile_id", "REQUIRED", "the tax and service profile of the outlet"))
	}
	if in.PriceListID < 1 {
		f = append(f, fieldErr("price_list_id", "REQUIRED", "the price list of the outlet"))
	}
	if in.Drawer != nil {
		if d := strings.TrimSpace(*in.Drawer); d == "" || len(d) > 20 {
			f = append(f, fieldErr("drawer", "INVALID_VALUE", "1 to 20 characters, or empty for no cash"))
		}
	}
	return append(f, checkSort(in.SortOrder)...)
}

func (s *Service) checkOutletRefs(ctx context.Context, p auth.Principal, propertyID int64, in OutletInput) error {
	if err := s.depts.Check(ctx, p.TenantID, propertyID, in.DepartmentID, "department_id"); err != nil {
		return err
	}
	var profActive, listActive bool
	if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM pos_rule_profiles WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, in.RuleProfileID).Scan(&profActive); err != nil {
		return orNotFound(err, invalid("outlet", fieldErr("rule_profile_id", "NOT_FOUND", "no such profile in this property")))
	}
	if !profActive {
		return invalid("outlet", fieldErr("rule_profile_id", "INACTIVE", "the profile is switched off"))
	}
	if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM pos_price_lists WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, in.PriceListID).Scan(&listActive); err != nil {
		return orNotFound(err, invalid("outlet", fieldErr("price_list_id", "NOT_FOUND", "no such price list in this property")))
	}
	if !listActive {
		return invalid("outlet", fieldErr("price_list_id", "INACTIVE", "the price list is switched off"))
	}
	if in.ReceiptPrinterID != nil {
		var kind string
		var on bool
		if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT kind, is_active FROM pos_printers WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, *in.ReceiptPrinterID).Scan(&kind, &on); err != nil {
			return orNotFound(err, invalid("outlet", fieldErr("receipt_printer_id", "NOT_FOUND", "no such printer in this property")))
		}
		if !on || kind != "RECEIPT" {
			return invalid("outlet", fieldErr("receipt_printer_id", "INVALID_VALUE", "a receipt printer that is in use"))
		}
	}
	return nil
}

func outletAudit(o Outlet) map[string]any {
	return map[string]any{
		"name": o.Name, "outlet_type": o.OutletType, "order_mode": o.OrderMode, "department_id": o.DepartmentID, "rule_profile_id": o.RuleProfileID, "price_list_id": o.PriceListID,
		"drawer": o.Drawer, "receipt_printer_id": o.ReceiptPrinterID, "uses_tables": o.UsesTables, "rounding_unit": o.RoundingUnit, "allow_room_charge": o.AllowRoomCharge,
		"allow_company_charge": o.AllowCompanyCharge, "allow_comp": o.AllowComp, "comp_treatment": o.CompTreatment, "max_discount_pct_no_approval": o.MaxDiscountPctNoApproval.String(),
		"block_night_audit_with_open_bills": o.BlockNightAuditWithOpenBills, "is_active": o.IsActive,
	}
}

// CreateOutlet adds an outlet (pos.manage).
func (s *Service) CreateOutlet(ctx context.Context, propertyID int64, in OutletInput) (Outlet, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Outlet{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := validateOutlet(in, true); len(f) > 0 {
		return Outlet{}, invalid("outlet", f...)
	}
	var out Outlet
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := s.checkOutletRefs(ctx, p, propertyID, in); err != nil {
			return err
		}
		id, err := s.q(ctx).InsertOutlet(ctx, posdb.InsertOutletParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, OutletType: in.OutletType, OrderMode: in.OrderMode, DepartmentID: in.DepartmentID,
			RuleProfileID: in.RuleProfileID, PriceListID: in.PriceListID, Drawer: trimPtr(in.Drawer), ReceiptPrinterID: in.ReceiptPrinterID, UsesTables: in.UsesTables,
			GuestCountRequired: in.GuestCountRequired, RoundingUnit: int16(in.RoundingUnit), AllowRoomCharge: in.AllowRoomCharge, AllowCompanyCharge: in.AllowCompanyCharge, //nolint:gosec // G115: validated
			AllowComp: in.AllowComp, CompTreatment: in.CompTreatment, MaxDiscountPctNoApproval: in.MaxDiscountPctNoApproval,
			BlockNightAuditWithOpenBills: in.BlockNightAuditWithOpenBills, IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID(), //nolint:gosec // G115: validated
		})
		if err != nil {
			return err
		}
		list, err := s.outlets(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.outlet_created", "pos_outlet", id, out.Code, nil, outletAudit(out)))
	})
	return out, err
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// UpdateOutlet replaces the settings of an outlet (pos.manage). The code never changes. A change of the complimentary treatment is audited with the old and the new value and applies to
// the bills opened afterwards: an open bill keeps the one it was opened with (stage 2).
func (s *Service) UpdateOutlet(ctx context.Context, propertyID, id int64, in OutletInput) (Outlet, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Outlet{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := validateOutlet(in, false); len(f) > 0 {
		return Outlet{}, invalid("outlet", f...)
	}
	if in.Version < 1 {
		return Outlet{}, invalid("outlet", fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	var out Outlet
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		list, err := s.outlets(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return notFound("OUTLET_NOT_FOUND", "outlet")
		}
		old := list[0]
		if old.Version != in.Version {
			return apperr.Conflict("VERSION_CONFLICT", "the outlet was changed by someone else").WithContext("current_version", old.Version)
		}
		if err := s.checkOutletRefs(ctx, p, propertyID, in); err != nil {
			return err
		}
		if _, err := s.q(ctx).UpdateOutlet(ctx, posdb.UpdateOutletParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, OutletType: in.OutletType, OrderMode: in.OrderMode, DepartmentID: in.DepartmentID,
			RuleProfileID: in.RuleProfileID, PriceListID: in.PriceListID, Drawer: trimPtr(in.Drawer), ReceiptPrinterID: in.ReceiptPrinterID, UsesTables: in.UsesTables,
			GuestCountRequired: in.GuestCountRequired, RoundingUnit: int16(in.RoundingUnit), AllowRoomCharge: in.AllowRoomCharge, AllowCompanyCharge: in.AllowCompanyCharge, //nolint:gosec // G115: validated
			AllowComp: in.AllowComp, CompTreatment: in.CompTreatment, MaxDiscountPctNoApproval: in.MaxDiscountPctNoApproval,
			BlockNightAuditWithOpenBills: in.BlockNightAuditWithOpenBills, IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID(), //nolint:gosec // G115: validated
		}); err != nil {
			return orNotFound(err, apperr.Conflict("VERSION_CONFLICT", "the outlet was changed by someone else"))
		}
		after, err := s.outlets(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = after[0]
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.outlet_updated", "pos_outlet", id, out.Code, outletAudit(old), outletAudit(out))); err != nil {
			return err
		}
		if old.CompTreatment != out.CompTreatment {
			return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.outlet_comp_treatment_changed", "pos_outlet", id, out.Code,
				map[string]any{"comp_treatment": old.CompTreatment}, map[string]any{"comp_treatment": out.CompTreatment}))
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------- printers and stations

func (s *Service) ListPrinters(ctx context.Context, propertyID int64) ([]Printer, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListPrinters(ctx, posdb.ListPrintersParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Printer, len(rows))
	for i, r := range rows {
		out[i] = Printer{ID: r.ID, Name: r.Name, Kind: r.Kind, ConnectionInfo: r.ConnectionInfo, PaperWidthMM: int(r.PaperWidthMm), Codepage: r.Codepage, OpensDrawer: r.OpensDrawer, IsActive: r.IsActive}
	}
	return out, nil
}

func validatePrinter(in PrinterInput) []apperr.FieldError {
	f := checkName("name", in.Name)
	if !printerKind[in.Kind] {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "RECEIPT, KITCHEN or LABEL"))
	}
	if in.PaperWidthMM != 58 && in.PaperWidthMM != 80 {
		f = append(f, fieldErr("paper_width_mm", "INVALID_VALUE", "58 or 80"))
	}
	if len(in.ConnectionInfo) > 200 {
		f = append(f, fieldErr("connection_info", "INVALID_VALUE", "at most 200 characters"))
	}
	if c := strings.TrimSpace(in.Codepage); c == "" || len(c) > 20 {
		f = append(f, fieldErr("codepage", "INVALID_VALUE", "1 to 20 characters"))
	}
	return f
}

// CreatePrinter records a printer (pos.manage). Nothing prints yet: the agent comes with stage 3.
func (s *Service) CreatePrinter(ctx context.Context, propertyID int64, in PrinterInput) (Printer, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Printer{}, err
	}
	in.Name, in.Codepage, in.ConnectionInfo = strings.TrimSpace(in.Name), strings.TrimSpace(in.Codepage), strings.TrimSpace(in.ConnectionInfo)
	if f := validatePrinter(in); len(f) > 0 {
		return Printer{}, invalid("printer", f...)
	}
	var out Printer
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertPrinter(ctx, posdb.InsertPrinterParams{TenantID: p.TenantID, PropertyID: propertyID, Name: in.Name, Kind: in.Kind, ConnectionInfo: in.ConnectionInfo,
			PaperWidthMm: int16(in.PaperWidthMM), Codepage: in.Codepage, OpensDrawer: in.OpensDrawer, IsActive: active(in.IsActive), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		out = Printer{ID: id, Name: in.Name, Kind: in.Kind, ConnectionInfo: in.ConnectionInfo, PaperWidthMM: in.PaperWidthMM, Codepage: in.Codepage, OpensDrawer: in.OpensDrawer, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.printer_created", "pos_printer", id, in.Name, nil, out))
	})
	return out, err
}

// UpdatePrinter replaces a printer's settings (pos.manage).
func (s *Service) UpdatePrinter(ctx context.Context, propertyID, id int64, in PrinterInput) (Printer, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Printer{}, err
	}
	in.Name, in.Codepage, in.ConnectionInfo = strings.TrimSpace(in.Name), strings.TrimSpace(in.Codepage), strings.TrimSpace(in.ConnectionInfo)
	if f := validatePrinter(in); len(f) > 0 {
		return Printer{}, invalid("printer", f...)
	}
	var out Printer
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		n, err := s.q(ctx).UpdatePrinter(ctx, posdb.UpdatePrinterParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, Kind: in.Kind, ConnectionInfo: in.ConnectionInfo,
			PaperWidthMm: int16(in.PaperWidthMM), Codepage: in.Codepage, OpensDrawer: in.OpensDrawer, IsActive: active(in.IsActive), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("PRINTER_NOT_FOUND", "printer")
		}
		out = Printer{ID: id, Name: in.Name, Kind: in.Kind, ConnectionInfo: in.ConnectionInfo, PaperWidthMM: in.PaperWidthMM, Codepage: in.Codepage, OpensDrawer: in.OpensDrawer, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.printer_updated", "pos_printer", id, in.Name, nil, out))
	})
	return out, err
}

func (s *Service) ListStations(ctx context.Context, propertyID int64) ([]Station, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListStations(ctx, posdb.ListStationsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Station, len(rows))
	for i, r := range rows {
		out[i] = Station{ID: r.ID, Code: r.Code, Name: r.Name, PrinterID: r.PrinterID, PrinterName: r.PrinterName, KDSEnabled: r.KdsEnabled, LateAfterMinutes: int(r.LateAfterMinutes), IsActive: r.IsActive}
	}
	return out, nil
}

func validateStation(in StationInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	if in.LateAfterMinutes < 1 || in.LateAfterMinutes > 240 {
		f = append(f, fieldErr("late_after_minutes", "INVALID_VALUE", "1 to 240"))
	}
	return f
}

// CreateStation adds a station (pos.manage).
func (s *Service) CreateStation(ctx context.Context, propertyID int64, in StationInput) (Station, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Station{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := validateStation(in, true); len(f) > 0 {
		return Station{}, invalid("station", f...)
	}
	var out Station
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertStation(ctx, posdb.InsertStationParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, PrinterID: in.PrinterID,
			KdsEnabled: in.KDSEnabled, LateAfterMinutes: int16(in.LateAfterMinutes), IsActive: active(in.IsActive), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		out = Station{ID: id, Code: in.Code, Name: in.Name, PrinterID: in.PrinterID, KDSEnabled: in.KDSEnabled, LateAfterMinutes: in.LateAfterMinutes, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.station_created", "pos_station", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateStation replaces a station's settings (pos.manage). The code never changes.
func (s *Service) UpdateStation(ctx context.Context, propertyID, id int64, in StationInput) (Station, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Station{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := validateStation(in, false); len(f) > 0 {
		return Station{}, invalid("station", f...)
	}
	var out Station
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		n, err := s.q(ctx).UpdateStation(ctx, posdb.UpdateStationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, PrinterID: in.PrinterID,
			KdsEnabled: in.KDSEnabled, LateAfterMinutes: int16(in.LateAfterMinutes), IsActive: active(in.IsActive), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("STATION_NOT_FOUND", "station")
		}
		rows, err := s.q(ctx).ListStations(ctx, posdb.ListStationsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.ID == id {
				out = Station{ID: r.ID, Code: r.Code, Name: r.Name, PrinterID: r.PrinterID, PrinterName: r.PrinterName, KDSEnabled: r.KdsEnabled, LateAfterMinutes: int(r.LateAfterMinutes), IsActive: r.IsActive}
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.station_updated", "pos_station", id, out.Code, nil, out))
	})
	return out, err
}
