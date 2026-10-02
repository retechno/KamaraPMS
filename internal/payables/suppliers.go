package payables

import (
	"context"
	"strings"

	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

func toSupplier(r payablesdb.ListSuppliersRow) Supplier {
	return Supplier{
		ID: r.ID, Code: r.Code, Name: r.Name, ContactName: deref(r.ContactName), Email: deref(r.Email), Phone: deref(r.Phone), Address: deref(r.Address), City: deref(r.City),
		TaxID: deref(r.TaxID), PaymentTermsDays: int(r.PaymentTermsDays), DefaultAccountID: r.DefaultAccountID, DefaultAccountCode: deref(r.DefaultAccountCode),
		DefaultAccountName: deref(r.DefaultAccountName), BankDetails: deref(r.BankDetails), Notes: deref(r.Notes), IsActive: r.IsActive, Outstanding: r.Outstanding, CreatedAt: r.CreatedAt,
	}
}

// Suppliers lists suppliers by name with what is owed to each (payables.view).
func (s *Service) Suppliers(ctx context.Context, propertyID int64, f SupplierFilter) ([]Supplier, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListSuppliers(ctx, payablesdb.ListSuppliersParams{TenantID: p.TenantID, PropertyID: propertyID, Active: f.Active, Q: nullable(f.Q), RowLimit: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]Supplier, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSupplier(r))
	}
	return out, nil
}

func (s *Service) loadSupplier(ctx context.Context, tenantID, propertyID, id int64) (Supplier, error) {
	rows, err := s.q(ctx).ListSuppliers(ctx, payablesdb.ListSuppliersParams{TenantID: tenantID, PropertyID: propertyID, ID: &id, RowLimit: 1})
	if err != nil {
		return Supplier{}, err
	}
	if len(rows) == 0 {
		return Supplier{}, errSupplierNotFound()
	}
	return toSupplier(rows[0]), nil
}

// GetSupplier is one supplier (payables.view).
func (s *Service) GetSupplier(ctx context.Context, propertyID, id int64) (Supplier, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return Supplier{}, err
	}
	return s.loadSupplier(ctx, p.TenantID, propertyID, id)
}

func validateSupplier(name, email, notes string, terms int, fields *[]apperr.FieldError, parts map[string]string) {
	if strings.TrimSpace(name) == "" {
		*fields = append(*fields, fieldErr("name", "REQUIRED", "the supplier needs a name"))
	}
	for f, v := range parts {
		limit := map[string]int{"name": 150, "contact_name": 150, "email": 254, "phone": 40, "address": 300, "city": 100, "tax_id": 40, "bank_details": 300, "notes": 1000}[f]
		if len([]rune(v)) > limit {
			*fields = append(*fields, fieldErr(f, "TOO_LONG", "too long"))
		}
	}
	if email != "" && !strings.Contains(email, "@") {
		*fields = append(*fields, fieldErr("email", "INVALID_EMAIL", "not an e-mail address"))
	}
	if terms < 0 || terms > 365 {
		*fields = append(*fields, fieldErr("payment_terms_days", "OUT_OF_RANGE", "between 0 and 365"))
	}
}

func (s *Service) checkAccount(ctx context.Context, tenantID, propertyID int64, id *int64) error {
	if id == nil {
		return nil
	}
	a, err := s.q(ctx).AccountUsable(ctx, payablesdb.AccountUsableParams{TenantID: tenantID, PropertyID: propertyID, ID: *id})
	switch {
	case isNoRows(err):
		return apperr.Invalid("the supplier is invalid", fieldErr("default_account_id", "NOT_FOUND", "no such account in this property"))
	case err != nil:
		return err
	case !a.IsPostable || !a.IsActive:
		return apperr.Invalid("the supplier is invalid", fieldErr("default_account_id", "NOT_USABLE", "an active account that takes postings"))
	}
	return nil
}

// CreateSupplier adds a supplier (payables.manage).
func (s *Service) CreateSupplier(ctx context.Context, propertyID int64, in SupplierInput) (Supplier, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesManage)
	if err != nil {
		return Supplier{}, err
	}
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	terms := 30
	if in.PaymentTermsDays != nil {
		terms = *in.PaymentTermsDays
	}
	active := in.IsActive == nil || *in.IsActive
	var fields []apperr.FieldError
	if !supplierCode.MatchString(code) {
		fields = append(fields, fieldErr("code", "INVALID_CODE", "letters, digits, dot, dash or underscore, up to 20 characters"))
	}
	validateSupplier(in.Name, in.Email, in.Notes, terms, &fields, map[string]string{
		"name": in.Name, "contact_name": in.ContactName, "email": in.Email, "phone": in.Phone, "address": in.Address, "city": in.City, "tax_id": in.TaxID, "bank_details": in.BankDetails, "notes": in.Notes,
	})
	if len(fields) > 0 {
		return Supplier{}, apperr.Invalid("the supplier is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := s.checkAccount(ctx, p.TenantID, propertyID, in.DefaultAccountID); err != nil {
			return err
		}
		id, err = s.q(ctx).InsertSupplier(ctx, payablesdb.InsertSupplierParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: code, Name: strings.TrimSpace(in.Name), ContactName: nullable(in.ContactName), Email: nullable(in.Email),
			Phone: nullable(in.Phone), Address: nullable(in.Address), City: nullable(in.City), TaxID: nullable(in.TaxID), PaymentTermsDays: termsOf(terms),
			DefaultAccountID: in.DefaultAccountID, BankDetails: nullable(in.BankDetails), Notes: nullable(in.Notes), IsActive: active, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.supplier_created", "supplier", id, nil, map[string]any{"code": code, "name": in.Name}))
	})
	if err != nil {
		return Supplier{}, err
	}
	return s.loadSupplier(ctx, p.TenantID, propertyID, id)
}

// UpdateSupplier changes a supplier (payables.manage).
func (s *Service) UpdateSupplier(ctx context.Context, propertyID, id int64, patch SupplierPatch) (Supplier, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesManage)
	if err != nil {
		return Supplier{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		cur, err := s.loadSupplier(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		pick := func(v *string, old string) string {
			if v != nil {
				return strings.TrimSpace(*v)
			}
			return old
		}
		name, contact, email := pick(patch.Name, cur.Name), pick(patch.ContactName, cur.ContactName), pick(patch.Email, cur.Email)
		phone, address, city := pick(patch.Phone, cur.Phone), pick(patch.Address, cur.Address), pick(patch.City, cur.City)
		taxID, bank, notes := pick(patch.TaxID, cur.TaxID), pick(patch.BankDetails, cur.BankDetails), pick(patch.Notes, cur.Notes)
		terms, active, account := cur.PaymentTermsDays, cur.IsActive, cur.DefaultAccountID
		if patch.PaymentTermsDays != nil {
			terms = *patch.PaymentTermsDays
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if patch.DefaultAccountID != nil {
			account = patch.DefaultAccountID
			if *account < 1 {
				account = nil
			}
		}
		var fields []apperr.FieldError
		validateSupplier(name, email, notes, terms, &fields, map[string]string{
			"name": name, "contact_name": contact, "email": email, "phone": phone, "address": address, "city": city, "tax_id": taxID, "bank_details": bank, "notes": notes,
		})
		if len(fields) > 0 {
			return apperr.Invalid("the supplier is invalid", fields...)
		}
		if patch.DefaultAccountID != nil {
			if err := s.checkAccount(ctx, p.TenantID, propertyID, account); err != nil {
				return err
			}
		}
		if err := s.q(ctx).UpdateSupplier(ctx, payablesdb.UpdateSupplierParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: name, ContactName: nullable(contact), Email: nullable(email), Phone: nullable(phone),
			Address: nullable(address), City: nullable(city), TaxID: nullable(taxID), PaymentTermsDays: termsOf(terms), DefaultAccountID: account,
			BankDetails: nullable(bank), Notes: nullable(notes), IsActive: active, ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.supplier_updated", "supplier", id,
			map[string]any{"name": cur.Name, "active": cur.IsActive, "terms": cur.PaymentTermsDays}, map[string]any{"name": name, "active": active, "terms": terms}))
	})
	if err != nil {
		return Supplier{}, err
	}
	return s.loadSupplier(ctx, p.TenantID, propertyID, id)
}

// termsOf narrows payment terms that validateSupplier has checked to be between 0 and 365 days.
func termsOf(days int) int16 { return int16(days) } //nolint:gosec // G115: validated to 0..365 by validateSupplier
