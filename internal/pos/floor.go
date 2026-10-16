package pos

import (
	"context"
	"fmt"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
)

// FloorPlan is the areas of an outlet with their tables and marks.
type FloorPlan struct {
	OutletID int64  `json:"outlet_id"`
	Areas    []Area `json:"areas"`
}

func (s *Service) areas(ctx context.Context, tenantID, propertyID int64, outletID, areaID *int64) ([]Area, error) {
	q := s.q(ctx)
	rows, err := q.ListAreas(ctx, posdb.ListAreasParams{TenantID: tenantID, PropertyID: propertyID, OutletID: outletID, ID: areaID})
	if err != nil {
		return nil, err
	}
	tables, err := q.ListTables(ctx, posdb.ListTablesParams{TenantID: tenantID, PropertyID: propertyID, OutletID: outletID, AreaID: areaID})
	if err != nil {
		return nil, err
	}
	marks, err := q.ListMarks(ctx, posdb.ListMarksParams{TenantID: tenantID, PropertyID: propertyID, OutletID: outletID, AreaID: areaID})
	if err != nil {
		return nil, err
	}
	out := make([]Area, len(rows))
	at := map[int64]int{}
	for i, r := range rows {
		out[i] = Area{ID: r.ID, OutletID: r.OutletID, Name: r.Name, SortOrder: int(r.SortOrder), GridW: int(r.GridW), GridH: int(r.GridH), LayoutVersion: r.LayoutVersion, IsActive: r.IsActive, Tables: []Table{}, Marks: []Mark{}}
		at[r.ID] = i
	}
	for _, t := range tables {
		if i, ok := at[t.AreaID]; ok {
			out[i].Tables = append(out[i].Tables, Table{ID: t.ID, AreaID: t.AreaID, Code: t.Code, Capacity: int(t.Capacity), Shape: t.Shape, X: int(t.X), Y: int(t.Y), W: int(t.W), H: int(t.H), IsActive: t.IsActive})
		}
	}
	for _, m := range marks {
		if i, ok := at[m.AreaID]; ok {
			out[i].Marks = append(out[i].Marks, Mark{ID: m.ID, Kind: m.Kind, Label: m.Label, X: int(m.X), Y: int(m.Y), W: int(m.W), H: int(m.H)})
		}
	}
	return out, nil
}

// GetFloorPlan answers the areas of an outlet with their tables and marks.
func (s *Service) GetFloorPlan(ctx context.Context, propertyID, outletID int64) (FloorPlan, error) {
	p, err := s.canRead(ctx, propertyID, readers...)
	if err != nil {
		return FloorPlan{}, err
	}
	o, err := s.outlets(ctx, p.TenantID, propertyID, &outletID, nil)
	if err != nil {
		return FloorPlan{}, err
	}
	if len(o) == 0 {
		return FloorPlan{}, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	areas, err := s.areas(ctx, p.TenantID, propertyID, &outletID, nil)
	if err != nil {
		return FloorPlan{}, err
	}
	return FloorPlan{OutletID: outletID, Areas: areas}, nil
}

func validateArea(name string, sort, w, h int) []apperr.FieldError {
	f := append(checkName("name", name), checkSort(sort)...)
	if w < 10 || w > 200 {
		f = append(f, fieldErr("grid_w", "INVALID_VALUE", "10 to 200"))
	}
	if h < 10 || h > 200 {
		f = append(f, fieldErr("grid_h", "INVALID_VALUE", "10 to 200"))
	}
	return f
}

// CreateArea adds an empty area to the floor of an outlet (pos.manage).
func (s *Service) CreateArea(ctx context.Context, propertyID, outletID int64, in AreaInput) (Area, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Area{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.GridW == 0 {
		in.GridW = 40
	}
	if in.GridH == 0 {
		in.GridH = 24
	}
	if f := validateArea(in.Name, in.SortOrder, in.GridW, in.GridH); len(f) > 0 {
		return Area{}, invalid("area", f...)
	}
	var out Area
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		o, err := s.outlets(ctx, p.TenantID, propertyID, &outletID, nil)
		if err != nil {
			return err
		}
		if len(o) == 0 {
			return notFound("OUTLET_NOT_FOUND", "outlet")
		}
		id, err := s.q(ctx).InsertArea(ctx, posdb.InsertAreaParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, Name: in.Name, SortOrder: int32(in.SortOrder), //nolint:gosec // G115: validated
			GridW: int16(in.GridW), GridH: int16(in.GridH), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		list, err := s.areas(ctx, p.TenantID, propertyID, nil, &id)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.area_created", "pos_area", id, in.Name, nil, map[string]any{"outlet": o[0].Code, "name": in.Name}))
	})
	return out, err
}

func validateLayout(in LayoutInput) []apperr.FieldError {
	f := validateArea(strings.TrimSpace(in.Name), in.SortOrder, in.GridW, in.GridH)
	if len(in.Tables) > maxTables {
		f = append(f, fieldErr("tables", "INVALID_VALUE", "at most 300 tables"))
	}
	if len(in.Marks) > maxMarks {
		f = append(f, fieldErr("marks", "INVALID_VALUE", "at most 100 marks"))
	}
	codes := map[string]bool{}
	ids := map[int64]bool{}
	for i, t := range in.Tables {
		tf := func(n string) string { return fmt.Sprintf("tables[%d].%s", i, n) }
		code := strings.TrimSpace(t.Code)
		switch {
		case code == "" || len([]rune(code)) > 10:
			f = append(f, fieldErr(tf("code"), "INVALID_VALUE", "1 to 10 characters"))
		case strings.HasPrefix(code, "~"):
			f = append(f, fieldErr(tf("code"), "INVALID_VALUE", "a code does not start with ~"))
		case codes[strings.ToUpper(code)]:
			f = append(f, fieldErr(tf("code"), "DUPLICATE", "the code is used by another table of this area"))
		}
		codes[strings.ToUpper(code)] = true
		if t.ID != 0 {
			if ids[t.ID] {
				f = append(f, fieldErr(tf("id"), "DUPLICATE", "the table is listed twice"))
			}
			ids[t.ID] = true
		}
		if !tableShapes[t.Shape] {
			f = append(f, fieldErr(tf("shape"), "INVALID_VALUE", "SQUARE, RECT or ROUND"))
		}
		if t.Capacity < 1 || t.Capacity > 99 {
			f = append(f, fieldErr(tf("capacity"), "INVALID_VALUE", "1 to 99"))
		}
		if t.W < 1 || t.W > 60 || t.H < 1 || t.H > 60 || t.X < 0 || t.Y < 0 || t.X+t.W > in.GridW || t.Y+t.H > in.GridH {
			f = append(f, fieldErr(tf("x"), "OUT_OF_PLAN", "the table is not inside the plan"))
		}
	}
	for i, m := range in.Marks {
		mf := func(n string) string { return fmt.Sprintf("marks[%d].%s", i, n) }
		if !markKinds[m.Kind] {
			f = append(f, fieldErr(mf("kind"), "INVALID_VALUE", "WINDOW, DOOR, CASHIER or OTHER"))
		}
		if len([]rune(m.Label)) > 40 {
			f = append(f, fieldErr(mf("label"), "INVALID_VALUE", "at most 40 characters"))
		}
		if m.W < 1 || m.W > 60 || m.H < 1 || m.H > 60 || m.X < 0 || m.Y < 0 || m.X+m.W > in.GridW || m.Y+m.H > in.GridH {
			f = append(f, fieldErr(mf("x"), "OUT_OF_PLAN", "the mark is not inside the plan"))
		}
	}
	return f
}

// SaveLayout saves a whole area: its name and size, its tables and its marks (pos.manage). The version of the layout the editor loaded must still be the current one (409
// VERSION_CONFLICT otherwise). A table that is not in the list is switched off, not deleted: a bill may have used it.
func (s *Service) SaveLayout(ctx context.Context, propertyID, areaID int64, in LayoutInput) (Area, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Area{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.LayoutVersion < 1 {
		return Area{}, invalid("layout", fieldErr("layout_version", "REQUIRED", "the version of the plan you loaded"))
	}
	if f := validateLayout(in); len(f) > 0 {
		return Area{}, invalid("layout", f...)
	}
	var out Area
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		version, err := q.LockArea(ctx, posdb.LockAreaParams{TenantID: p.TenantID, PropertyID: propertyID, ID: areaID})
		if err != nil {
			return orNotFound(err, notFound("AREA_NOT_FOUND", "area"))
		}
		if version != in.LayoutVersion {
			return apperr.Conflict("VERSION_CONFLICT", "the floor plan was changed by someone else").WithContext("current_version", version)
		}
		existing, err := q.ListTables(ctx, posdb.ListTablesParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: &areaID})
		if err != nil {
			return err
		}
		have := map[int64]bool{}
		for _, t := range existing {
			have[t.ID] = true
		}
		keep := []int64{}
		for _, t := range in.Tables {
			if t.ID != 0 {
				if !have[t.ID] {
					return invalid("layout", fieldErr("tables", "NOT_FOUND", "a table is not one of this area"))
				}
				keep = append(keep, t.ID)
			}
		}
		if err := q.DeactivateTables(ctx, posdb.DeactivateTablesParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: areaID, Keep: keep}); err != nil {
			return err
		}
		// Park the codes of the tables that stay under a temporary one, so that two tables may swap codes in one save.
		for _, id := range keep {
			if _, err := s.txm.DB(ctx).Exec(ctx, "UPDATE pos_tables SET code = '~' || id::text WHERE tenant_id = $1 AND property_id = $2 AND id = $3", p.TenantID, propertyID, id); err != nil {
				return err
			}
		}
		for _, t := range in.Tables {
			code := strings.TrimSpace(t.Code)
			if t.ID != 0 {
				if _, err := q.UpdateTable(ctx, posdb.UpdateTableParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: areaID, ID: t.ID, Code: code, Capacity: int16(t.Capacity), Shape: t.Shape, //nolint:gosec // G115: validated
					X: int16(t.X), Y: int16(t.Y), W: int16(t.W), H: int16(t.H), IsActive: true}); err != nil { //nolint:gosec // G115: validated
					return err
				}
				continue
			}
			if _, err := q.InsertTable(ctx, posdb.InsertTableParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: areaID, Code: code, Capacity: int16(t.Capacity), Shape: t.Shape, //nolint:gosec // G115: validated
				X: int16(t.X), Y: int16(t.Y), W: int16(t.W), H: int16(t.H), IsActive: true}); err != nil { //nolint:gosec // G115: validated
				return err
			}
		}
		if err := q.DeleteMarks(ctx, posdb.DeleteMarksParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: areaID}); err != nil {
			return err
		}
		for _, m := range in.Marks {
			if err := q.InsertMark(ctx, posdb.InsertMarkParams{TenantID: p.TenantID, PropertyID: propertyID, AreaID: areaID, Kind: m.Kind, Label: strings.TrimSpace(m.Label),
				X: int16(m.X), Y: int16(m.Y), W: int16(m.W), H: int16(m.H)}); err != nil { //nolint:gosec // G115: validated
				return err
			}
		}
		if err := q.UpdateArea(ctx, posdb.UpdateAreaParams{TenantID: p.TenantID, PropertyID: propertyID, ID: areaID, Name: in.Name, SortOrder: int32(in.SortOrder), //nolint:gosec // G115: validated
			GridW: int16(in.GridW), GridH: int16(in.GridH), IsActive: active(in.IsActive), ActorID: p.ActorID()}); err != nil { //nolint:gosec // G115: validated
			return err
		}
		list, err := s.areas(ctx, p.TenantID, propertyID, nil, &areaID)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.layout_saved", "pos_area", areaID, in.Name, nil, map[string]any{"tables": len(in.Tables), "marks": len(in.Marks), "layout_version": out.LayoutVersion}))
	})
	return out, err
}

// SetupCheck lists what is wrong with the configuration: an outlet with nothing to sell, a product whose station has no printer or screen in an outlet that needs one, an outlet with
// tables and no floor plan, and what a cashier will miss. It reads and writes nothing.
func (s *Service) SetupCheck(ctx context.Context, propertyID int64) ([]SetupIssue, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	out := []SetupIssue{}
	outlets, err := q.SetupCheckOutlets(ctx, posdb.SetupCheckOutletsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	for _, o := range outlets {
		if o.SoldProducts == 0 {
			out = append(out, SetupIssue{Code: "OUTLET_NO_PRODUCTS", Severity: "ERROR", Outlet: o.OutletCode})
		}
		if o.UsesTables && o.Tables == 0 {
			out = append(out, SetupIssue{Code: "OUTLET_NO_TABLES", Severity: "WARNING", Outlet: o.OutletCode})
		}
		if o.NoPrinter {
			out = append(out, SetupIssue{Code: "OUTLET_NO_RECEIPT_PRINTER", Severity: "WARNING", Outlet: o.OutletCode})
		}
		if o.NoDrawer {
			out = append(out, SetupIssue{Code: "OUTLET_NO_DRAWER", Severity: "WARNING", Outlet: o.OutletCode})
		}
	}
	rows, err := q.SetupCheckStations(ctx, posdb.SetupCheckStationsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		code := "PRODUCT_STATION_NO_OUTPUT"
		if r.StationID == nil {
			code = "PRODUCT_NO_STATION"
		}
		is := SetupIssue{Code: code, Severity: "ERROR", Outlet: r.OutletCode, Product: r.ProductCode, OrderMode: r.OrderMode}
		switch {
		case r.StationID == nil:
			is.Reason = "NO_STATION"
		case !r.StationActive:
			is.Reason = "STATION_INACTIVE"
		case r.OrderMode == "PRINT":
			is.Reason = "NO_PRINTER"
			if r.StationHasKds {
				is.OtherOutput = "KDS"
			}
		default:
			is.Reason = "NO_KDS"
			if r.StationHasPrinter {
				is.OtherOutput = "PRINTER"
			}
		}
		if r.StationCode != nil {
			is.Station = *r.StationCode
		}
		out = append(out, is)
	}
	return out, nil
}
