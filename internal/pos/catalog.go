package pos

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
)

// ---------------------------------------------------------------- rule profiles

func (s *Service) profiles(ctx context.Context, tenantID, propertyID int64, id *int64) ([]RuleProfile, error) {
	q := s.q(ctx)
	rows, err := q.ListRuleProfiles(ctx, posdb.ListRuleProfilesParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return nil, err
	}
	rules, err := q.ListRuleProfileRules(ctx, posdb.ListRuleProfileRulesParams{TenantID: tenantID, PropertyID: propertyID, ProfileID: id})
	if err != nil {
		return nil, err
	}
	byProfile := map[int64][]RuleRef{}
	for _, r := range rules {
		ref := RuleRef{Code: r.Code, Name: r.Name, Rate: r.Rate, Sequence: int(r.Sequence), IsActive: r.RuleIsActive, TaxID: r.TaxID, ServiceChargeID: r.ServiceChargeID, OnService: r.TaxOnService}
		if r.TaxID != nil {
			ref.RuleType = "TAX"
		} else {
			ref.RuleType = "SERVICE"
		}
		byProfile[r.ProfileID] = append(byProfile[r.ProfileID], ref)
	}
	out := make([]RuleProfile, len(rows))
	for i, r := range rows {
		out[i] = RuleProfile{ID: r.ID, Code: r.Code, Name: r.Name, PriceMode: r.PriceMode, IsActive: r.IsActive, Rules: byProfile[r.ID]}
		if out[i].Rules == nil {
			out[i].Rules = []RuleRef{}
		}
	}
	return out, nil
}

// ListRuleProfiles answers the tax and service profiles of the property, each with its rules.
func (s *Service) ListRuleProfiles(ctx context.Context, propertyID int64) ([]RuleProfile, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	return s.profiles(ctx, p.TenantID, propertyID, nil)
}

func validateProfile(in RuleProfileInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	if in.PriceMode != "EXCLUSIVE" && in.PriceMode != "INCLUSIVE" {
		f = append(f, fieldErr("price_mode", "INVALID_VALUE", "EXCLUSIVE or INCLUSIVE"))
	}
	seenTax, seenSvc := map[int64]bool{}, map[int64]bool{}
	if len(in.Rules) > 20 {
		f = append(f, fieldErr("rules", "INVALID_VALUE", "at most 20 rules"))
	}
	for i, r := range in.Rules {
		field := func(n string) string { return "rules[" + itoa(i) + "]." + n }
		switch {
		case (r.TaxID == nil) == (r.ServiceChargeID == nil):
			f = append(f, fieldErr(field("tax_id"), "INVALID_VALUE", "a rule is one tax or one service charge"))
		case r.TaxID != nil && seenTax[*r.TaxID], r.ServiceChargeID != nil && seenSvc[*r.ServiceChargeID]:
			f = append(f, fieldErr(field("tax_id"), "DUPLICATE", "the rule is listed twice"))
		case r.TaxID != nil:
			seenTax[*r.TaxID] = true
		default:
			seenSvc[*r.ServiceChargeID] = true
		}
	}
	return f
}

func itoa(i int) string { return decimal.NewFromInt(int64(i)).String() }

// checkRulesActive refuses a rule that is not an active tax or service charge of the property.
func (s *Service) checkRulesActive(ctx context.Context, tenantID, propertyID int64, rules []RuleInput) error {
	for i, r := range rules {
		var on bool
		var err error
		if r.TaxID != nil {
			err = s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM taxes WHERE tenant_id = $1 AND property_id = $2 AND id = $3", tenantID, propertyID, *r.TaxID).Scan(&on)
		} else {
			err = s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM service_charges WHERE tenant_id = $1 AND property_id = $2 AND id = $3", tenantID, propertyID, *r.ServiceChargeID).Scan(&on)
		}
		field := "rules[" + itoa(i) + "].tax_id"
		if errors.Is(err, pgx.ErrNoRows) {
			return invalid("profile", fieldErr(field, "NOT_FOUND", "no such tax or service charge in this property"))
		}
		if err != nil {
			return err
		}
		if !on {
			return invalid("profile", fieldErr(field, "INACTIVE", "the tax or service charge is switched off"))
		}
	}
	return nil
}

func (s *Service) saveProfileRules(ctx context.Context, tenantID, propertyID, id int64, rules []RuleInput) error {
	q := s.q(ctx)
	if err := q.DeleteRuleProfileRules(ctx, posdb.DeleteRuleProfileRulesParams{TenantID: tenantID, PropertyID: propertyID, ProfileID: id}); err != nil {
		return err
	}
	for i, r := range rules {
		if err := q.InsertRuleProfileRule(ctx, posdb.InsertRuleProfileRuleParams{TenantID: tenantID, PropertyID: propertyID, ProfileID: id, TaxID: r.TaxID, ServiceChargeID: r.ServiceChargeID, Sequence: int16(i + 1)}); err != nil { //nolint:gosec // G115: at most 20
			return err
		}
	}
	return nil
}

// CreateRuleProfile adds a profile (pos.manage).
func (s *Service) CreateRuleProfile(ctx context.Context, propertyID int64, in RuleProfileInput) (RuleProfile, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return RuleProfile{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := validateProfile(in, true); len(f) > 0 {
		return RuleProfile{}, invalid("profile", f...)
	}
	var out RuleProfile
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := s.checkRulesActive(ctx, p.TenantID, propertyID, in.Rules); err != nil {
			return err
		}
		id, err := s.q(ctx).InsertRuleProfile(ctx, posdb.InsertRuleProfileParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, PriceMode: in.PriceMode, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.saveProfileRules(ctx, p.TenantID, propertyID, id, in.Rules); err != nil {
			return err
		}
		list, err := s.profiles(ctx, p.TenantID, propertyID, &id)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.rule_profile_created", "pos_rule_profile", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateRuleProfile replaces a profile and its rules (pos.manage). What was already posted keeps its snapshot of the rules: nothing is rewritten.
func (s *Service) UpdateRuleProfile(ctx context.Context, propertyID, id int64, in RuleProfileInput) (RuleProfile, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return RuleProfile{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := validateProfile(in, false); len(f) > 0 {
		return RuleProfile{}, invalid("profile", f...)
	}
	var out RuleProfile
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		before, err := s.profiles(ctx, p.TenantID, propertyID, &id)
		if err != nil {
			return err
		}
		if len(before) == 0 {
			return notFound("RULE_PROFILE_NOT_FOUND", "profile")
		}
		if err := s.checkRulesActive(ctx, p.TenantID, propertyID, in.Rules); err != nil {
			return err
		}
		if _, err := s.q(ctx).UpdateRuleProfile(ctx, posdb.UpdateRuleProfileParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, PriceMode: in.PriceMode, IsActive: active(in.IsActive), ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.saveProfileRules(ctx, p.TenantID, propertyID, id, in.Rules); err != nil {
			return err
		}
		list, err := s.profiles(ctx, p.TenantID, propertyID, &id)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.rule_profile_updated", "pos_rule_profile", id, out.Code, before[0], out))
	})
	return out, err
}

// ---------------------------------------------------------------- price lists

func (s *Service) ListPriceLists(ctx context.Context, propertyID int64) ([]PriceList, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListPriceLists(ctx, posdb.ListPriceListsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]PriceList, len(rows))
	for i, r := range rows {
		out[i] = PriceList{ID: r.ID, Code: r.Code, Name: r.Name, IsActive: r.IsActive, ItemCount: r.ItemCount}
	}
	return out, nil
}

// CreatePriceList adds a price list (pos.manage).
func (s *Service) CreatePriceList(ctx context.Context, propertyID int64, in PriceListInput) (PriceList, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return PriceList{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	f := append(checkCode("code", in.Code), checkName("name", in.Name)...)
	if len(f) > 0 {
		return PriceList{}, invalid("price list", f...)
	}
	var out PriceList
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertPriceList(ctx, posdb.InsertPriceListParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = PriceList{ID: id, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.price_list_created", "pos_price_list", id, in.Code, nil, out))
	})
	return out, err
}

// UpdatePriceList renames or switches off a price list (pos.manage). One that an active outlet uses is not switched off.
func (s *Service) UpdatePriceList(ctx context.Context, propertyID, id int64, in PriceListInput) (PriceList, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return PriceList{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkName("name", in.Name); len(f) > 0 {
		return PriceList{}, invalid("price list", f...)
	}
	var out PriceList
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if !active(in.IsActive) {
			var n int
			if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT count(*) FROM pos_outlets WHERE tenant_id = $1 AND property_id = $2 AND price_list_id = $3 AND is_active", p.TenantID, propertyID, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("PRICE_LIST_IN_USE", "an active outlet uses this price list").WithContext("outlets", n)
			}
		}
		n, err := s.q(ctx).UpdatePriceList(ctx, posdb.UpdatePriceListParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("PRICE_LIST_NOT_FOUND", "price list")
		}
		out = PriceList{ID: id, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.price_list_updated", "pos_price_list", id, in.Name, nil, out))
	})
	return out, err
}

// ListPriceItems answers the prices of a price list.
func (s *Service) ListPriceItems(ctx context.Context, propertyID, listID int64) ([]PriceItem, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT true FROM pos_price_lists WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, listID).Scan(&exists); err != nil {
		return nil, orNotFound(err, notFound("PRICE_LIST_NOT_FOUND", "price list"))
	}
	rows, err := s.q(ctx).ListPriceListItems(ctx, posdb.ListPriceListItemsParams{TenantID: p.TenantID, PropertyID: propertyID, PriceListID: &listID})
	if err != nil {
		return nil, err
	}
	out := make([]PriceItem, len(rows))
	for i, r := range rows {
		out[i] = PriceItem{PriceListID: r.PriceListID, ProductID: r.ProductID, Price: r.Price, IsSold: r.IsSold, StationID: r.StationID}
	}
	return out, nil
}

// SetPriceItems sets the prices of some products in a price list (pos.manage); the products not named are left as they are.
func (s *Service) SetPriceItems(ctx context.Context, propertyID, listID int64, items []PriceItemInput) ([]PriceItem, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > 2000 {
		return nil, invalid("price list", fieldErr("items", "INVALID_VALUE", "1 to 2000 prices"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		var exists bool
		if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT true FROM pos_price_lists WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, listID).Scan(&exists); err != nil {
			return orNotFound(err, notFound("PRICE_LIST_NOT_FOUND", "price list"))
		}
		for i, it := range items {
			if f := checkMoney("items["+itoa(i)+"].price", it.Price, dec, false); len(f) > 0 {
				return invalid("price list", f...)
			}
			if err := s.q(ctx).UpsertPriceListItem(ctx, posdb.UpsertPriceListItemParams{TenantID: p.TenantID, PropertyID: propertyID, PriceListID: listID, ProductID: it.ProductID,
				Price: it.Price, IsSold: it.IsSold, StationID: it.StationID, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.prices_set", "pos_price_list", listID, "", nil, map[string]any{"items": len(items)}))
	})
	if err != nil {
		return nil, err
	}
	return s.ListPriceItems(ctx, propertyID, listID)
}

// ---------------------------------------------------------------- categories

// checkFbClass reads the sale class of a category: empty is none (35-inventory.md 4.9).
func checkFbClass(v *string) (*string, []apperr.FieldError) {
	if v == nil {
		return nil, nil
	}
	c := strings.ToUpper(strings.TrimSpace(*v))
	switch c {
	case "":
		return nil, nil
	case "FOOD", "BEVERAGE", "OTHER":
		return &c, nil
	}
	return nil, []apperr.FieldError{fieldErr("fb_class", "INVALID_VALUE", "FOOD, BEVERAGE or OTHER")}
}

func (s *Service) ListCategories(ctx context.Context, propertyID int64) ([]Category, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCategories(ctx, posdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Category, len(rows))
	for i, r := range rows {
		out[i] = Category{ID: r.ID, Code: r.Code, Name: r.Name, ParentID: r.ParentID, SortOrder: int(r.SortOrder), IsActive: r.IsActive, ProductCount: r.ProductCount, FbClass: r.FbClass}
	}
	return out, nil
}

// CreateCategory adds a category (pos.manage).
func (s *Service) CreateCategory(ctx context.Context, propertyID int64, in CategoryInput) (Category, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Category{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	f := append(append(checkCode("code", in.Code), checkName("name", in.Name)...), checkSort(in.SortOrder)...)
	fbClass, cf := checkFbClass(in.FbClass)
	f = append(f, cf...)
	if len(f) > 0 {
		return Category{}, invalid("category", f...)
	}
	var out Category
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if in.ParentID != nil {
			var parent *int64
			if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT parent_id FROM pos_categories WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, *in.ParentID).Scan(&parent); err != nil {
				return orNotFound(err, invalid("category", fieldErr("parent_id", "NOT_FOUND", "no such category in this property")))
			}
			if parent != nil {
				return invalid("category", fieldErr("parent_id", "TOO_DEEP", "a category inside a category has no categories: one level only"))
			}
		}
		id, err := s.q(ctx).InsertCategory(ctx, posdb.InsertCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, ParentID: in.ParentID,
			SortOrder: int32(in.SortOrder), IsActive: active(in.IsActive), FbClass: fbClass, ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		out = Category{ID: id, Code: in.Code, Name: in.Name, ParentID: in.ParentID, SortOrder: in.SortOrder, IsActive: active(in.IsActive), FbClass: fbClass}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.category_created", "pos_category", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateCategory renames, orders or switches off a category (pos.manage).
func (s *Service) UpdateCategory(ctx context.Context, propertyID, id int64, in CategoryInput) (Category, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Category{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	f := append(checkName("name", in.Name), checkSort(in.SortOrder)...)
	fbClass, cf := checkFbClass(in.FbClass)
	f = append(f, cf...)
	if len(f) > 0 {
		return Category{}, invalid("category", f...)
	}
	var out Category
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		n, err := s.q(ctx).UpdateCategory(ctx, posdb.UpdateCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, SortOrder: int32(in.SortOrder), IsActive: active(in.IsActive), FbClass: fbClass, ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("CATEGORY_NOT_FOUND", "category")
		}
		out = Category{ID: id, Name: in.Name, SortOrder: in.SortOrder, IsActive: active(in.IsActive), FbClass: fbClass}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.category_updated", "pos_category", id, in.Name, nil, out))
	})
	return out, err
}

// ---------------------------------------------------------------- products

func toProduct(r posdb.ListProductsRow) Product {
	return Product{ID: r.ID, Code: r.Code, Name: r.Name, ShortName: r.ShortName, CategoryID: r.CategoryID, CategoryName: r.CategoryName, ChargeCodeID: r.ChargeCodeID, ChargeCode: r.ChargeCode,
		ChargeName: r.ChargeCodeName, RuleProfileID: r.RuleProfileID, StationID: r.StationID, IsActive: r.IsActive, Version: r.Version}
}

func (s *Service) products(ctx context.Context, tenantID, propertyID int64, id *int64, code *string, activeOnly *bool) ([]Product, error) {
	rows, err := s.q(ctx).ListProducts(ctx, posdb.ListProductsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Code: code, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]Product, len(rows))
	for i, r := range rows {
		out[i] = toProduct(r)
	}
	return out, nil
}

// withDetails fills the option groups and the prices of the products.
func (s *Service) withDetails(ctx context.Context, tenantID, propertyID int64, list []Product, productID *int64) ([]Product, error) {
	q := s.q(ctx)
	groups, err := q.ListOptionGroups(ctx, posdb.ListOptionGroupsParams{TenantID: tenantID, PropertyID: propertyID, ProductID: productID})
	if err != nil {
		return nil, err
	}
	opts, err := q.ListOptions(ctx, posdb.ListOptionsParams{TenantID: tenantID, PropertyID: propertyID, ProductID: productID})
	if err != nil {
		return nil, err
	}
	prices, err := q.ListPriceListItems(ctx, posdb.ListPriceListItemsParams{TenantID: tenantID, PropertyID: propertyID, ProductID: productID})
	if err != nil {
		return nil, err
	}
	groupsOf := map[int64][]OptionGroup{}
	index := map[int64][2]int{} // group id -> product id, position
	for _, g := range groups {
		og := OptionGroup{ID: g.ID, Name: g.Name, Kind: g.Kind, MinSelect: int(g.MinSelect), MaxSelect: int(g.MaxSelect), SortOrder: int(g.SortOrder), Options: []Option{}}
		index[g.ID] = [2]int{int(g.ProductID), len(groupsOf[g.ProductID])}
		groupsOf[g.ProductID] = append(groupsOf[g.ProductID], og)
	}
	for _, o := range opts {
		ix := index[o.GroupID]
		gs := groupsOf[int64(ix[0])]
		gs[ix[1]].Options = append(gs[ix[1]].Options, Option{ID: o.ID, Name: o.Name, PriceDelta: o.PriceDelta, IsDefault: o.IsDefault, MaxQty: int(o.MaxQty), SortOrder: int(o.SortOrder), IsActive: o.IsActive})
	}
	pricesOf := map[int64][]PriceItem{}
	for _, pr := range prices {
		pricesOf[pr.ProductID] = append(pricesOf[pr.ProductID], PriceItem{PriceListID: pr.PriceListID, ProductID: pr.ProductID, Price: pr.Price, IsSold: pr.IsSold, StationID: pr.StationID})
	}
	for i := range list {
		list[i].OptionGroups = groupsOf[list[i].ID]
		if list[i].OptionGroups == nil {
			list[i].OptionGroups = []OptionGroup{}
		}
		list[i].Prices = pricesOf[list[i].ID]
		if list[i].Prices == nil {
			list[i].Prices = []PriceItem{}
		}
	}
	return list, nil
}

// ListProducts answers the products. With an outlet each carries the price and whether it is sold there.
func (s *Service) ListProducts(ctx context.Context, propertyID int64, outletID *int64, activeOnly *bool) ([]Product, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	list, err := s.products(ctx, p.TenantID, propertyID, nil, nil, activeOnly)
	if err != nil {
		return nil, err
	}
	if outletID == nil {
		return list, nil
	}
	o, err := s.outlets(ctx, p.TenantID, propertyID, outletID, nil)
	if err != nil {
		return nil, err
	}
	if len(o) == 0 {
		return nil, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	items, err := s.q(ctx).ListPriceListItems(ctx, posdb.ListPriceListItemsParams{TenantID: p.TenantID, PropertyID: propertyID, PriceListID: &o[0].PriceListID})
	if err != nil {
		return nil, err
	}
	byProduct := map[int64]posdb.ListPriceListItemsRow{}
	for _, it := range items {
		byProduct[it.ProductID] = it
	}
	for i := range list {
		if it, ok := byProduct[list[i].ID]; ok {
			price, sold := it.Price, it.IsSold
			list[i].Price, list[i].IsSold = &price, &sold
		} else {
			sold := false
			list[i].IsSold = &sold
		}
	}
	return list, nil
}

// GetProduct answers one product with its option groups and its prices.
func (s *Service) GetProduct(ctx context.Context, propertyID, id int64) (Product, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return Product{}, err
	}
	list, err := s.products(ctx, p.TenantID, propertyID, &id, nil, nil)
	if err != nil {
		return Product{}, err
	}
	if len(list) == 0 {
		return Product{}, notFound("PRODUCT_NOT_FOUND", "product")
	}
	d, err := s.withDetails(ctx, p.TenantID, propertyID, list, &id)
	if err != nil {
		return Product{}, err
	}
	return d[0], nil
}

func validateProduct(in ProductInput, create bool, decimals int32) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		if c := strings.TrimSpace(in.Code); c == "" || len(c) > 30 {
			f = append(f, fieldErr("code", "INVALID_FORMAT", "1 to 30 characters"))
		}
	}
	f = append(f, checkName("name", in.Name)...)
	if in.ShortName != nil && len([]rune(strings.TrimSpace(*in.ShortName))) > maxShort {
		f = append(f, fieldErr("short_name", "INVALID_VALUE", "at most 24 characters: it is printed on the kitchen ticket"))
	}
	if in.CategoryID < 1 {
		f = append(f, fieldErr("category_id", "REQUIRED", "the category of the product"))
	}
	if in.ChargeCodeID < 1 {
		f = append(f, fieldErr("charge_code_id", "REQUIRED", "the charge code decides the revenue account and the department"))
	}
	if len(in.OptionGroups) > maxGroups {
		f = append(f, fieldErr("option_groups", "INVALID_VALUE", "at most 12 groups"))
	}
	total := 0
	for gi, g := range in.OptionGroups {
		gf := func(n string) string { return "option_groups[" + itoa(gi) + "]." + n }
		f = append(f, checkName(gf("name"), g.Name)...)
		switch g.Kind {
		case "VARIANT":
			if g.MaxSelect != 1 || g.MinSelect < 0 || g.MinSelect > 1 {
				f = append(f, fieldErr(gf("max_select"), "INVALID_VALUE", "a variant is pick one: required (1) or optional (0)"))
			}
		case "MODIFIER":
			if g.MinSelect < 0 || g.MaxSelect < 1 || g.MinSelect > g.MaxSelect || g.MaxSelect > 20 {
				f = append(f, fieldErr(gf("max_select"), "INVALID_VALUE", "an add-on group takes 0 or more, and at most 1 to 20"))
			}
		default:
			f = append(f, fieldErr(gf("kind"), "INVALID_VALUE", "VARIANT or MODIFIER"))
		}
		if len(g.Options) == 0 {
			f = append(f, fieldErr(gf("options"), "REQUIRED", "a group has at least one choice"))
		}
		defaults := 0
		for oi, o := range g.Options {
			of := func(n string) string { return gf("options[" + itoa(oi) + "]." + n) }
			total++
			f = append(f, checkName(of("name"), o.Name)...)
			f = append(f, checkMoney(of("price_delta"), o.PriceDelta, decimals, g.Kind == "VARIANT")...)
			if g.Kind == "MODIFIER" && o.PriceDelta.IsNegative() {
				f = append(f, fieldErr(of("price_delta"), "INVALID_VALUE", "an add-on is priced at zero or more"))
			}
			if o.MaxQty < 1 || o.MaxQty > 99 {
				f = append(f, fieldErr(of("max_qty"), "INVALID_VALUE", "1 to 99"))
			}
			if o.IsDefault {
				defaults++
			}
		}
		if g.Kind == "VARIANT" && defaults > 1 {
			f = append(f, fieldErr(gf("options"), "INVALID_VALUE", "one default at most in a variant group"))
		}
		if g.Kind == "VARIANT" && g.MinSelect == 1 && defaults == 0 && false {
			f = append(f, fieldErr(gf("options"), "INVALID_VALUE", ""))
		}
	}
	if total > maxOptions {
		f = append(f, fieldErr("option_groups", "INVALID_VALUE", "at most 60 choices in all"))
	}
	seen := map[int64]bool{}
	for i, pr := range in.Prices {
		pf := "prices[" + itoa(i) + "]."
		if pr.PriceListID < 1 || seen[pr.PriceListID] {
			f = append(f, fieldErr(pf+"price_list_id", "INVALID_VALUE", "one price for each price list"))
		}
		seen[pr.PriceListID] = true
		f = append(f, checkMoney(pf+"price", pr.Price, decimals, false)...)
	}
	return f
}

// checkProductRefs: the charge code is a code a sale may use (not a room or transfer code) and active; the profile and the station are of the property and in use.
func (s *Service) checkProductRefs(ctx context.Context, p auth.Principal, propertyID int64, in ProductInput) error {
	q := s.q(ctx)
	cc, err := q.ChargeCodeKind(ctx, posdb.ChargeCodeKindParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.ChargeCodeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid("product", fieldErr("charge_code_id", "NOT_FOUND", "no such charge code in this property"))
	}
	if err != nil {
		return err
	}
	if !cc.IsActive {
		return invalid("product", fieldErr("charge_code_id", "INACTIVE", "the charge code is switched off"))
	}
	if cc.ChargeType == "ROOM" || cc.ChargeType == "TRANSFER" {
		return invalid("product", fieldErr("charge_code_id", "INVALID_VALUE", "a room or transfer code cannot be sold by an outlet"))
	}
	var on bool
	if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM pos_categories WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, in.CategoryID).Scan(&on); err != nil {
		return orNotFound(err, invalid("product", fieldErr("category_id", "NOT_FOUND", "no such category in this property")))
	}
	if in.RuleProfileID != nil {
		if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM pos_rule_profiles WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, *in.RuleProfileID).Scan(&on); err != nil {
			return orNotFound(err, invalid("product", fieldErr("rule_profile_id", "NOT_FOUND", "no such profile in this property")))
		}
		if !on {
			return invalid("product", fieldErr("rule_profile_id", "INACTIVE", "the profile is switched off"))
		}
	}
	if in.StationID != nil {
		if err := s.txm.DB(ctx).QueryRow(ctx, "SELECT is_active FROM pos_stations WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, *in.StationID).Scan(&on); err != nil {
			return orNotFound(err, invalid("product", fieldErr("station_id", "NOT_FOUND", "no such station in this property")))
		}
	}
	return nil
}

func (s *Service) saveProductParts(ctx context.Context, p auth.Principal, propertyID, id int64, in ProductInput) error {
	q := s.q(ctx)
	if err := q.DeleteProductRecipeOptionLines(ctx, posdb.DeleteProductRecipeOptionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: id}); err != nil {
		return err
	}
	if err := q.DeleteProductOptions(ctx, posdb.DeleteProductOptionsParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: id}); err != nil {
		return err
	}
	if err := q.DeleteProductOptionGroups(ctx, posdb.DeleteProductOptionGroupsParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: id}); err != nil {
		return err
	}
	for gi, g := range in.OptionGroups {
		gid, err := q.InsertOptionGroup(ctx, posdb.InsertOptionGroupParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: id, Name: strings.TrimSpace(g.Name), Kind: g.Kind,
			MinSelect: int16(g.MinSelect), MaxSelect: int16(g.MaxSelect), SortOrder: int32(gi)}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		for oi, o := range g.Options {
			if err := q.InsertOption(ctx, posdb.InsertOptionParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: gid, Name: strings.TrimSpace(o.Name), PriceDelta: o.PriceDelta,
				IsDefault: o.IsDefault, MaxQty: int16(o.MaxQty), SortOrder: int32(oi), IsActive: true}); err != nil { //nolint:gosec // G115: validated
				return err
			}
		}
	}
	for _, pr := range in.Prices {
		if err := q.UpsertPriceListItem(ctx, posdb.UpsertPriceListItemParams{TenantID: p.TenantID, PropertyID: propertyID, PriceListID: pr.PriceListID, ProductID: id,
			Price: pr.Price, IsSold: pr.IsSold, StationID: pr.StationID, ActorID: p.ActorID()}); err != nil {
			return err
		}
	}
	return nil
}

// CreateProduct adds a product with its option groups and prices (pos.manage).
func (s *Service) CreateProduct(ctx context.Context, propertyID int64, in ProductInput) (Product, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Product{}, err
	}
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	var out Product
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if f := validateProduct(in, true, dec); len(f) > 0 {
			return invalid("product", f...)
		}
		if err := s.checkProductRefs(ctx, p, propertyID, in); err != nil {
			return err
		}
		id, err := s.q(ctx).InsertProduct(ctx, posdb.InsertProductParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, ShortName: trimPtr(in.ShortName),
			CategoryID: in.CategoryID, ChargeCodeID: in.ChargeCodeID, RuleProfileID: in.RuleProfileID, StationID: in.StationID, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.saveProductParts(ctx, p, propertyID, id, in); err != nil {
			return err
		}
		out, err = s.productFull(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.product_created", "pos_product", id, in.Code, nil, productAudit(out)))
	})
	return out, err
}

func productAudit(pr Product) map[string]any {
	return map[string]any{"code": pr.Code, "name": pr.Name, "category_id": pr.CategoryID, "charge_code_id": pr.ChargeCodeID, "rule_profile_id": pr.RuleProfileID, "station_id": pr.StationID,
		"is_active": pr.IsActive, "option_groups": len(pr.OptionGroups), "prices": pr.Prices}
}

func (s *Service) productFull(ctx context.Context, tenantID, propertyID, id int64) (Product, error) {
	list, err := s.products(ctx, tenantID, propertyID, &id, nil, nil)
	if err != nil {
		return Product{}, err
	}
	if len(list) == 0 {
		return Product{}, notFound("PRODUCT_NOT_FOUND", "product")
	}
	d, err := s.withDetails(ctx, tenantID, propertyID, list, &id)
	if err != nil {
		return Product{}, err
	}
	return d[0], nil
}

// UpdateProduct replaces a product, its option groups and its prices (pos.manage). The code never changes.
func (s *Service) UpdateProduct(ctx context.Context, propertyID, id int64, in ProductInput) (Product, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Product{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Version < 1 {
		return Product{}, invalid("product", fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	var out Product
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if f := validateProduct(in, false, dec); len(f) > 0 {
			return invalid("product", f...)
		}
		before, err := s.productFull(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if before.Version != in.Version {
			return apperr.Conflict("VERSION_CONFLICT", "the product was changed by someone else").WithContext("current_version", before.Version)
		}
		if err := s.checkProductRefs(ctx, p, propertyID, in); err != nil {
			return err
		}
		n, err := s.q(ctx).UpdateProduct(ctx, posdb.UpdateProductParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, ShortName: trimPtr(in.ShortName),
			CategoryID: in.CategoryID, ChargeCodeID: in.ChargeCodeID, RuleProfileID: in.RuleProfileID, StationID: in.StationID, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.Conflict("VERSION_CONFLICT", "the product was changed by someone else")
		}
		if err := s.saveProductParts(ctx, p, propertyID, id, in); err != nil {
			return err
		}
		out, err = s.productFull(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.product_updated", "pos_product", id, out.Code, productAudit(before), productAudit(out)))
	})
	return out, err
}

// ---------------------------------------------------------------- sold out today

// ListSoldOut answers the products marked sold out today at an outlet (the open business day).
func (s *Service) ListSoldOut(ctx context.Context, propertyID, outletID int64) ([]int64, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if o, err := s.outlets(ctx, p.TenantID, propertyID, &outletID, nil); err != nil {
		return nil, err
	} else if len(o) == 0 {
		return nil, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	ids, err := s.q(ctx).ListSoldOut(ctx, posdb.ListSoldOutParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, BusinessDate: day.BusinessDate})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// SetSoldOut marks a product sold out for the business day, or clears it (pos.order, pos.cashier or pos.manage). It clears itself when the day changes.
func (s *Service) SetSoldOut(ctx context.Context, propertyID, outletID, productID int64, soldOut bool) error {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return err
	}
	return s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		o, err := s.outlets(ctx, p.TenantID, propertyID, &outletID, nil)
		if err != nil {
			return err
		}
		if len(o) == 0 {
			return notFound("OUTLET_NOT_FOUND", "outlet")
		}
		if _, err := s.products(ctx, p.TenantID, propertyID, &productID, nil, nil); err != nil {
			return err
		}
		pl, err := s.products(ctx, p.TenantID, propertyID, &productID, nil, nil)
		if err != nil {
			return err
		}
		if len(pl) == 0 {
			return notFound("PRODUCT_NOT_FOUND", "product")
		}
		action := "pos.product_sold_out"
		if soldOut {
			err = s.q(ctx).MarkSoldOut(ctx, posdb.MarkSoldOutParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, ProductID: productID, BusinessDate: bd, ActorID: p.ActorID()})
		} else {
			action = "pos.product_available"
			err = s.q(ctx).ClearSoldOut(ctx, posdb.ClearSoldOutParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, ProductID: productID})
		}
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, action, "pos_product", productID, pl[0].Code, nil, map[string]any{"outlet": o[0].Code}))
	})
}
