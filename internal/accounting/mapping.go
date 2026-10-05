package accounting

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

func meaningOf(key string) string {
	for _, k := range mapKeys {
		if k.Key == key {
			return k.Meaning
		}
	}
	return ""
}

// AccountMap lists the accounts the system posts to (accounting.view).
func (s *Service) AccountMap(ctx context.Context, propertyID int64) ([]MapEntry, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return nil, err
	}
	return s.accountMap(ctx, p.TenantID, propertyID)
}

func (s *Service) accountMap(ctx context.Context, tenantID, propertyID int64) ([]MapEntry, error) {
	rows, err := s.q(ctx).ListAccountMap(ctx, accountingdb.ListAccountMapParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]MapEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, MapEntry{Key: r.MapKey, Meaning: meaningOf(r.MapKey), AccountID: r.AccountID, AccountCode: r.AccountCode, AccountName: r.AccountName, AccountType: r.AccountType})
	}
	slices.SortFunc(out, func(a, b MapEntry) int {
		return slices.IndexFunc(mapKeys, func(k struct{ Key, Type, Meaning string }) bool { return k.Key == a.Key }) -
			slices.IndexFunc(mapKeys, func(k struct{ Key, Type, Meaning string }) bool { return k.Key == b.Key })
	})
	return out, nil
}

// SetAccountMap points system keys at accounts (accounting.manage). Each account must be active, take postings and be
// of the right type (cash and receivables are assets, deposits and payables liabilities; the suspense account may be
// anything). All or nothing.
func (s *Service) SetAccountMap(ctx context.Context, propertyID int64, in []MapInput) ([]MapEntry, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return nil, err
	}
	if len(in) == 0 || len(in) > len(mapKeys) {
		return nil, apperr.Invalid("the mapping is invalid", fieldErr("entries", "OUT_OF_RANGE", fmt.Sprintf("between 1 and %d entries", len(mapKeys))))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate); err != nil {
			return err
		}
		bd, err := s.businessDate(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		before, err := s.accountMap(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		var fields []apperr.FieldError
		for i, e := range in {
			at := func(f string) string { return "entries[" + itoa(i) + "]." + f }
			idx := slices.IndexFunc(mapKeys, func(k struct{ Key, Type, Meaning string }) bool { return k.Key == e.Key })
			switch {
			case idx < 0:
				fields = append(fields, fieldErr(at("map_key"), "INVALID_VALUE", "a known key"))
				continue
			case seen[e.Key]:
				fields = append(fields, fieldErr(at("map_key"), "DUPLICATE", "each key once"))
				continue
			}
			seen[e.Key] = true
			acc, err := q.GetAccountRow(ctx, accountingdb.GetAccountRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: e.AccountID})
			if err != nil {
				fields = append(fields, fieldErr(at("account_id"), "NOT_FOUND", "an account of this property"))
				continue
			}
			switch want := mapKeys[idx].Type; {
			case !acc.IsPostable:
				fields = append(fields, fieldErr(at("account_id"), "NOT_POSTABLE", "a header account takes no postings"))
			case !acc.IsActive:
				fields = append(fields, fieldErr(at("account_id"), "INACTIVE", "the account is inactive"))
			case want != "" && acc.AccountType != want:
				fields = append(fields, fieldErr(at("account_id"), "WRONG_TYPE", "an account of type "+want))
			default:
				if err := q.SetAccountMap(ctx, accountingdb.SetAccountMapParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: e.Key, AccountID: e.AccountID, ActorID: p.ActorID()}); err != nil {
					return err
				}
			}
		}
		if len(fields) > 0 {
			return apperr.Invalid("the mapping is invalid", fields...)
		}
		for _, e := range in { // a system account that requires a department needs a default for it
			id := e.AccountID
			if err := s.requireSetup(ctx, p.TenantID, propertyID, &id); err != nil {
				return err
			}
		}
		after, err := s.accountMap(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.map_changed", "property", propertyID, before, after))
	})
	if err != nil {
		return nil, err
	}
	return s.accountMap(ctx, p.TenantID, propertyID)
}

// checkMapUsable verifies that every system key still points at an active account that takes postings.
func (s *Service) checkMapUsable(ctx context.Context, tenantID, propertyID int64) error {
	rows, err := s.q(ctx).ListAccountMap(ctx, accountingdb.ListAccountMapParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return err
	}
	for _, r := range rows {
		a, err := s.q(ctx).GetAccountRow(ctx, accountingdb.GetAccountRowParams{TenantID: tenantID, PropertyID: propertyID, ID: r.AccountID})
		if err != nil {
			return err
		}
		if !a.IsPostable || !a.IsActive {
			return apperr.Conflict("ACCOUNT_IN_USE", "the system posts to account "+a.Code+" ("+r.MapKey+"): it must stay active and take postings").WithContext("map_key", r.MapKey)
		}
	}
	return nil
}

// fallbackFor is the account the amounts of a code go to when the code cannot be used.
func fallbackFor(kind string) string {
	switch kind {
	case "TAX":
		return KeyTaxPayable
	case "SERVICE_CHARGE":
		return KeyServicePayable
	}
	return KeySuspense
}

// UnmappedCodes lists the charge codes, taxes and service charges whose account code the journals cannot use
// (accounting.view): none, unknown to the chart, inactive, a header, or of a type that does not fit. Their amounts are
// posted to the fallback account of the kind, which the report names.
func (s *Service) UnmappedCodes(ctx context.Context, propertyID int64) (CodeReport, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return CodeReport{}, err
	}
	rows, err := s.q(ctx).ListCodeUsage(ctx, accountingdb.ListCodeUsageParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return CodeReport{}, err
	}
	mapped, err := s.accountMap(ctx, p.TenantID, propertyID)
	if err != nil {
		return CodeReport{}, err
	}
	name := map[string]string{}
	for _, m := range mapped {
		name[m.Key] = m.AccountCode + " " + m.AccountName
	}
	out := CodeReport{Issues: []CodeIssue{}}
	for _, r := range rows {
		if !r.IsActive {
			continue
		}
		out.Checked++
		problem := ""
		switch {
		case r.GlAccountCode == nil:
			problem = "NO_CODE"
		case r.AccountID == nil:
			problem = "UNKNOWN_ACCOUNT"
		case r.AccountActive != nil && !*r.AccountActive:
			problem = "INACTIVE_ACCOUNT"
		case r.AccountPostable != nil && !*r.AccountPostable:
			problem = "HEADER_ACCOUNT"
		case r.AccountType != nil && !typeFits(r.Kind, *r.AccountType):
			problem = "WRONG_TYPE"
		}
		if problem == "" {
			continue
		}
		out.Issues = append(out.Issues, CodeIssue{Kind: r.Kind, ID: r.ID, Code: r.Code, Name: r.Name, Account: deref(r.GlAccountCode), Problem: problem, PostedTo: name[fallbackFor(r.Kind)]})
	}
	return out, nil
}

// typeFits says whether an account type can receive what a kind of code carries: revenue for what is sold, a liability
// for tax, and a liability or revenue (a service charge the hotel keeps) for service charges.
func typeFits(kind, accountType string) bool {
	switch kind {
	case "CHARGE_CODE":
		return accountType == TypeRevenue
	case "TAX":
		return accountType == TypeLiability
	}
	return accountType == TypeLiability || accountType == TypeRevenue
}

func itoa(n int) string { return strconv.Itoa(n) }
