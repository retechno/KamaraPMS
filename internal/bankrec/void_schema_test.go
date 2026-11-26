package bankrec_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/db"
	"kamarapms/migrations"
)

// The rules the database keeps for a void (migration 00101, docs/architecture/40-card-settlement-void.md 7.2).

func (g *guestFx) dbCode(t *testing.T, sql string, args ...any) string {
	t.Helper()
	err := g.Exec(t, sql, args...)
	if err == nil {
		return ""
	}
	e, ok := apperr.As(db.MapError(err))
	if !ok {
		t.Fatalf("unmapped: %v", err)
	}
	return e.Code
}

// Test 13: a settlement changes once, from POSTED to VOIDED with the columns of the void, and is never deleted.
func TestTheDatabaseLetsASettlementChangeOnlyToVoided(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	for name, c := range map[string]struct{ sql, code string }{
		"another column":                {`UPDATE card_settlements SET net = 1`, "SETTLEMENT_ALREADY_VOIDED"},
		"voided without the void":       {`UPDATE card_settlements SET status = 'VOIDED'`, "INVALID_VALUE"},
		"an unknown status":             {`UPDATE card_settlements SET status = 'GONE'`, "SETTLEMENT_ALREADY_VOIDED"},
		"a delete":                      {`DELETE FROM card_settlements`, "SETTLEMENT_ALREADY_VOIDED"},
		"the release of a live payment": {`UPDATE card_settlement_items SET released_at = now()`, "SETTLEMENT_NOT_VOIDED"},
		"a delete of a payment":         {`DELETE FROM card_settlement_items`, "SETTLEMENT_NOT_VOIDED"},
	} {
		if got := g.dbCode(t, c.sql); got != c.code {
			t.Errorf("%s: %q, want %s", name, got, c.code)
		}
	}
	_, err := g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
	for name, sql := range map[string]string{
		"back to posted":     `UPDATE card_settlements SET status = 'POSTED', voided_at = NULL, void_reason = NULL, void_journal_id = NULL`,
		"a second void":      `UPDATE card_settlements SET void_reason = 'other'`,
		"a delete":           `DELETE FROM card_settlements`,
		"a released item":    `UPDATE card_settlement_items SET released_at = NULL`,
		"a released release": `UPDATE card_settlement_items SET released_at = now()`,
	} {
		if g.Exec(t, sql) == nil {
			t.Errorf("%s is refused", name)
		}
	}
}

// Test 14: a payment line has one live item at a time; once released, a new live item is accepted.
func TestAPaymentLineIsSettledOnceAtATime(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	again := `INSERT INTO card_settlement_items (tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount)
		SELECT tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount FROM card_settlement_items WHERE settlement_id = $1 LIMIT 1`
	if got := g.dbCode(t, again, row.ID); got != "ALREADY_SETTLED" {
		t.Fatalf("two live items: %q", got)
	}
	_, err := g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
	if got := g.dbCode(t, again, row.ID); got != "" {
		t.Fatalf("after the release a new live item is taken: %q", got)
	}
}

// Test 16: the Down of migration 00101 refuses to run once a settlement is voided (the old unique key could not hold it); before, it passes.
func TestTheDownOfTheVoidMigrationRefusesOnceASettlementIsVoided(t *testing.T) {
	b, err := fs.ReadFile(migrations.FS, "00101_card_settlement_void.sql")
	must(t, err)
	down := string(b)[strings.Index(string(b), "-- +goose Down"):]
	start := strings.Index(down, "DO $$")
	guard := down[start : strings.Index(down[start:], "$$;")+start+3]
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	run := func() error {
		ctx := context.Background()
		tx, err := g.Pool.Begin(ctx)
		must(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, guard)
		return err
	}
	if err := run(); err != nil {
		t.Fatalf("no void yet: %v", err)
	}
	_, err = g.void(t, g.admin, g.lastSettlement(t).ID, "wrong")
	must(t, err)
	if err := run(); err == nil || !strings.Contains(err.Error(), "cannot be rolled back") {
		t.Fatalf("a void holds the rollback: %v", err)
	}
}
