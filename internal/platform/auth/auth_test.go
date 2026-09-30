package auth

import (
	"context"
	"testing"

	"kamarapms/internal/platform/apperr"
)

func TestRequireAndAdmin(t *testing.T) {
	if _, err := Require(context.Background()); !apperr.IsCode(err, "UNAUTHENTICATED") {
		t.Fatalf("got %v", err)
	}
	user := WithPrincipal(context.Background(), Principal{TenantID: 1, UserID: 2})
	if _, err := RequireTenantAdmin(user); !apperr.IsCode(err, "PERMISSION_DENIED") {
		t.Fatalf("non-admin: %v", err)
	}
	admin := WithPrincipal(context.Background(), Principal{TenantID: 1, UserID: 2, IsTenantAdmin: true})
	if p, err := RequireTenantAdmin(admin); err != nil || p.UserID != 2 {
		t.Fatalf("admin: %v", err)
	}
}

func TestActorID(t *testing.T) {
	if (Principal{TenantID: 1}).ActorID() != nil {
		t.Fatal("no user -> nil actor")
	}
	if id := (Principal{TenantID: 1, UserID: 9}).ActorID(); id == nil || *id != 9 {
		t.Fatal("actor id")
	}
}

func TestCatalogueIsUniqueAndComplete(t *testing.T) {
	seen := map[Permission]bool{}
	for _, p := range Catalogue {
		if seen[p.Code] {
			t.Errorf("duplicate permission %s", p.Code)
		}
		seen[p.Code] = true
		if p.Group == "" || p.Description == "" || p.Milestone == "" {
			t.Errorf("incomplete catalogue entry %+v", p)
		}
	}
	if !ValidPermission(PermFolioPostCharge) || ValidPermission("folio.delete_everything") {
		t.Fatal("ValidPermission")
	}
}
