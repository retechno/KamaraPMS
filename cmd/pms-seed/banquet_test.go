package main

import (
	"bytes"
	"strings"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/iam"
	"kamarapms/internal/rooms/roomstest"
)

func TestSeedBanquet(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	svc := banquet.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy)

	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedBanquetWith(ctx, svc, e.Billing, p.ID, dry, &out)
		if err != nil {
			t.Fatal(err)
		}
		return n, out.String()
	}

	n, out := run(true)
	if n == 0 || !strings.Contains(out, "would create") {
		t.Fatalf("dry run: %d %q", n, out)
	}
	if rooms, _ := svc.ListRooms(ctx, p.ID, nil); len(rooms) != 0 {
		t.Fatalf("a dry run created %d rooms", len(rooms))
	}

	created, _ := run(false)
	if created != n {
		t.Fatalf("created %d, the dry run said %d", created, n)
	}
	rooms, err := svc.ListRooms(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != len(bqRooms) {
		t.Fatalf("%d rooms, want %d", len(rooms), len(bqRooms))
	}
	for _, r := range rooms {
		if r.Code == "BALL" && (!r.IsCombo || len(r.Parts) != 2 || len(r.Setups) == 0) {
			t.Fatalf("the ballroom is not made of the two halls: %+v", r)
		}
	}
	pk, err := svc.ListPackages(ctx, p.ID, nil)
	if err != nil || len(pk) != 1 || pk[0].Name != "Fullday Meeting" || len(pk[0].Components) != 3 {
		t.Fatalf("package: %+v %v", pk, err)
	}

	again, out := run(false)
	if again != 0 || !strings.Contains(out, "created 0 banquet rows") {
		t.Fatalf("a second run created %d: %s", again, out)
	}
}
