package main

import (
	"bytes"
	"strings"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack"
	"kamarapms/internal/iam"
	"kamarapms/internal/rooms/roomstest"
)

func TestSeedNightAuditRecipients(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	svc := auditpack.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, nil)
	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedNightAuditWith(ctx, svc, p.ID, dry, &out)
		if err != nil {
			t.Fatal(err)
		}
		return n, out.String()
	}

	n, out := run(true)
	if n != len(napDemoRecipients) || !strings.Contains(out, "would create") {
		t.Fatalf("dry run: %d %q", n, out)
	}
	if list, _ := svc.ListRecipients(ctx, p.ID); len(list.Items) != 0 {
		t.Fatalf("a dry run created %d recipients", len(list.Items))
	}
	if n, _ := run(false); n != len(napDemoRecipients) {
		t.Fatalf("created %d", n)
	}
	list, err := svc.ListRecipients(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != len(napDemoRecipients) {
		t.Fatalf("%d recipients", len(list.Items))
	}
	inactive := 0
	for _, r := range list.Items {
		if !strings.HasSuffix(r.Email, ".example") {
			t.Errorf("%s is not on a reserved domain", r.Email)
		}
		if !r.IsActive {
			inactive++
		}
	}
	if inactive != 1 {
		t.Errorf("%d inactive recipients, want 1", inactive)
	}
	if n, out := run(false); n != 0 || !strings.Contains(out, "exists") {
		t.Fatalf("second run: %d %q", n, out)
	}
}
