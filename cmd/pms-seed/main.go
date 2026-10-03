// Command pms-seed fills a DEVELOPMENT database with demo data, through the same services the application uses (so
// every row has its audit entry and the rooms their housekeeping state).
//
//	pms-seed rooms -tenant DEMO -property BALI [-floors 10] [-per-floor 10] [-dry-run]
//	pms-seed rates -tenant DEMO -property BALI [-days 90] [-plan RO] [-dry-run]
//
// rooms: creates the room types Standard, Superior, Deluxe and Suite when the property lacks them and rooms numbered
// floor*100+n (101..110, 201..210, ...), spread over the types, with a mix of housekeeping states and a bed type each
// (Standard: Twin or Queen, Superior: Double, Deluxe and Suite: King). Rooms that exist without a bed type get one.
//
// rates: creates the rate plan (Room Only, sold through the ROOM charge code) when the property lacks it and fills the
// grid of every room type for the next days from the business date: a weekday price and a higher weekend price
// (Friday and Saturday).
//
// Both are safe to run again (rooms that exist are left alone; rates are written again with the same prices). They
// refuse to run against a production configuration. They read PMS_DATABASE_URL from the environment or ./.env.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rates"
	"kamarapms/internal/rooms"
	"kamarapms/internal/tenancy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pms-seed:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  pms-seed rooms -tenant CODE -property CODE [-floors N] [-per-floor N] [-dry-run]
  pms-seed rates -tenant CODE -property CODE [-days N] [-plan CODE] [-dry-run]`

// A room type the seed creates when the property has no type with that code, and the price of its nights.
type typeSpec struct {
	code, name, description string
	maxAdult, maxChild      int32
	// positions are the places on a floor (1 to 10) that have this type.
	positions []int
	// weekday and weekend are the nightly prices (Friday and Saturday are the weekend).
	weekday, weekend string
}

var types = []typeSpec{
	{code: "STD", name: "Standard", description: "Standard room", maxAdult: 2, maxChild: 1, positions: []int{1, 2, 3, 4}, weekday: "500000", weekend: "600000"},
	{code: "SUP", name: "Superior", description: "Superior room", maxAdult: 2, maxChild: 1, positions: []int{5, 6, 7}, weekday: "650000", weekend: "780000"},
	{code: "DLX", name: "Deluxe", description: "Deluxe room", maxAdult: 2, maxChild: 2, positions: []int{8, 9}, weekday: "800000", weekend: "960000"},
	{code: "STE", name: "Suite", description: "Suite", maxAdult: 3, maxChild: 2, positions: []int{10}, weekday: "1500000", weekend: "1800000"},
}

// A housekeeping state for a new room, by its number: most are ready, some need work, so the boards look alive.
var states = []housekeeping.Status{housekeeping.Clean, housekeeping.Clean, housekeeping.Inspected, housekeeping.Dirty, housekeeping.Clean, housekeeping.Cleaning, housekeeping.Inspected}

// env is what a seed command works with: the services, the tenant's administrator as the actor, and the property.
type env struct {
	ctx        context.Context
	tenant     tenancy.Tenant
	propertyID int64
	ten        *tenancy.Service
	rm         *rooms.Service
	rt         *rates.Service
	bc         *billingconfig.Service
}

func run(args []string) error {
	if len(args) == 0 || (args[0] != "rooms" && args[0] != "rates") {
		return fmt.Errorf("%s", usage)
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tenantCode := fs.String("tenant", "", "tenant code")
	propertyCode := fs.String("property", "", "property code")
	floors := fs.Int("floors", 10, "rooms: number of floors (1 to 30)")
	perFloor := fs.Int("per-floor", 10, "rooms: rooms per floor (10, so the types spread evenly)")
	days := fs.Int("days", 90, "rates: nights to price from the business date (1 to 365)")
	plan := fs.String("plan", "RO", "rates: the rate plan code")
	dryRun := fs.Bool("dry-run", false, "show what would be created, create nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *tenantCode == "" || *propertyCode == "" {
		return fmt.Errorf("-tenant and -property are required\n%s", usage)
	}
	if cmd == "rooms" && (*floors < 1 || *floors > 30 || *perFloor != 10) {
		return fmt.Errorf("-floors must be 1 to 30 and -per-floor 10")
	}
	if cmd == "rates" && (*days < 1 || *days > 365) {
		return fmt.Errorf("-days must be 1 to 365")
	}

	e, closePool, err := open(*tenantCode, *propertyCode)
	if err != nil {
		return err
	}
	defer closePool()
	if cmd == "rooms" {
		return seedRooms(e, strings.ToUpper(*propertyCode), *floors, *perFloor, *dryRun)
	}
	return seedRates(e, strings.ToUpper(*propertyCode), strings.ToUpper(strings.TrimSpace(*plan)), *days, *dryRun)
}

// open reads the configuration, refuses production, wires the services and finds the tenant and the property.
func open(tenantCode, propertyCode string) (*env, func(), error) {
	if err := config.LoadDotEnv(".env"); err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if cfg.Env == config.EnvProduction {
		return nil, nil, fmt.Errorf("refusing to seed demo data into a production configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	pool, err := db.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	closeAll := func() {
		pool.Close()
		cancel()
	}

	txm := db.NewTxManager(pool, cfg.DBLockTimeout)
	clk := clock.System{}
	aw := audit.NewWriter(clk)
	authz := iam.NewAuthorizer(txm)
	ten := tenancy.NewService(txm, clk, aw, authz)
	hk := housekeeping.NewService(txm, clk, aw, authz, ten)
	avail := availability.NewService(txm)
	e := &env{
		ten: ten, rm: rooms.NewService(txm, clk, aw, authz, ten, hk, avail), rt: rates.NewService(txm, clk, aw, authz, ten, avail),
		bc: billingconfig.NewService(txm, clk, aw, authz, ten),
	}

	tenant, err := ten.TenantByCode(ctx, tenantCode)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	e.tenant = tenant
	// The seed acts as the tenant's administrator (no user: the audit trail records it as the system).
	e.ctx = auth.WithPrincipal(ctx, auth.Principal{TenantID: tenant.ID, IsTenantAdmin: true})

	props, err := ten.ListProperties(e.ctx, 0, 200)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	for _, p := range props {
		if strings.EqualFold(p.Code, propertyCode) {
			e.propertyID = p.ID
		}
	}
	if e.propertyID == 0 {
		closeAll()
		return nil, nil, fmt.Errorf("tenant %s has no property %q", tenant.Code, propertyCode)
	}
	return e, closeAll, nil
}

func seedRooms(e *env, propertyCode string, floors, perFloor int, dryRun bool) error {
	ctx, propertyID, rm := e.ctx, e.propertyID, e.rm

	// Room types: reuse the ones that exist by code, create the others.
	existing, err := rm.ListRoomTypes(ctx, propertyID, 0, nil, 200)
	if err != nil {
		return err
	}
	typeID := map[string]int64{}
	for _, t := range existing {
		typeID[t.Code] = t.ID
	}
	for i, spec := range types {
		if _, ok := typeID[spec.code]; ok {
			fmt.Printf("room type %s exists\n", spec.code)
			continue
		}
		fmt.Printf("room type %s (%s): to create\n", spec.code, spec.name)
		if dryRun {
			continue
		}
		t, err := rm.CreateRoomType(ctx, propertyID, rooms.RoomTypeInput{
			Code: spec.code, Name: spec.name, Description: spec.description, MaxAdult: spec.maxAdult, MaxChild: spec.maxChild,
			MaxOccupancy: spec.maxAdult + spec.maxChild, BaseOccupancy: 2, SortOrder: int32(i + 1), IsActive: true,
		})
		if err != nil {
			return fmt.Errorf("room type %s: %w", spec.code, err)
		}
		typeID[spec.code] = t.ID
	}

	// The bed types of the property, by code (a property that was created before they existed has them from the migration).
	bedList, err := rm.ListBedTypes(ctx, propertyID, nil)
	if err != nil {
		return err
	}
	bedID := map[string]int64{}
	for _, b := range bedList {
		bedID[b.Code] = b.ID
	}

	// Rooms that exist, by number.
	have := map[string]rooms.Room{}
	var after int64
	for {
		page, err := rm.ListRooms(ctx, propertyID, after, rooms.RoomFilter{}, 200)
		if err != nil {
			return err
		}
		for _, r := range page {
			have[r.RoomNumber] = r
			after = r.ID
		}
		if len(page) < 200 {
			break
		}
	}

	created, skipped, bedsSet := 0, 0, 0
	for f := 1; f <= floors; f++ {
		for n := 1; n <= perFloor; n++ {
			number := strconv.Itoa(f*100 + n)
			var bed *int64
			if id, ok := bedID[bedCodeAt(n)]; ok {
				bed = &id
			}
			if room, ok := have[number]; ok {
				skipped++
				if room.BedTypeID == nil && bed != nil {
					bedsSet++
					if !dryRun {
						if _, err := rm.UpdateRoom(ctx, propertyID, room.ID, rooms.RoomPatch{BedTypeID: bed}); err != nil {
							return fmt.Errorf("room %s: %w", number, err)
						}
					}
				}
				continue
			}
			if dryRun {
				created++
				continue
			}
			_, err := rm.CreateRoom(ctx, propertyID, rooms.CreateRoomInput{
				RoomInput:           rooms.RoomInput{RoomTypeID: typeID[typeCodeAt(n)], RoomNumber: number, Floor: strconv.Itoa(f), BedTypeID: bed, IsActive: true},
				InitialHousekeeping: states[(f*7+n)%len(states)],
			})
			if err != nil {
				return fmt.Errorf("room %s: %w", number, err)
			}
			created++
		}
	}
	verb := "created"
	if dryRun {
		verb = "would create"
	}
	fmt.Printf("%s %d rooms in %s/%s (%d already existed, %d of them got a bed type)\n", verb, created, e.tenant.Code, propertyCode, skipped, bedsSet)
	return nil
}

func seedRates(e *env, propertyCode, planCode string, days int, dryRun bool) error {
	ctx, propertyID := e.ctx, e.propertyID

	// The room types the seed prices: the ones of the property that carry a seed code.
	roomTypes, err := e.rm.ListRoomTypes(ctx, propertyID, 0, nil, 200)
	if err != nil {
		return err
	}
	priced := map[string]int64{}
	for _, t := range roomTypes {
		priced[t.Code] = t.ID
	}

	// The rate plan: reuse it by code, or create it on the ROOM charge code.
	plans, err := e.rt.ListRatePlans(ctx, propertyID, 0, nil, 200)
	if err != nil {
		return err
	}
	var planID int64
	for _, p := range plans {
		if p.Code == planCode {
			planID = p.ID
		}
	}
	if planID == 0 {
		fmt.Printf("rate plan %s: to create\n", planCode)
		if !dryRun {
			typeRoom := "ROOM"
			codes, err := e.bc.ListChargeCodes(ctx, propertyID, 0, billingconfig.ChargeCodeFilter{ChargeType: &typeRoom}, 50)
			if err != nil {
				return err
			}
			var charge int64
			for _, c := range codes {
				if c.Code == "ROOM" && c.IsActive {
					charge = c.ID
				}
			}
			if charge == 0 {
				return fmt.Errorf("the property has no active ROOM charge code to sell rooms through")
			}
			p, err := e.rt.CreateRatePlan(ctx, propertyID, rates.RatePlanInput{
				Code: planCode, Name: "Room Only", Description: "Demo plan: room only", MealPlan: "RO", IsRefundable: true, RoomChargeCodeID: charge, IsActive: true,
			})
			if err != nil {
				return err
			}
			planID = p.ID
		}
	} else {
		fmt.Printf("rate plan %s exists\n", planCode)
	}

	if err := seedFreePlans(e, plans, dryRun); err != nil {
		return err
	}

	day, err := e.ten.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return err
	}
	from := day.BusinessDate
	to := from.AddDays(days)
	written := int64(0)
	for _, spec := range types {
		id, ok := priced[spec.code]
		if !ok {
			fmt.Printf("room type %s is missing: run the rooms command first\n", spec.code)
			continue
		}
		fmt.Printf("room type %s: %s on weekdays, %s on Fridays and Saturdays, %s to %s\n", spec.code, spec.weekday, spec.weekend, from, to.AddDays(-1))
		if dryRun {
			continue
		}
		// the weekday price for every night, then the weekend price over Fridays and Saturdays
		base, err := e.rt.FillRates(e.ctx, propertyID, rates.FillInput{RatePlanID: planID, RoomTypeIDs: []int64{id}, From: from, To: to, Amount: spec.weekday})
		if err != nil {
			return fmt.Errorf("rates of %s: %w", spec.code, err)
		}
		weekend, err := e.rt.FillRates(e.ctx, propertyID, rates.FillInput{RatePlanID: planID, RoomTypeIDs: []int64{id}, From: from, To: to, Weekdays: []string{"FRI", "SAT"}, Amount: spec.weekend})
		if err != nil {
			return fmt.Errorf("weekend rates of %s: %w", spec.code, err)
		}
		written += base.UpdatedNights + weekend.UpdatedNights
	}
	if dryRun {
		fmt.Printf("would price %d nights in %s/%s\n", days, e.tenant.Code, propertyCode)
		return nil
	}
	fmt.Printf("wrote %d prices in %s/%s (plan %s)\n", written, e.tenant.Code, propertyCode, planCode)
	return nil
}

// bedCodeAt is the bed type of the room at a position on a floor: the first two Standard rooms have Twin beds, the other
// two a Queen; Superior rooms a Double; Deluxe rooms and the Suite a King.
func bedCodeAt(position int) string {
	switch typeCodeAt(position) {
	case "STD":
		if position <= 2 {
			return "TWIN"
		}
		return "QUEEN"
	case "SUP":
		return "DOUBLE"
	default:
		return "KING"
	}
}

func typeCodeAt(position int) string {
	for _, t := range types {
		for _, p := range t.positions {
			if p == position {
				return t.code
			}
		}
	}
	return types[0].code
}

// seedFreePlans adds the complimentary and house use plans (COMP, HOUSE) when the property has none with those codes.
// They need no rates: a night on them costs nothing.
func seedFreePlans(e *env, plans []rates.RatePlan, dryRun bool) error {
	have := map[string]bool{}
	for _, p := range plans {
		have[p.Code] = true
	}
	free := []struct{ code, name, kind string }{
		{"COMP", "Complimentary", rates.KindComplimentary},
		{"HOUSE", "House Use", rates.KindHouseUse},
	}
	typeRoom := "ROOM"
	for _, f := range free {
		if have[f.code] {
			fmt.Printf("rate plan %s exists\n", f.code)
			continue
		}
		fmt.Printf("rate plan %s: to create (%s, no rates needed)\n", f.code, f.kind)
		if dryRun {
			continue
		}
		codes, err := e.bc.ListChargeCodes(e.ctx, e.propertyID, 0, billingconfig.ChargeCodeFilter{ChargeType: &typeRoom}, 50)
		if err != nil {
			return err
		}
		var charge int64
		for _, c := range codes {
			if c.Code == "ROOM" && c.IsActive {
				charge = c.ID
			}
		}
		if charge == 0 {
			return fmt.Errorf("the property has no active ROOM charge code to sell rooms through")
		}
		if _, err := e.rt.CreateRatePlan(e.ctx, e.propertyID, rates.RatePlanInput{
			Code: f.code, Name: f.name, Description: "Demo plan: " + f.name, MealPlan: "RO", IsRefundable: true, RoomChargeCodeID: charge, OccupancyKind: f.kind, IsActive: true,
		}); err != nil {
			return err
		}
	}
	return nil
}
