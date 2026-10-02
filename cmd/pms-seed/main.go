// Command pms-seed fills a DEVELOPMENT database with demo data, through the same services the application uses (so
// every row has its audit entry and the rooms their housekeeping state).
//
//	pms-seed rooms -tenant DEMO -property BALI [-floors 10] [-per-floor 10] [-dry-run]
//
// rooms: creates the room types Standard, Superior, Deluxe and Suite when the property lacks them and rooms numbered
// floor*100+n (101..110, 201..210, ...), spread over the types, with a mix of housekeeping states. It is safe to run
// again: a room number or a room type code that exists is left alone. It refuses to run against a production
// configuration. It reads PMS_DATABASE_URL from the environment or ./.env.
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
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
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
  pms-seed rooms -tenant CODE -property CODE [-floors N] [-per-floor N] [-dry-run]`

// A room type the seed creates when the property has no type with that code.
type typeSpec struct {
	code, name, description string
	maxAdult, maxChild      int32
	// share is how many rooms of each ten (by position on the floor) are of this type.
	positions []int
}

var types = []typeSpec{
	{code: "STD", name: "Standard", description: "Standard room", maxAdult: 2, maxChild: 1, positions: []int{1, 2, 3, 4}},
	{code: "SUP", name: "Superior", description: "Superior room", maxAdult: 2, maxChild: 1, positions: []int{5, 6, 7}},
	{code: "DLX", name: "Deluxe", description: "Deluxe room", maxAdult: 2, maxChild: 2, positions: []int{8, 9}},
	{code: "STE", name: "Suite", description: "Suite", maxAdult: 3, maxChild: 2, positions: []int{10}},
}

// A housekeeping state for a new room, by its number: most are ready, some need work, so the boards look alive.
var states = []housekeeping.Status{housekeeping.Clean, housekeeping.Clean, housekeeping.Inspected, housekeeping.Dirty, housekeeping.Clean, housekeeping.Cleaning, housekeeping.Inspected}

func run(args []string) error {
	if len(args) == 0 || args[0] != "rooms" {
		return fmt.Errorf("%s", usage)
	}
	fs := flag.NewFlagSet("rooms", flag.ContinueOnError)
	tenantCode := fs.String("tenant", "", "tenant code")
	propertyCode := fs.String("property", "", "property code")
	floors := fs.Int("floors", 10, "number of floors (1 to 30)")
	perFloor := fs.Int("per-floor", 10, "rooms per floor (10, so the types spread evenly)")
	dryRun := fs.Bool("dry-run", false, "show what would be created, create nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *tenantCode == "" || *propertyCode == "" {
		return fmt.Errorf("-tenant and -property are required\n%s", usage)
	}
	if *floors < 1 || *floors > 30 || *perFloor != 10 {
		return fmt.Errorf("-floors must be 1 to 30 and -per-floor 10")
	}

	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Env == config.EnvProduction {
		return fmt.Errorf("refusing to seed demo data into a production configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	txm := db.NewTxManager(pool, cfg.DBLockTimeout)
	clk := clock.System{}
	aw := audit.NewWriter(clk)
	authz := iam.NewAuthorizer(txm)
	ten := tenancy.NewService(txm, clk, aw, authz)
	hk := housekeeping.NewService(txm, clk, aw, authz, ten)
	rm := rooms.NewService(txm, clk, aw, authz, ten, hk, availability.NewService(txm))

	tenant, err := ten.TenantByCode(ctx, *tenantCode)
	if err != nil {
		return err
	}
	// The seed acts as the tenant's administrator (no user: the audit trail records it as the system).
	ctx = auth.WithPrincipal(ctx, auth.Principal{TenantID: tenant.ID, IsTenantAdmin: true})

	props, err := ten.ListProperties(ctx, 0, 200)
	if err != nil {
		return err
	}
	var propertyID int64
	for _, p := range props {
		if strings.EqualFold(p.Code, *propertyCode) {
			propertyID = p.ID
		}
	}
	if propertyID == 0 {
		return fmt.Errorf("tenant %s has no property %q", tenant.Code, *propertyCode)
	}

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
		if *dryRun {
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

	// Rooms that exist, by number.
	have := map[string]bool{}
	var after int64
	for {
		page, err := rm.ListRooms(ctx, propertyID, after, rooms.RoomFilter{}, 200)
		if err != nil {
			return err
		}
		for _, r := range page {
			have[r.RoomNumber] = true
			after = r.ID
		}
		if len(page) < 200 {
			break
		}
	}

	created, skipped := 0, 0
	for f := 1; f <= *floors; f++ {
		for n := 1; n <= *perFloor; n++ {
			number := strconv.Itoa(f*100 + n)
			if have[number] {
				skipped++
				continue
			}
			code := typeCodeAt(n)
			if *dryRun {
				created++
				continue
			}
			_, err := rm.CreateRoom(ctx, propertyID, rooms.CreateRoomInput{
				RoomInput:           rooms.RoomInput{RoomTypeID: typeID[code], RoomNumber: number, Floor: strconv.Itoa(f), IsActive: true},
				InitialHousekeeping: states[(f*7+n)%len(states)],
			})
			if err != nil {
				return fmt.Errorf("room %s: %w", number, err)
			}
			created++
		}
	}
	verb := "created"
	if *dryRun {
		verb = "would create"
	}
	fmt.Printf("%s %d rooms in %s/%s (%d already existed)\n", verb, created, tenant.Code, strings.ToUpper(*propertyCode), skipped)
	return nil
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
