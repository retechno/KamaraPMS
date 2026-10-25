package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
	"kamarapms/internal/billingconfig"
)

// pms-seed banquet: demo data of the banquet (docs/architecture/36-banquet.md, PR 1) for a DEVELOPMENT database, through the service of the application. Safe to run again: what exists (by code)
// is left alone.
//
//	rooms       Saraswati A and B (and the Ballroom made of the two), Ubud 1 and 2, Kenanga, Taman Teratai, with the capacity of each set-up
//	types       Pernikahan, Rapat, Gala dinner, Seminar
//	reasons     why an event is lost or cancelled
//	items       a few menus, a drink package, equipment, decoration and the rental of a room
//	packages    "Fullday Meeting" at 363,000 nett per guest, four components (PR 2)
//	policy      the template STANDARD (default): deposits 30% at signature, 50% at H-30, 20% at H-7; cancellation tiers 0/25/50/100% of the net revenue (PR 2)

type bqRoom struct {
	code, name      string
	setup, teardown int
	parts           []string
	caps            map[string]int
	sort            int
}

var bqRooms = []bqRoom{
	{code: "SARA", name: "Saraswati A", setup: 60, teardown: 30, caps: map[string]int{"THEATER": 120, "CLASSROOM": 70, "ROUND_TABLE": 80, "COCKTAIL": 150}, sort: 1},
	{code: "SARB", name: "Saraswati B", setup: 60, teardown: 30, caps: map[string]int{"THEATER": 120, "CLASSROOM": 70, "ROUND_TABLE": 80, "COCKTAIL": 150}, sort: 2},
	{code: "BALL", name: "Ballroom Saraswati", setup: 90, teardown: 45, parts: []string{"SARA", "SARB"}, caps: map[string]int{"THEATER": 300, "CLASSROOM": 160, "ROUND_TABLE": 200, "COCKTAIL": 350}, sort: 3},
	{code: "UBUD1", name: "Ubud 1", setup: 30, teardown: 15, caps: map[string]int{"U_SHAPE": 20, "BOARDROOM": 24, "CLASSROOM": 30}, sort: 4},
	{code: "UBUD2", name: "Ubud 2", setup: 30, teardown: 15, caps: map[string]int{"U_SHAPE": 20, "BOARDROOM": 24, "CLASSROOM": 30}, sort: 5},
	{code: "KENA", name: "Kenanga", setup: 45, teardown: 20, caps: map[string]int{"THEATER": 60, "CLASSROOM": 36, "BOARDROOM": 30, "ROUND_TABLE": 40}, sort: 6},
	{code: "TERA", name: "Taman Teratai", setup: 60, teardown: 30, caps: map[string]int{"ROUND_TABLE": 120, "COCKTAIL": 200}, sort: 7},
}

var bqTypes = [][2]string{{"WEDDING", "Pernikahan"}, {"MEETING", "Rapat"}, {"GALA", "Gala dinner"}, {"SEMINAR", "Seminar"}}

var bqReasons = []struct{ kind, code, name string }{
	{"LOST", "PRICE", "Harga terlalu tinggi"},
	{"LOST", "DATE", "Tanggal tidak cocok"},
	{"LOST", "COMPETITOR", "Memilih tempat lain"},
	{"CANCEL", "POSTPONED", "Acara ditunda"},
	{"CANCEL", "CLIENT", "Dibatalkan pemesan"},
}

type bqItem struct {
	code, name, kind, basis, price, charge string // charge: the type of the charge code
	fb                                     string
}

var bqItems = []bqItem{
	{code: "COFFEEBRK", name: "Coffee break", kind: "MENU", basis: "PER_PAX", price: "40000", charge: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "LUNCH", name: "Makan siang prasmanan", kind: "MENU", basis: "PER_PAX", price: "150000", charge: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "DINNER", name: "Makan malam set menu", kind: "MENU", basis: "PER_PAX", price: "225000", charge: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "SOFTDRINK", name: "Paket minuman ringan", kind: "DRINK_PACKAGE", basis: "PER_PAX", price: "60000", charge: billingconfig.TypeFoodBeverage, fb: "BEVERAGE"},
	{code: "PROJECTOR", name: "Proyektor dan layar", kind: "EQUIPMENT", basis: "PER_SESSION", price: "750000", charge: "SERVICE"},
	{code: "SOUND", name: "Sound system", kind: "EQUIPMENT", basis: "PER_SESSION", price: "1500000", charge: "SERVICE"},
	{code: "FLOWERS", name: "Dekorasi bunga meja", kind: "DECOR", basis: "PER_UNIT", price: "350000", charge: "SERVICE"},
	{code: "ROOMPAX", name: "Sewa ruang per tamu", kind: "ROOM_RENTAL", basis: "PER_PAX", price: "50000", charge: billingconfig.TypeRoom},
	{code: "EQUIPPAX", name: "Peralatan rapat per tamu", kind: "EQUIPMENT", basis: "PER_PAX", price: "20000", charge: "SERVICE"},
	{code: "ROOMRENT", name: "Sewa ruang per jam", kind: "ROOM_RENTAL", basis: "PER_HOUR", price: "500000", charge: billingconfig.TypeRoom},
}

func seedBanquet(e *env, dryRun bool) error {
	svc := banquet.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, nil, e.bc, nil) // the seed makes masters and templates only: no folio, no approval
	_, err := seedBanquetWith(e.ctx, svc, e.bc, e.propertyID, dryRun, os.Stdout)
	return err
}

// seedBanquetWith does the work; the return value is the number of rows created (or that would be).
func seedBanquetWith(ctx context.Context, svc *banquet.Service, bc *billingconfig.Service, pid int64, dryRun bool, out io.Writer) (int, error) {
	made := 0
	say := func(kind, code string, exists bool) {
		if exists {
			_, _ = fmt.Fprintf(out, "%s %s exists\n", kind, code)
			return
		}
		made++
		_, _ = fmt.Fprintf(out, "%s %s: to create\n", kind, code)
	}

	// the rooms: the base rooms first, then the combined ones (made of base rooms that exist by then)
	rooms, err := svc.ListRooms(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	roomID := map[string]int64{}
	for _, r := range rooms {
		roomID[r.Code] = r.ID
	}
	for _, combo := range []bool{false, true} {
		for _, spec := range bqRooms {
			if (len(spec.parts) > 0) != combo {
				continue
			}
			_, ok := roomID[spec.code]
			say("meeting room", spec.code, ok)
			if ok || dryRun {
				continue
			}
			active := true
			r, err := svc.CreateRoom(ctx, pid, banquet.RoomInput{Code: spec.code, Name: spec.name, SetupMinutes: spec.setup, TeardownMinutes: spec.teardown, IsCombo: combo, IsActive: &active, SortOrder: spec.sort})
			if err != nil {
				return 0, fmt.Errorf("room %s: %w", spec.code, err)
			}
			roomID[spec.code] = r.ID
			if combo {
				ids := make([]int64, 0, len(spec.parts))
				for _, p := range spec.parts {
					ids = append(ids, roomID[p])
				}
				if _, err := svc.SetRoomParts(ctx, pid, r.ID, banquet.PartsInput{RoomIDs: ids}); err != nil {
					return 0, fmt.Errorf("room %s parts: %w", spec.code, err)
				}
			}
			setups := make([]banquet.RoomSetup, 0, len(spec.caps))
			for _, style := range banquet.SetupStyles {
				if c, ok := spec.caps[style]; ok {
					setups = append(setups, banquet.RoomSetup{SetupStyle: style, Capacity: c})
				}
			}
			if _, err := svc.SetRoomSetups(ctx, pid, r.ID, banquet.SetupsInput{Setups: setups}); err != nil {
				return 0, fmt.Errorf("room %s set-ups: %w", spec.code, err)
			}
		}
	}

	types, err := svc.ListEventTypes(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	haveType := map[string]bool{}
	for _, t := range types {
		haveType[t.Code] = true
	}
	for _, spec := range bqTypes {
		say("event type", spec[0], haveType[spec[0]])
		if haveType[spec[0]] || dryRun {
			continue
		}
		if _, err := svc.CreateEventType(ctx, pid, banquet.EventTypeInput{Code: spec[0], Name: spec[1]}); err != nil {
			return 0, fmt.Errorf("event type %s: %w", spec[0], err)
		}
	}

	reasons, err := svc.ListReasons(ctx, pid, nil, nil)
	if err != nil {
		return 0, err
	}
	haveReason := map[string]bool{}
	for _, r := range reasons {
		haveReason[r.Kind+"/"+r.Code] = true
	}
	for _, spec := range bqReasons {
		ok := haveReason[spec.kind+"/"+spec.code]
		say("reason", spec.code, ok)
		if ok || dryRun {
			continue
		}
		if _, err := svc.CreateReason(ctx, pid, banquet.ReasonInput{Kind: spec.kind, Code: spec.code, Name: spec.name}); err != nil {
			return 0, fmt.Errorf("reason %s: %w", spec.code, err)
		}
	}

	// the items need a charge code of the type that fits: the first active one of that type
	codes, err := bc.ListChargeCodes(ctx, pid, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		return 0, err
	}
	chargeOf := func(kind string) (int64, bool) {
		for _, c := range codes {
			if c.IsActive && c.ChargeType == kind {
				return c.ID, true
			}
		}
		for _, c := range codes {
			if c.IsActive && c.ChargeType == billingconfig.TypeOther {
				return c.ID, true
			}
		}
		return 0, false
	}
	items, err := svc.ListItems(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	itemID := map[string]int64{}
	for _, it := range items {
		itemID[it.Code] = it.ID
	}
	for _, spec := range bqItems {
		_, ok := itemID[spec.code]
		say("item", spec.code, ok)
		if ok || dryRun {
			continue
		}
		charge, found := chargeOf(spec.charge)
		if !found {
			_, _ = fmt.Fprintf(out, "item %s: skipped, the property has no active %s charge code\n", spec.code, spec.charge)
			made--
			continue
		}
		in := banquet.ItemInput{Code: spec.code, Name: spec.name, Kind: spec.kind, UnitBasis: spec.basis, ChargeCodeID: charge, ListPrice: decimal.RequireFromString(spec.price)}
		if spec.fb != "" {
			fb := spec.fb
			in.FbClass = &fb
		}
		it, err := svc.CreateItem(ctx, pid, in)
		if err != nil {
			return 0, fmt.Errorf("item %s: %w", spec.code, err)
		}
		itemID[spec.code] = it.ID
	}

	packages, err := svc.ListPackages(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	havePkg := false
	for _, p := range packages {
		havePkg = havePkg || p.Code == "FULLDAY"
	}
	say("package", "FULLDAY", havePkg)
	if !havePkg && !dryRun {
		var comps []banquet.ComponentInput
		for _, c := range []struct{ code, qty string }{{"LUNCH", "1"}, {"COFFEEBRK", "2"}, {"ROOMPAX", "1"}, {"EQUIPPAX", "1"}} {
			if id, ok := itemID[c.code]; ok {
				comps = append(comps, banquet.ComponentInput{ItemID: id, QtyPerPax: decimal.RequireFromString(c.qty)})
			}
		}
		if len(comps) == 0 {
			_, _ = fmt.Fprintln(out, "package FULLDAY: skipped, no item to put in it")
			made--
		} else if _, err := svc.CreatePackage(ctx, pid, banquet.PackageInput{Code: "FULLDAY", Name: "Fullday Meeting", PricePerPax: decimal.RequireFromString("363000"), Components: comps}); err != nil {
			return 0, fmt.Errorf("package FULLDAY: %w", err)
		}
	}

	// the policy template: a starting point copied to an event when it is confirmed
	templates, err := svc.ListPolicyTemplates(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	haveTemplate := false
	for _, tpl := range templates {
		haveTemplate = haveTemplate || tpl.Code == "STANDARD"
	}
	say("policy template", "STANDARD", haveTemplate)
	if !haveTemplate && !dryRun {
		days := func(n int) *int { return &n }
		pct := decimal.RequireFromString
		in := banquet.PolicyTemplateInput{
			Code: "STANDARD", Name: "Standar", IsDefault: len(templates) == 0, Base: "NET",
			Deposits: []banquet.TemplateDeposit{
				{Seq: 1, Label: "Tanda tangan", Percent: pct("30")},
				{Seq: 2, Label: "H-30", DaysBefore: days(30), Percent: pct("50")},
				{Seq: 3, Label: "H-7", DaysBefore: days(7), Percent: pct("20")},
			},
			Tiers: []banquet.TierInput{
				{DaysFrom: 60, Percent: pct("0"), Action: "FEE"},
				{DaysFrom: 30, DaysTo: days(59), Percent: pct("25"), Action: "FEE"},
				{DaysFrom: 7, DaysTo: days(29), Percent: pct("50"), Action: "FEE"},
				{DaysFrom: 0, DaysTo: days(6), Percent: pct("100"), Action: "FEE"},
			},
		}
		if _, err := svc.CreatePolicyTemplate(ctx, pid, in); err != nil {
			return 0, fmt.Errorf("policy template STANDARD: %w", err)
		}
	}

	verb := "created"
	if dryRun {
		verb = "would create"
	}
	_, _ = fmt.Fprintf(out, "%s %d banquet rows\n", verb, made)
	return made, nil
}
