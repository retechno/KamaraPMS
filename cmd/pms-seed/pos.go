package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/departments"
	"kamarapms/internal/pos"
)

// pms-seed pos: demo data of the POS (docs/architecture/34-pos.md) for a DEVELOPMENT database, through the services of the application. Safe to run again: what exists (by code) is left alone.
//
//	taxes and service charge  PB1 10% (on the service charge) and SVC 10%, when the property has none with that code
//	profiles                  FB (service 10% and PB1 10%, as the mockups) and TAXONLY (PB1 only)
//	price lists               RESTO, RS (room service) and LAUNDRY; the laundry has its own list and sells services only
//	outlets                   RESTO (print tickets), BAR (kitchen display), RS (room service, print) and LD (laundry, direct)
//	products                  food and drinks in the lists of the restaurant and of room service, laundry services in the list of the laundry
//	floor plans               indoors and terrace of the restaurant, the tables of the bar

type posProduct struct {
	code, name, short, category, charge, station string
	resto, rs, laundry                           string // the price in each list; empty = not sold there
}

var posProducts = []posProduct{
	{code: "NASGOR", name: "Nasi Goreng Kampung", short: "Nasgor", category: "MAINS", charge: "RESTAURANT", station: "KITCHEN", resto: "85000", rs: "95000"},
	{code: "MIEGOR", name: "Mie Goreng Seafood", short: "Mie seafood", category: "MAINS", charge: "RESTAURANT", station: "KITCHEN", resto: "95000", rs: "105000"},
	{code: "SATE", name: "Sate Ayam Madura", category: "MAINS", charge: "RESTAURANT", station: "KITCHEN", resto: "90000", rs: "100000"},
	{code: "BETUTU", name: "Bebek Betutu", category: "MAINS", charge: "RESTAURANT", station: "KITCHEN", resto: "165000", rs: "175000"},
	{code: "IKANBKR", name: "Ikan Bakar Jimbaran", category: "MAINS", charge: "RESTAURANT", station: "KITCHEN", resto: "185000", rs: "195000"},
	{code: "ESTEH", name: "Es Teh Manis", category: "DRINKS", charge: "RESTAURANT", station: "BAR", resto: "25000", rs: "28000"}, //nolint:misspell // Indonesian: es teh manis is sweet iced tea
	{code: "JUSALP", name: "Jus Alpukat", category: "DRINKS", charge: "RESTAURANT", station: "BAR", resto: "45000", rs: "50000"},
	{code: "KOPIBALI", name: "Kopi Bali", category: "DRINKS", charge: "RESTAURANT", station: "BAR", resto: "35000", rs: "40000"},
	{code: "KMEJA", name: "Cuci setrika kemeja", category: "LAUNDRY", charge: "LAUNDRY", laundry: "35000"},
	{code: "CELANA", name: "Cuci setrika celana", category: "LAUNDRY", charge: "LAUNDRY", laundry: "30000"},
	{code: "JAS", name: "Cuci kering jas", category: "LAUNDRY", charge: "LAUNDRY", laundry: "75000"},
	{code: "GAUN", name: "Cuci kering gaun", category: "LAUNDRY", charge: "LAUNDRY", laundry: "90000"},
	{code: "EKSPRES", name: "Layanan ekspres (6 jam)", category: "LAUNDRY", charge: "LAUNDRY", laundry: "25000"},
}

func seedPos(e *env, dryRun bool) error {
	ctx, pid := e.ctx, e.propertyID
	deptSvc := departments.NewService(e.txm, e.clk, e.aw, e.authz, e.ten)
	svc := pos.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, deptSvc)
	say := func(kind, code string, exists bool) {
		if exists {
			fmt.Printf("%s %s exists\n", kind, code)
		} else {
			fmt.Printf("%s %s: to create\n", kind, code)
		}
	}

	// the department of the sales: Food and beverage
	deps, err := deptSvc.List(ctx, pid, departments.Filter{})
	if err != nil {
		return err
	}
	var dept int64
	for _, d := range deps {
		if d.Code == "FB" && d.Active {
			dept = d.ID
		}
	}
	if dept == 0 {
		return fmt.Errorf("the property has no active department FB (Food and beverage)")
	}

	// the tax and the service charge
	taxes, err := e.bc.ListTaxes(ctx, pid, 0, nil, 200)
	if err != nil {
		return err
	}
	var taxID, svcID int64
	for _, t := range taxes {
		if t.Code == "PB1" {
			taxID = t.ID
		}
	}
	say("tax", "PB1", taxID != 0)
	if taxID == 0 && !dryRun {
		t, err := e.bc.CreateTax(ctx, pid, billingconfig.TaxInput{Code: "PB1", Name: "PB1 (pajak restoran)", Rate: "10", TaxOnService: true, IsActive: true})
		if err != nil {
			return fmt.Errorf("tax PB1: %w", err)
		}
		taxID = t.ID
	}
	charges, err := e.bc.ListServiceCharges(ctx, pid, 0, nil, 200)
	if err != nil {
		return err
	}
	for _, s := range charges {
		if s.Code == "SVC" {
			svcID = s.ID
		}
	}
	say("service charge", "SVC", svcID != 0)
	if svcID == 0 && !dryRun {
		s, err := e.bc.CreateServiceCharge(ctx, pid, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Biaya layanan", Rate: "10", IsActive: true})
		if err != nil {
			return fmt.Errorf("service charge SVC: %w", err)
		}
		svcID = s.ID
	}

	// profiles
	profiles, err := svc.ListRuleProfiles(ctx, pid)
	if err != nil {
		return err
	}
	profileID := map[string]int64{}
	for _, p := range profiles {
		profileID[p.Code] = p.ID
	}
	for _, spec := range []struct {
		code, name string
		rules      []pos.RuleInput
	}{
		{"FB", "F&B: servis 10% + PB1 10%", []pos.RuleInput{{ServiceChargeID: &svcID}, {TaxID: &taxID}}},
		{"TAXONLY", "Hanya pajak (PB1 10%)", []pos.RuleInput{{TaxID: &taxID}}},
	} {
		_, ok := profileID[spec.code]
		say("profile", spec.code, ok)
		if ok || dryRun {
			continue
		}
		p, err := svc.CreateRuleProfile(ctx, pid, pos.RuleProfileInput{Code: spec.code, Name: spec.name, PriceMode: "EXCLUSIVE", Rules: spec.rules})
		if err != nil {
			return fmt.Errorf("profile %s: %w", spec.code, err)
		}
		profileID[spec.code] = p.ID
	}

	// price lists
	lists, err := svc.ListPriceLists(ctx, pid)
	if err != nil {
		return err
	}
	listID := map[string]int64{}
	for _, l := range lists {
		listID[l.Code] = l.ID
	}
	for _, spec := range [][2]string{{"RESTO", "Harga restoran"}, {"RS", "Harga room service"}, {"LAUNDRY", "Harga laundry"}} {
		_, ok := listID[spec[0]]
		say("price list", spec[0], ok)
		if ok || dryRun {
			continue
		}
		l, err := svc.CreatePriceList(ctx, pid, pos.PriceListInput{Code: spec[0], Name: spec[1]})
		if err != nil {
			return fmt.Errorf("price list %s: %w", spec[0], err)
		}
		listID[spec[0]] = l.ID
	}

	// categories
	cats, err := svc.ListCategories(ctx, pid)
	if err != nil {
		return err
	}
	catID := map[string]int64{}
	for _, c := range cats {
		catID[c.Code] = c.ID
	}
	for i, spec := range [][2]string{{"MAINS", "Makanan utama"}, {"DRINKS", "Minuman"}, {"LAUNDRY", "Layanan laundry"}} {
		_, ok := catID[spec[0]]
		say("category", spec[0], ok)
		if ok || dryRun {
			continue
		}
		c, err := svc.CreateCategory(ctx, pid, pos.CategoryInput{Code: spec[0], Name: spec[1], SortOrder: i + 1})
		if err != nil {
			return fmt.Errorf("category %s: %w", spec[0], err)
		}
		catID[spec[0]] = c.ID
	}

	// printers and stations
	printers, err := svc.ListPrinters(ctx, pid)
	if err != nil {
		return err
	}
	printerID := map[string]int64{}
	for _, p := range printers {
		printerID[p.Name] = p.ID
	}
	for _, spec := range []pos.PrinterInput{
		{Name: "Printer struk kasir", Kind: "RECEIPT", ConnectionInfo: "192.168.1.50:9100", PaperWidthMM: 80, Codepage: "CP437", OpensDrawer: true},
		{Name: "Printer dapur", Kind: "KITCHEN", ConnectionInfo: "192.168.1.51:9100", PaperWidthMM: 80, Codepage: "CP437"},
	} {
		_, ok := printerID[spec.Name]
		say("printer", spec.Name, ok)
		if ok || dryRun {
			continue
		}
		p, err := svc.CreatePrinter(ctx, pid, spec)
		if err != nil {
			return fmt.Errorf("printer %s: %w", spec.Name, err)
		}
		printerID[spec.Name] = p.ID
	}
	stations, err := svc.ListStations(ctx, pid)
	if err != nil {
		return err
	}
	stationID := map[string]int64{}
	for _, s := range stations {
		stationID[s.Code] = s.ID
	}
	for _, spec := range []struct {
		code, name, printer string
		kds                 bool
		late                int
	}{{"KITCHEN", "Dapur utama", "Printer dapur", true, 15}, {"BAR", "Bar", "", true, 10}} {
		_, ok := stationID[spec.code]
		say("station", spec.code, ok)
		if ok || dryRun {
			continue
		}
		in := pos.StationInput{Code: spec.code, Name: spec.name, KDSEnabled: spec.kds, LateAfterMinutes: spec.late}
		if id, has := printerID[spec.printer]; has {
			in.PrinterID = &id
		}
		s, err := svc.CreateStation(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("station %s: %w", spec.code, err)
		}
		stationID[spec.code] = s.ID
	}

	// outlets
	existing, err := svc.ListOutlets(ctx, pid, nil)
	if err != nil {
		return err
	}
	outletID := map[string]int64{}
	for _, o := range existing {
		outletID[o.Code] = o.ID
	}
	receipt := printerID["Printer struk kasir"]
	for _, spec := range []struct {
		code, name, typ, mode, profile, list, drawer string
		tables, guests                               bool
		rounding                                     int
		comp                                         string
	}{
		{"RESTO", "Restoran Teratai", "RESTAURANT", "PRINT", "FB", "RESTO", "LACI1", true, true, 100, "PAYMENT_COMP"},
		{"BAR", "Bar Kolam", "BAR", "KDS", "FB", "RESTO", "LACI2", true, false, 0, "PAYMENT_COMP"},
		{"RS", "Room service", "ROOM_SERVICE", "PRINT", "FB", "RS", "", false, false, 0, "DISCOUNT_100"},
		{"LD", "Laundry", "LAUNDRY", "DIRECT", "TAXONLY", "LAUNDRY", "LACI3", false, false, 0, "PAYMENT_COMP"},
	} {
		_, ok := outletID[spec.code]
		say("outlet", spec.code, ok)
		if ok || dryRun {
			continue
		}
		in := pos.OutletInput{
			Code: spec.code, Name: spec.name, OutletType: spec.typ, OrderMode: spec.mode, DepartmentID: dept, RuleProfileID: profileID[spec.profile], PriceListID: listID[spec.list],
			UsesTables: spec.tables, GuestCountRequired: spec.guests, RoundingUnit: spec.rounding, AllowRoomCharge: true, AllowComp: true, CompTreatment: spec.comp,
		}
		if spec.drawer != "" {
			d := spec.drawer
			in.Drawer = &d
		}
		if receipt != 0 && spec.drawer != "" {
			in.ReceiptPrinterID = &receipt
		}
		o, err := svc.CreateOutlet(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("outlet %s: %w", spec.code, err)
		}
		outletID[spec.code] = o.ID
	}

	// charge codes
	codes, err := e.bc.ListChargeCodes(ctx, pid, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		return err
	}
	chargeID := map[string]int64{}
	for _, c := range codes {
		chargeID[c.Code] = c.ID
	}

	// products: the laundry list holds the laundry services only; the food is not sold at the laundry
	products, err := svc.ListProducts(ctx, pid, nil, nil)
	if err != nil {
		return err
	}
	haveProduct := map[string]bool{}
	for _, p := range products {
		haveProduct[p.Code] = true
	}
	for _, p := range posProducts {
		say("product", p.code, haveProduct[p.code])
		if haveProduct[p.code] || dryRun {
			continue
		}
		if chargeID[p.charge] == 0 {
			return fmt.Errorf("product %s: the property has no charge code %s", p.code, p.charge)
		}
		in := pos.ProductInput{Code: p.code, Name: p.name, CategoryID: catID[p.category], ChargeCodeID: chargeID[p.charge]}
		if p.short != "" {
			s := p.short
			in.ShortName = &s
		}
		if id, ok := stationID[p.station]; ok {
			in.StationID = &id
		}
		for list, price := range map[string]string{"RESTO": p.resto, "RS": p.rs, "LAUNDRY": p.laundry} {
			if price == "" {
				continue
			}
			in.Prices = append(in.Prices, pos.ProductPriceIn{PriceListID: listID[list], Price: decimalOf(price), IsSold: true})
		}
		if p.code == "NASGOR" {
			in.OptionGroups = []pos.OptionGroup{
				{Name: "Porsi", Kind: "VARIANT", MinSelect: 1, MaxSelect: 1, Options: []pos.Option{{Name: "Reguler", MaxQty: 1, IsDefault: true}, {Name: "Jumbo", PriceDelta: decimalOf("20000"), MaxQty: 1}}},
				{Name: "Level pedas", Kind: "VARIANT", MinSelect: 1, MaxSelect: 1, Options: []pos.Option{{Name: "Tidak pedas", MaxQty: 1}, {Name: "Sedang", MaxQty: 1, IsDefault: true}, {Name: "Pedas", MaxQty: 1}}},
				{Name: "Lauk tambahan", Kind: "MODIFIER", MinSelect: 0, MaxSelect: 3, Options: []pos.Option{
					{Name: "Telur ceplok", PriceDelta: decimalOf("8000"), MaxQty: 2}, {Name: "Kerupuk", PriceDelta: decimalOf("5000"), MaxQty: 1}, {Name: "Sate ayam 2 tusuk", PriceDelta: decimalOf("20000"), MaxQty: 1},
				}},
			}
		}
		if _, err := svc.CreateProduct(ctx, pid, in); err != nil {
			return fmt.Errorf("product %s: %w", p.code, err)
		}
	}

	// floor plans
	for _, plan := range []struct {
		outlet string
		areas  []struct {
			name string
			n    int
			cols int
		}
	}{
		{"RESTO", []struct {
			name string
			n    int
			cols int
		}{{"Dalam ruangan", 12, 4}, {"Teras", 4, 4}}},
		{"BAR", []struct {
			name string
			n    int
			cols int
		}{{"Bar kolam", 6, 3}}},
	} {
		oid := outletID[plan.outlet]
		if oid == 0 || dryRun {
			fmt.Printf("floor plan %s: to create\n", plan.outlet)
			continue
		}
		fp, err := svc.GetFloorPlan(ctx, pid, oid)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, a := range fp.Areas {
			have[a.Name] = true
		}
		for i, spec := range plan.areas {
			if have[spec.name] {
				fmt.Printf("floor plan %s / %s exists\n", plan.outlet, spec.name)
				continue
			}
			a, err := svc.CreateArea(ctx, pid, oid, pos.AreaInput{Name: spec.name, SortOrder: i + 1})
			if err != nil {
				return fmt.Errorf("area %s: %w", spec.name, err)
			}
			var tables []pos.Table
			for n := 0; n < spec.n; n++ {
				shape := "SQUARE"
				if (n/spec.cols+n%spec.cols)%3 == 0 {
					shape = "ROUND"
				}
				tables = append(tables, pos.Table{Code: tableCode(plan.outlet, spec.name, n+1), Capacity: 2 + 2*((n+1)%2), Shape: shape, X: 3 + (n%spec.cols)*8, Y: 3 + (n/spec.cols)*6, W: 4, H: 4, IsActive: true})
			}
			if _, err := svc.SaveLayout(ctx, pid, a.ID, pos.LayoutInput{Name: a.Name, SortOrder: a.SortOrder, GridW: a.GridW, GridH: a.GridH, LayoutVersion: a.LayoutVersion, Tables: tables}); err != nil {
				return fmt.Errorf("layout %s: %w", spec.name, err)
			}
			fmt.Printf("floor plan %s / %s: %d tables created\n", plan.outlet, spec.name, len(tables))
		}
	}
	if dryRun {
		fmt.Println("dry run: nothing was created")
	}
	return nil
}

// tableCode numbers the tables of an outlet: 1..N indoors, T1.. on a terrace, P1.. at a pool bar.
func tableCode(outlet, area string, n int) string {
	switch strings.ToLower(area) {
	case "teras":
		return "T" + strconv.Itoa(n)
	case "bar kolam":
		return "P" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func decimalOf(s string) decimal.Decimal { return decimal.RequireFromString(s) }
