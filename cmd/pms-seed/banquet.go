package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/departments"
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
//	stock       the lunch and the coffee break linked to the POS products Nasi Goreng and Kopi Bali (they have a recipe), and the kitchen warehouse set for the banquet, when the POS and inventory demos are there (PR 4)
//	beo         the department that reads each section of the BEO (kitchen and F&B service: FB; housekeeping and front office: ROOMS; engineering: POM; security: AG), where the section has none and the
//	            department exists; and a sample text for each section of each session of a Definite event whose BEO is still empty (PR 3)

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
	code, name, kind, basis, price string
	charge                         string // the charge code of the banquet that the item belongs to
	legacyType                     string // the type the first version of this seed looked for (the first active code of that type was taken)
	oldPrice                       string // the price the first version gave, so that an item nobody changed can follow the document
	fb                             string
}

var bqItems = []bqItem{
	{code: "COFFEEBRK", name: "Coffee break", kind: "MENU", basis: "PER_PAX", price: "40000", oldPrice: "45000", charge: "BQT_FB", legacyType: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "LUNCH", name: "Makan siang prasmanan", kind: "MENU", basis: "PER_PAX", price: "150000", oldPrice: "135000", charge: "BQT_FB", legacyType: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "DINNER", name: "Makan malam set menu", kind: "MENU", basis: "PER_PAX", price: "225000", charge: "BQT_FB", legacyType: billingconfig.TypeFoodBeverage, fb: "FOOD"},
	{code: "SOFTDRINK", name: "Paket minuman ringan", kind: "DRINK_PACKAGE", basis: "PER_PAX", price: "60000", charge: "BQT_FB", legacyType: billingconfig.TypeFoodBeverage, fb: "BEVERAGE"},
	{code: "PROJECTOR", name: "Proyektor dan layar", kind: "EQUIPMENT", basis: "PER_SESSION", price: "750000", charge: "BQT_EQUIPMENT", legacyType: "SERVICE"},
	{code: "SOUND", name: "Sound system", kind: "EQUIPMENT", basis: "PER_SESSION", price: "1500000", charge: "BQT_EQUIPMENT", legacyType: "SERVICE"},
	{code: "FLOWERS", name: "Dekorasi bunga meja", kind: "DECOR", basis: "PER_UNIT", price: "350000", charge: "BQT_DECOR", legacyType: "SERVICE"},
	{code: "ROOMPAX", name: "Sewa ruang per tamu", kind: "ROOM_RENTAL", basis: "PER_PAX", price: "50000", charge: "BQT_ROOM", legacyType: billingconfig.TypeRoom},
	{code: "EQUIPPAX", name: "Peralatan rapat per tamu", kind: "EQUIPMENT", basis: "PER_PAX", price: "20000", charge: "BQT_EQUIPMENT", legacyType: "SERVICE"},
	{code: "ROOMRENT", name: "Sewa ruang per jam", kind: "ROOM_RENTAL", basis: "PER_HOUR", price: "500000", charge: "BQT_ROOM", legacyType: billingconfig.TypeRoom},
}

// bqRuleCodes are the charge codes of the banquet that carry the service charge and the tax of the demo (BQT_FORFEIT has none).
var bqRuleCodes = []string{"BQT_FB", "BQT_ROOM", "BQT_EQUIPMENT", "BQT_DECOR", "BQT_OTHER"}

func seedBanquet(e *env, dryRun bool) error {
	svc := banquet.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, nil, e.bc, nil) // the seed makes masters and templates only: no folio, no approval
	deps := departments.NewService(e.txm, e.clk, e.aw, e.authz, e.ten)
	if _, err := seedBanquetWith(e.ctx, svc, deps, e.bc, e.propertyID, dryRun, os.Stdout); err != nil {
		return err
	}
	_, err := seedBanquetStock(e.ctx, svc, e.txm.DB(e.ctx), e.propertyID, dryRun, os.Stdout)
	return err
}

// seedBanquetWith does the work; the return value is the number of rows created (or that would be).
func seedBanquetWith(ctx context.Context, svc *banquet.Service, deps *departments.Service, bc *billingconfig.Service, pid int64, dryRun bool, out io.Writer) (int, error) {
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

	// the charge codes of the banquet carry the service charge and the tax of the demo, as the food and beverage of the other seeds do: SVC 10% and PB1 10% (on the service)
	codes, err := bc.ListChargeCodes(ctx, pid, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		return 0, err
	}
	byCode := map[string]billingconfig.ChargeCode{}
	for _, c := range codes {
		byCode[c.Code] = c
	}
	taxes, err := bc.ListTaxes(ctx, pid, 0, nil, 200)
	if err != nil {
		return 0, err
	}
	var taxID, svcID int64
	for _, t := range taxes {
		if t.Code == "PB1" {
			taxID = t.ID
		}
	}
	say("tax", "PB1", taxID != 0)
	if taxID == 0 && !dryRun {
		t, err := bc.CreateTax(ctx, pid, billingconfig.TaxInput{Code: "PB1", Name: "PB1 (pajak restoran)", Rate: "10", TaxOnService: true, IsActive: true})
		if err != nil {
			return 0, fmt.Errorf("tax PB1: %w", err)
		}
		taxID = t.ID
	}
	charges, err := bc.ListServiceCharges(ctx, pid, 0, nil, 200)
	if err != nil {
		return 0, err
	}
	for _, c := range charges {
		if c.Code == "SVC" {
			svcID = c.ID
		}
	}
	say("service charge", "SVC", svcID != 0)
	if svcID == 0 && !dryRun {
		c, err := bc.CreateServiceCharge(ctx, pid, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Biaya layanan", Rate: "10", IsActive: true})
		if err != nil {
			return 0, fmt.Errorf("service charge SVC: %w", err)
		}
		svcID = c.ID
	}
	for _, code := range bqRuleCodes {
		cc, ok := byCode[code]
		if !ok {
			_, _ = fmt.Fprintf(out, "charge code %s: warning, the property has none (a property made before migration 00090)\n", code)
			continue
		}
		if len(cc.Taxes) > 0 || len(cc.ServiceCharges) > 0 {
			_, _ = fmt.Fprintf(out, "charge code %s rules exist\n", code)
			continue
		}
		say("charge code rules", code, false)
		if dryRun {
			continue
		}
		if _, err := bc.ReplaceRules(ctx, pid, cc.ID, billingconfig.RulesInput{
			ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svcID, Sequence: 1}},
			Taxes:          []billingconfig.TaxRuleInput{{TaxID: taxID, Sequence: 1}},
		}); err != nil {
			return 0, fmt.Errorf("charge code %s rules: %w", code, err)
		}
	}

	// an item belongs to the charge code of its kind by CODE; the type is only a fallback (with a warning) for a property that has no such code
	legacy := func(kind string) (int64, bool) { // what the first version of this seed took
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
	chargeOf := func(spec bqItem) (int64, bool) {
		if c, ok := byCode[spec.charge]; ok && c.IsActive {
			return c.ID, true
		}
		_, _ = fmt.Fprintf(out, "item %s: warning, no active charge code %s: the first one of type %s is used\n", spec.code, spec.charge, spec.legacyType)
		return legacy(spec.legacyType)
	}
	items, err := svc.ListItems(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	have := map[string]banquet.Item{}
	itemID := map[string]int64{}
	for _, it := range items {
		have[it.Code] = it
		itemID[it.Code] = it.ID
	}
	for _, spec := range bqItems {
		if old, ok := have[spec.code]; ok {
			// an item that exists is fixed only while nobody changed it: it still has the charge code (and the price) the first version gave
			want, wantOK := byCode[spec.charge]
			legacyID, _ := legacy(spec.legacyType)
			price := decimal.RequireFromString(spec.price)
			// the price read from the database carries the scale of the column (1500000.000): sent back as it is, it has more decimals than a currency of 0 or 2 does
			newCharge, newPrice := old.ChargeCodeID, decimal.RequireFromString(old.ListPrice.String())
			if wantOK && old.ChargeCodeID != want.ID {
				if old.ChargeCodeID == legacyID {
					newCharge = want.ID
				} else {
					_, _ = fmt.Fprintf(out, "item %s exists: warning, charge code %s kept (the demo says %s)\n", spec.code, old.ChargeCode, spec.charge)
				}
			}
			if spec.oldPrice != "" && old.ListPrice.Equal(decimal.RequireFromString(spec.oldPrice)) && !old.ListPrice.Equal(price) {
				newPrice = price
			}
			if newCharge == old.ChargeCodeID && newPrice.Equal(old.ListPrice) {
				say("item", spec.code, true)
				continue
			}
			_, _ = fmt.Fprintf(out, "item %s exists: to fix (charge code, price)\n", spec.code)
			made++
			if dryRun {
				continue
			}
			active := old.IsActive
			if _, err := svc.UpdateItem(ctx, pid, old.ID, banquet.ItemInput{Code: old.Code, Name: old.Name, Kind: old.Kind, UnitBasis: old.UnitBasis, ChargeCodeID: newCharge, ListPrice: newPrice,
				FbClass: old.FbClass, PosProductID: old.PosProductID, Description: old.Description, IsActive: &active}); err != nil {
				return 0, fmt.Errorf("item %s: %w", spec.code, err)
			}
			continue
		}
		charge, found := chargeOf(spec)
		say("item", spec.code, false)
		if dryRun {
			continue
		}
		if !found {
			_, _ = fmt.Fprintf(out, "item %s: skipped, the property has no active charge code for it\n", spec.code)
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
	var existing *banquet.Package
	for i := range packages {
		if packages[i].Code == "FULLDAY" {
			existing = &packages[i]
		}
	}
	// the package of the document (36 10.1): 363,000 nett per guest = lunch 150,000 + coffee break 2 x 40,000 + room 50,000 + equipment 20,000 = 300,000 net, with 10% service and 10% tax on it
	want := []struct{ code, qty string }{{"LUNCH", "1"}, {"COFFEEBRK", "2"}, {"ROOMPAX", "1"}, {"EQUIPPAX", "1"}}
	input := func() (banquet.PackageInput, bool) {
		var comps []banquet.ComponentInput
		for _, c := range want {
			id, ok := itemID[c.code]
			if !ok {
				return banquet.PackageInput{}, false
			}
			comps = append(comps, banquet.ComponentInput{ItemID: id, QtyPerPax: decimal.RequireFromString(c.qty)})
		}
		active := true
		if existing != nil {
			active = existing.IsActive
		}
		return banquet.PackageInput{Code: "FULLDAY", Name: "Fullday Meeting", PricePerPax: decimal.RequireFromString("363000"), IsActive: &active, Components: comps}, true
	}
	switch {
	case existing == nil:
		say("package", "FULLDAY", false)
		if dryRun {
			break
		}
		in, ok := input()
		if !ok {
			_, _ = fmt.Fprintln(out, "package FULLDAY: skipped, an item of it is missing")
			made--
			break
		}
		if _, err := svc.CreatePackage(ctx, pid, in); err != nil {
			return 0, fmt.Errorf("package FULLDAY: %w", err)
		}
	case bqPackageIsTheFirstVersion(*existing):
		_, _ = fmt.Fprintln(out, "package FULLDAY exists: to fix (the price and the four components of the document)")
		made++
		if dryRun {
			break
		}
		in, ok := input()
		if !ok {
			_, _ = fmt.Fprintln(out, "package FULLDAY: skipped, an item of it is missing")
			made--
			break
		}
		if _, err := svc.UpdatePackage(ctx, pid, existing.ID, in); err != nil {
			return 0, fmt.Errorf("package FULLDAY: %w", err)
		}
	default:
		say("package", "FULLDAY", true)
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

	n, err := seedBeo(ctx, svc, deps, pid, dryRun, out)
	if err != nil {
		return 0, err
	}
	made += n

	verb := "created"
	if dryRun {
		verb = "would create"
	}
	_, _ = fmt.Fprintf(out, "%s %d banquet rows\n", verb, made)
	return made, nil
}

// bqPackageIsTheFirstVersion says the package is still what the first version of this seed made (285,000 and the coffee break twice, the lunch and the soft drink): nobody changed it, so it follows the document.
func bqPackageIsTheFirstVersion(p banquet.Package) bool {
	if !p.PricePerPax.Equal(decimal.RequireFromString("285000")) || len(p.Components) != 3 {
		return false
	}
	qty := map[string]string{}
	for _, c := range p.Components {
		qty[c.ItemCode] = c.QtyPerPax.String()
	}
	return qty["COFFEEBRK"] == "2" && qty["LUNCH"] == "1" && qty["SOFTDRINK"] == "1"
}

// bqSectionDepartment is the department of the demo that reads each section of the BEO (the standard departments every property has).
var bqSectionDepartment = map[string]string{
	banquet.SectionKitchen: "FB", banquet.SectionFBService: "FB", banquet.SectionHousekeeping: "ROOMS", banquet.SectionFrontOffice: "ROOMS", banquet.SectionEngineering: "POM", banquet.SectionSecurity: "AG",
}

// bqBeoText is the sample text of a section of the BEO of a session.
var bqBeoText = map[string]string{
	banquet.SectionGeneral:      "Tamu undangan datang mulai satu jam sebelum acara. PIC acara: sales yang menangani.",
	banquet.SectionKitchen:      "Menu sesuai paket. Siapkan 5% porsi cadangan. Tanpa kacang; sediakan menu vegetarian untuk 10 tamu.",
	banquet.SectionFBService:    "Susunan meja sesuai set-up. Welcome drink saat tamu datang, coffee break 30 menit sebelum akhir sesi.",
	banquet.SectionHousekeeping: "Ruang dan toilet terdekat siap satu jam sebelum acara; periksa lagi saat coffee break.",
	banquet.SectionEngineering:  "Periksa AC, lampu, dan sound system sehari sebelumnya. Teknisi siaga selama acara.",
	banquet.SectionSecurity:     "Dua petugas di pintu masuk ruang; parkir tamu diarahkan ke area barat.",
	banquet.SectionFrontOffice:  "Papan petunjuk di lobi. Resepsionis tahu nama acara dan ruangnya.",
}

// seedBeo maps the sections of the BEO to departments and gives the Definite events that have no BEO yet a sample one. It returns how many rows it made (or would make).
func seedBeo(ctx context.Context, svc *banquet.Service, deps *departments.Service, pid int64, dryRun bool, out io.Writer) (int, error) {
	made := 0
	if deps != nil {
		list, err := deps.List(ctx, pid, departments.Filter{})
		if err != nil {
			return 0, err
		}
		byCode := map[string]int64{}
		for _, d := range list {
			byCode[d.Code] = d.ID
		}
		have, err := svc.ListRecipients(ctx, pid)
		if err != nil {
			return 0, err
		}
		var items []banquet.RecipientItem
		for _, r := range have {
			code := bqSectionDepartment[r.Section]
			id, ok := byCode[code]
			if r.DepartmentID != nil || !ok {
				_, _ = fmt.Fprintf(out, "BEO recipient %s exists\n", r.Section)
				continue
			}
			made++
			_, _ = fmt.Fprintf(out, "BEO recipient %s: to create (%s)\n", r.Section, code)
			items = append(items, banquet.RecipientItem{Section: r.Section, DepartmentID: &id})
		}
		if len(items) > 0 && !dryRun {
			if _, err := svc.PutRecipients(ctx, pid, banquet.RecipientsInput{Items: items}); err != nil {
				return 0, fmt.Errorf("BEO recipients: %w", err)
			}
		}
	}

	events, err := svc.ListEvents(ctx, pid, banquet.EventFilter{Statuses: []string{"DEFINITE"}, Limit: 200})
	if err != nil {
		return 0, err
	}
	for _, ev := range events {
		view, err := svc.Beo(ctx, pid, ev.ID)
		if err != nil {
			return 0, err
		}
		for _, s := range view.Sessions {
			empty := s.Status == "ACTIVE" && len(s.RunOfShow) == 0
			for _, x := range s.Sections {
				empty = empty && strings.TrimSpace(x.Body) == ""
			}
			if !empty {
				continue
			}
			made++
			_, _ = fmt.Fprintf(out, "BEO %s session %d: to create\n", ev.Number, s.No)
			if dryRun {
				continue
			}
			in := banquet.BeoInput{Sections: bqBeoText}
			if s.Start != nil {
				in.RunOfShow = []banquet.RunLineInput{{At: *s.Start, Text: "Tamu mulai datang dan registrasi"}}
			}
			if _, err := svc.SaveBeo(ctx, pid, ev.ID, s.ID, in); err != nil {
				return 0, fmt.Errorf("BEO %s session %d: %w", ev.Number, s.No, err)
			}
		}
	}
	return made, nil
}
