package assets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

func categoryFrom(r assetsdb.ListCategoriesRow) Category {
	return Category{ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, CostAccountID: r.CostAccountID, CostAccountCode: r.CostAccountCode, CostAccountName: r.CostAccountName,
		AccumulatedAccountID: r.AccumulatedAccountID, AccumulatedAccountCode: r.AccumulatedAccountCode, AccumulatedAccountName: r.AccumulatedAccountName,
		ExpenseAccountID: r.ExpenseAccountID, ExpenseAccountCode: r.ExpenseAccountCode, ExpenseAccountName: r.ExpenseAccountName,
		DepartmentID: r.DepartmentID, DepartmentCode: r.DepartmentCode, DepartmentName: r.DepartmentName, Method: r.Method, LifeMonths: r.LifeMonths, ResidualPct: r.ResidualPct, DBFactor: r.DbFactor,
		FiscalGroup: r.FiscalGroup, FiscalMethod: r.FiscalMethod, IsDepreciable: r.IsDepreciable != nil && *r.IsDepreciable, IsActive: r.IsActive, SortOrder: r.SortOrder,
		AssetCount: int(r.AssetCount), Version: r.Version}
}

// ListCategories lists the categories with their accounts (any asset permission, or whoever enters bills).
func (s *Service) ListCategories(ctx context.Context, propertyID int64) ([]Category, error) {
	p, err := s.require(ctx, propertyID, categoryReaders...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCategories(ctx, assetsdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Category, 0, len(rows))
	for _, r := range rows {
		out = append(out, categoryFrom(r))
	}
	return out, nil
}

func (s *Service) category(ctx context.Context, p auth.Principal, propertyID, id int64) (Category, error) {
	rows, err := s.q(ctx).ListCategories(ctx, assetsdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Category{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return categoryFrom(r), nil
		}
	}
	return Category{}, notFound("CATEGORY_NOT_FOUND", "category")
}

// checkCategory validates the input of a category and returns the values to store: the accounts are of the property, active and postable, of the right type, and the cost account is the cost
// account of no other active category (the waiting list finds the category of a journal line by it).
func (s *Service) checkCategory(ctx context.Context, p auth.Principal, propertyID int64, in *CategoryInput, exceptID int64) error {
	var f []apperr.FieldError
	in.Code, in.Name = normalCode(in.Code), trimmed(in.Name)
	if exceptID == 0 && !categoryCodePattern.MatchString(in.Code) {
		f = append(f, fieldErr("code", "INVALID_FORMAT", "capital letters, digits and . _ -, up to 20 characters"))
	}
	if n := len([]rune(in.Name)); n < 1 || n > 100 {
		f = append(f, fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
	}
	if !categoryKinds[in.Kind] {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "LAND, BUILDING, FFE, OE, VEHICLE, IT, CIP, LICENSE or OTHER"))
	}
	notDepreciated := in.Kind == KindLand || in.Kind == KindCIP
	together := in.AccumulatedAccountID != nil
	if (in.ExpenseAccountID != nil) != together || (in.Method != nil) != together || (in.LifeMonths != nil) != together {
		f = append(f, fieldErr("method", "INCOMPLETE", "the accumulated account, the expense account, the method and the life come together or not at all"))
	}
	if notDepreciated && together {
		f = append(f, fieldErr("accumulated_account_id", "NOT_ALLOWED", "land and construction in progress are not depreciated"))
	}
	if in.Method != nil && *in.Method != MethodSL && *in.Method != MethodDB {
		f = append(f, fieldErr("method", "INVALID_VALUE", "SL or DB"))
	}
	if in.LifeMonths != nil && (*in.LifeMonths < 1 || *in.LifeMonths > maxLifeMonths) {
		f = append(f, fieldErr("life_months", "OUT_OF_RANGE", "1 to 1200 months"))
	}
	if in.ResidualPct.IsNegative() || in.ResidualPct.GreaterThan(decHundred) || !in.ResidualPct.Equal(in.ResidualPct.Round(2)) {
		f = append(f, fieldErr("residual_pct", "OUT_OF_RANGE", "0 to 100 percent, two decimals at most"))
	}
	if in.DBFactor != nil && (!in.DBFactor.IsPositive() || in.DBFactor.GreaterThan(decimal.NewFromInt(5)) || !in.DBFactor.Equal(in.DBFactor.Round(2))) {
		f = append(f, fieldErr("db_factor", "OUT_OF_RANGE", "above 0 and up to 5, two decimals at most"))
	}
	if in.Method == nil || *in.Method != MethodDB {
		in.DBFactor = nil
	}
	if in.FiscalGroup == "" {
		in.FiscalGroup = FiscalNone
	}
	if !fiscalGroups[in.FiscalGroup] {
		f = append(f, fieldErr("fiscal_group", "INVALID_VALUE", "NONE, G1, G2, G3, G4, BLD_PERMANENT or BLD_NON_PERMANENT"))
	} else if in.FiscalGroup == FiscalNone {
		in.FiscalMethod = nil
	} else {
		if in.FiscalMethod == nil {
			in.FiscalMethod = ptr(MethodSL)
		}
		if *in.FiscalMethod != MethodSL && *in.FiscalMethod != MethodDB {
			f = append(f, fieldErr("fiscal_method", "INVALID_VALUE", "SL or DB"))
		} else if *in.FiscalMethod == MethodDB && isBuildingGroup(in.FiscalGroup) {
			f = append(f, fieldErr("fiscal_method", "NOT_ALLOWED", "a building has the straight line only"))
		}
	}
	// the accounts
	q := s.q(ctx)
	check := func(field string, id int64, wantType string) {
		a, err := q.GetAccount(ctx, assetsdb.GetAccountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr(field, "NOT_FOUND", "no such account in this property"))
		case err != nil:
			f = append(f, fieldErr(field, "UNAVAILABLE", err.Error()))
		case !a.IsActive || !a.IsPostable:
			f = append(f, fieldErr(field, "ACCOUNT_NOT_POSTABLE", "the account must be active and take postings"))
		case a.AccountType != wantType:
			f = append(f, fieldErr(field, "WRONG_TYPE", "an account of type "+wantType+" is needed"))
		}
	}
	check("cost_account_id", in.CostAccountID, "ASSET")
	if in.AccumulatedAccountID != nil {
		check("accumulated_account_id", *in.AccumulatedAccountID, "ASSET")
		if *in.AccumulatedAccountID == in.CostAccountID {
			f = append(f, fieldErr("accumulated_account_id", "SAME_ACCOUNT", "the accumulated depreciation account is not the cost account"))
		}
	}
	if in.ExpenseAccountID != nil {
		check("expense_account_id", *in.ExpenseAccountID, "EXPENSE")
	}
	if in.IsActive == nil || *in.IsActive {
		if other, err := q.CategoryUsingCostAccount(ctx, assetsdb.CategoryUsingCostAccountParams{TenantID: p.TenantID, PropertyID: propertyID, AccountID: in.CostAccountID, ExceptID: exceptID}); err != nil {
			return err
		} else if other != 0 {
			f = append(f, fieldErr("cost_account_id", "ALREADY_USED", "another category uses this cost account"))
		}
	}
	if in.DepartmentID != nil {
		if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
			return err
		}
	}
	if len(f) > 0 {
		return apperr.Invalid("the category is invalid", f...)
	}
	return nil
}

func normalCode(code string) string {
	out := []rune(trimmed(code))
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
	}
	return string(out)
}

// CreateCategory adds a category (asset.setup).
func (s *Service) CreateCategory(ctx context.Context, propertyID int64, in CategoryInput) (Category, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return Category{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		if err := s.checkCategory(ctx, p, propertyID, &in, 0); err != nil {
			return err
		}
		row, err := s.q(ctx).InsertCategory(ctx, assetsdb.InsertCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Kind: in.Kind,
			CostAccountID: in.CostAccountID, AccumulatedAccountID: in.AccumulatedAccountID, ExpenseAccountID: in.ExpenseAccountID, DepartmentID: in.DepartmentID, Method: in.Method,
			LifeMonths: in.LifeMonths, ResidualPct: in.ResidualPct, DbFactor: in.DBFactor, FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod,
			IsActive: in.IsActive == nil || *in.IsActive, SortOrder: in.SortOrder, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		id = row.ID
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.category_created", "fa_category", id, in.Code, nil, map[string]any{"code": in.Code, "name": in.Name, "kind": in.Kind}))
	})
	if err != nil {
		return Category{}, err
	}
	return s.category(ctx, p, propertyID, id)
}

// UpdateCategory replaces the editable fields of a category (asset.setup). While the category has assets its kind and its accounts do not change (409 CATEGORY_IN_USE): the way is a new
// category and a change of category per asset.
func (s *Service) UpdateCategory(ctx context.Context, propertyID, id int64, in CategoryInput) (Category, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return Category{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		q := s.q(ctx)
		cur, err := q.GetCategory(ctx, assetsdb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("CATEGORY_NOT_FOUND", "category"))
		}
		in.Code = cur.Code
		if err := s.checkCategory(ctx, p, propertyID, &in, id); err != nil {
			return err
		}
		if n, err := q.CategoryAssetCount(ctx, assetsdb.CategoryAssetCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		} else if n > 0 && (in.Kind != cur.Kind || in.CostAccountID != cur.CostAccountID || !sameInt64(in.AccumulatedAccountID, cur.AccumulatedAccountID) || !sameInt64(in.ExpenseAccountID, cur.ExpenseAccountID)) {
			return apperr.Conflict("CATEGORY_IN_USE", fmt.Sprintf("the category has %d assets: its kind and its accounts do not change; make a new category and change the category of the assets", n)).WithContext("assets", n)
		}
		_, err = q.UpdateCategory(ctx, assetsdb.UpdateCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, Kind: in.Kind,
			CostAccountID: in.CostAccountID, AccumulatedAccountID: in.AccumulatedAccountID, ExpenseAccountID: in.ExpenseAccountID, DepartmentID: in.DepartmentID, Method: in.Method,
			LifeMonths: in.LifeMonths, ResidualPct: in.ResidualPct, DbFactor: in.DBFactor, FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod,
			IsActive: in.IsActive == nil || *in.IsActive, SortOrder: in.SortOrder, ActorID: p.ActorID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return versionConflict("category", cur.Version)
		}
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.category_updated", "fa_category", id, cur.Code, map[string]any{"name": cur.Name, "kind": cur.Kind, "is_active": cur.IsActive},
			map[string]any{"name": in.Name, "kind": in.Kind, "is_active": in.IsActive == nil || *in.IsActive}))
	})
	if err != nil {
		return Category{}, err
	}
	return s.category(ctx, p, propertyID, id)
}

func sameInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ListAccounts lists the postable accounts of a type for the pickers of the setup (asset.setup): a plan with the inventory and without the accounting can set a category up without the
// routes of the chart.
func (s *Service) ListAccounts(ctx context.Context, propertyID int64, accountType string) ([]AccountOption, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return nil, err
	}
	var at *string
	if accountType != "" {
		at = &accountType
	}
	rows, err := s.q(ctx).ListAccountsOfType(ctx, assetsdb.ListAccountsOfTypeParams{TenantID: p.TenantID, PropertyID: propertyID, AccountType: at})
	if err != nil {
		return nil, err
	}
	out := make([]AccountOption, 0, len(rows))
	for _, r := range rows {
		o := AccountOption{ID: r.ID, Code: r.Code, Name: r.Name, AccountType: r.AccountType}
		if r.StatementGroup != nil {
			o.StatementGroup = *r.StatementGroup
		}
		out = append(out, o)
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------------
// Locations

func locationFrom(r assetsdb.ListLocationsRow) Location {
	l := Location{ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, RoomID: r.RoomID, BanquetRoomID: r.BanquetRoomID, WarehouseID: r.WarehouseID, LabelCode: r.LabelCode,
		IsActive: r.IsActive, AssetCount: int(r.AssetCount)}
	switch {
	case r.RoomNumber != nil:
		l.ReferenceLabel = ptr("Room " + *r.RoomNumber)
	case r.BanquetRoomName != nil:
		l.ReferenceLabel = r.BanquetRoomName
	case r.WarehouseName != nil:
		l.ReferenceLabel = r.WarehouseName
	}
	return l
}

// ListLocations lists the locations, optionally of one kind or only the active ones (any asset permission, or whoever enters bills).
func (s *Service) ListLocations(ctx context.Context, propertyID int64, kind string, active *bool) ([]Location, error) {
	p, err := s.require(ctx, propertyID, categoryReaders...)
	if err != nil {
		return nil, err
	}
	var k *string
	if kind != "" {
		k = &kind
	}
	rows, err := s.q(ctx).ListLocations(ctx, assetsdb.ListLocationsParams{TenantID: p.TenantID, PropertyID: propertyID, Kind: k, Active: active})
	if err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(rows))
	for _, r := range rows {
		out = append(out, locationFrom(r))
	}
	return out, nil
}

func (s *Service) location(ctx context.Context, p auth.Principal, propertyID, id int64) (Location, error) {
	rows, err := s.q(ctx).ListLocations(ctx, assetsdb.ListLocationsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Location{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return locationFrom(r), nil
		}
	}
	return Location{}, notFound("LOCATION_NOT_FOUND", "location")
}

// insertLocation makes a location with a fresh random label code (32^12 codes: a clash is not expected; the unique key is the backstop).
func (s *Service) insertLocation(ctx context.Context, p auth.Principal, propertyID int64, arg assetsdb.InsertLocationParams) (int64, error) {
	arg.TenantID, arg.PropertyID, arg.ActorID = p.TenantID, propertyID, p.ActorID()
	code, err := newLabelCode()
	if err != nil {
		return 0, err
	}
	arg.LabelCode = code
	row, err := s.q(ctx).InsertLocation(ctx, arg)
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

// CreateLocation adds a free area (asset.setup); a room, a banquet room or a warehouse is made by EnsureLocation when it is first used.
func (s *Service) CreateLocation(ctx context.Context, propertyID int64, in LocationInput) (Location, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return Location{}, err
	}
	in.Code, in.Name = trimmed(in.Code), trimmed(in.Name)
	var f []apperr.FieldError
	if !locationCodePattern.MatchString(in.Code) {
		f = append(f, fieldErr("code", "INVALID_FORMAT", "letters, digits and . _ : / -, up to 30 characters"))
	}
	if n := len([]rune(in.Name)); n < 1 || n > 100 {
		f = append(f, fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(f) > 0 {
		return Location{}, invalid("location", f...)
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		id, err = s.insertLocation(ctx, p, propertyID, assetsdb.InsertLocationParams{Code: in.Code, Name: in.Name, Kind: "AREA"})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.location_created", "fa_location", id, in.Code, nil, map[string]any{"code": in.Code, "name": in.Name, "kind": "AREA"}))
	})
	if err != nil {
		return Location{}, err
	}
	return s.location(ctx, p, propertyID, id)
}

// UpdateLocation renames a location or switches it off (asset.setup).
func (s *Service) UpdateLocation(ctx context.Context, propertyID, id int64, in LocationPatch) (Location, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return Location{}, err
	}
	in.Name = trimmed(in.Name)
	if n := len([]rune(in.Name)); n < 1 || n > 100 {
		return Location{}, invalid("location", fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		q := s.q(ctx)
		cur, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("LOCATION_NOT_FOUND", "location"))
		}
		act := cur.IsActive
		if in.IsActive != nil {
			act = *in.IsActive
		}
		if _, err := q.UpdateLocation(ctx, assetsdb.UpdateLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsActive: act, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.location_updated", "fa_location", id, cur.Code,
			map[string]any{"name": cur.Name, "is_active": cur.IsActive}, map[string]any{"name": in.Name, "is_active": act}))
	})
	if err != nil {
		return Location{}, err
	}
	return s.location(ctx, p, propertyID, id)
}

// EnsureLocation answers the location of a room, a banquet room or a warehouse, making it when it is used for the first time (F17). asset.setup or asset.manage.
func (s *Service) EnsureLocation(ctx context.Context, propertyID int64, in EnsureLocationInput) (Location, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup, auth.PermAssetManage)
	if err != nil {
		return Location{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		id, err = s.ensureLocation(ctx, p, propertyID, day, in.Kind, in.RefID, 0, "")
		return err
	})
	if err != nil {
		return Location{}, err
	}
	return s.location(ctx, p, propertyID, id)
}

// ensureLocation finds or makes the location of a master. ref is the id, or the code or number when the id is 0 (a line of a file).
func (s *Service) ensureLocation(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, kind string, refID int64, _ int, refCode string) (int64, error) {
	q := s.q(ctx)
	var code, name string
	arg := assetsdb.InsertLocationParams{Kind: kind}
	switch kind {
	case "ROOM":
		var r assetsdb.GetRoomRefRow
		var err error
		if refID != 0 {
			r, err = q.GetRoomRef(ctx, assetsdb.GetRoomRefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: refID})
		} else {
			var r2 assetsdb.GetRoomRefByNumberRow
			r2, err = q.GetRoomRefByNumber(ctx, assetsdb.GetRoomRefByNumberParams{TenantID: p.TenantID, PropertyID: propertyID, RoomNumber: refCode})
			r = assetsdb.GetRoomRefRow(r2)
		}
		if err != nil {
			return 0, orNotFound(err, invalid("location", fieldErr("ref_id", "NOT_FOUND", "no such room in this property")))
		}
		if cur, err := q.GetLocationByRoom(ctx, assetsdb.GetLocationByRoomParams{TenantID: p.TenantID, PropertyID: propertyID, RefID: &r.ID}); err == nil {
			return cur.ID, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		code, name, arg.RoomID = "ROOM-"+r.RoomNumber, "Room "+r.RoomNumber, &r.ID
	case "BANQUET_ROOM":
		var r assetsdb.GetBanquetRoomRefRow
		var err error
		if refID != 0 {
			r, err = q.GetBanquetRoomRef(ctx, assetsdb.GetBanquetRoomRefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: refID})
		} else {
			var r2 assetsdb.GetBanquetRoomRefByCodeRow
			r2, err = q.GetBanquetRoomRefByCode(ctx, assetsdb.GetBanquetRoomRefByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: refCode})
			r = assetsdb.GetBanquetRoomRefRow(r2)
		}
		if err != nil {
			return 0, orNotFound(err, invalid("location", fieldErr("ref_id", "NOT_FOUND", "no such banquet room in this property")))
		}
		if cur, err := q.GetLocationByBanquetRoom(ctx, assetsdb.GetLocationByBanquetRoomParams{TenantID: p.TenantID, PropertyID: propertyID, RefID: &r.ID}); err == nil {
			return cur.ID, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		code, name, arg.BanquetRoomID = "BQ-"+r.Code, r.Name, &r.ID
	case "WAREHOUSE":
		var r assetsdb.GetWarehouseRefRow
		var err error
		if refID != 0 {
			r, err = q.GetWarehouseRef(ctx, assetsdb.GetWarehouseRefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: refID})
		} else {
			var r2 assetsdb.GetWarehouseRefByCodeRow
			r2, err = q.GetWarehouseRefByCode(ctx, assetsdb.GetWarehouseRefByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: refCode})
			r = assetsdb.GetWarehouseRefRow(r2)
		}
		if err != nil {
			return 0, orNotFound(err, invalid("location", fieldErr("ref_id", "NOT_FOUND", "no such warehouse in this property")))
		}
		if cur, err := q.GetLocationByWarehouse(ctx, assetsdb.GetLocationByWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, RefID: &r.ID}); err == nil {
			return cur.ID, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		code, name, arg.WarehouseID = "WH-"+r.Code, r.Name, &r.ID
	default:
		return 0, invalid("location", fieldErr("kind", "INVALID_VALUE", "ROOM, BANQUET_ROOM or WAREHOUSE"))
	}
	if len(code) > maxLocationCodeChars {
		code = code[:maxLocationCodeChars]
	}
	arg.Code, arg.Name = code, name
	id, err := s.insertLocation(ctx, p, propertyID, arg)
	if err != nil {
		return 0, err
	}
	return id, s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.location_created", "fa_location", id, code, nil, map[string]any{"code": code, "name": name, "kind": kind}))
}
