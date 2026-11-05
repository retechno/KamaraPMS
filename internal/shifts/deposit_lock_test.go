package shifts_test

import (
	"context"
	"testing"

	"kamarapms/internal/folios"
)

// A cash deposit for a reservation that has no folio yet, by a cashier with an open shift (as in the demo data).
// The first deposit creates the folio (document number: level 50) and then takes the cashier's shift (level 43):
// the lock order guard refused it ("level 43 requested after level 50").
func (f *fx) reservationWithoutFolio(t *testing.T, conf string) int64 {
	t.Helper()
	var id, guest int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name) VALUES ($1, $2, 'Guest') RETURNING id`, f.tenantID, conf).Scan(&guest))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, guest_id, reservation_date, source, status, confirmed_at) VALUES ($1, $2, $3, $4, '2026-09-30', 'PHONE', 'CONFIRMED', now()) RETURNING id`,
		f.tenantID, f.propID, conf, guest).Scan(&id))
	return id
}

func TestCashDepositWithOpenShiftCreatesTheFolio(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "FRONT", "0")
	res := f.reservationWithoutFolio(t, "DEP1")
	in := folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"}

	first, err := f.Folios.Deposit(f.cashier, f.propID, res, "dep-1", in)
	must(t, err)
	second, err := f.Folios.Deposit(f.cashier, f.propID, res, "dep-2", in) // the folio exists now: 42 then 43 then 50
	must(t, err)
	if first.Payment.FolioID != second.Payment.FolioID {
		t.Fatalf("one folio for both deposits: %+v %+v", first.Payment, second.Payment)
	}
	if got := f.Count(t, `SELECT count(*) FROM payments WHERE shift_id = $1`, sh.ID); got != 2 {
		t.Fatalf("%d payments on the shift, want 2", got)
	}
}

func TestCashDepositWithoutShiftIsRefusedBeforeAnyFolioExists(t *testing.T) {
	f := setup(t)
	res := f.reservationWithoutFolio(t, "DEP2")
	_, err := f.Folios.Deposit(f.cashier, f.propID, res, "dep-1", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	wantCode(t, err, "NO_OPEN_SHIFT")
	if got := f.Count(t, `SELECT count(*) FROM folios WHERE reservation_id = $1`, res); got != 0 {
		t.Fatalf("a refused deposit left %d folios", got)
	}
}

func TestNonCashDepositDoesNotNeedAShift(t *testing.T) {
	f := setup(t)
	res := f.reservationWithoutFolio(t, "DEP3")
	_, err := f.Folios.Deposit(f.cashier, f.propID, res, "dep-1", folios.PaymentInput{Amount: "1000", PaymentMethod: "BANK_TRANSFER", ReferenceNumber: "T1"})
	must(t, err)
}
