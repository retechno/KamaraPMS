package reports_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reports"
	"kamarapms/internal/rooms/roomstest"
)

func money(n int64) string { return strconv.FormatInt(n, 10) }

func checkOf(t *testing.T, cs []reports.LedgerCheck, key string) reports.LedgerCheck {
	t.Helper()
	for _, c := range cs {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", key, cs)
	return reports.LedgerCheck{}
}

// The story of the books of the deposit release (accounting/depositrelease_test.go): a deposit of 300,000, three nights of 1,000,000, then the rest is paid and the guest leaves. The guest ledger
// and the deposit ledger are asked for each closed day after the later days were posted; every figure is hand-computed.
//
//	EVERY_DAY: the deposit pays the first night at once: the liability is 0 from the first close. Guest ledger account 1210 = n x 1,000,000 - 300,000, account 2310 = 0.
//	ON_CLOSE:  the deposit is held whole until the folio closes: liability 300,000, account 1210 = n x 1,000,000, 2310 = 300,000; at the check-out it is applied.
//
// In both modes the folio balance is n x 1,000,000 - 300,000, so the guest ledger account is the balance plus the deposits held.
func TestGuestAndDepositLedgerOfAPastDateInBothReleaseModes(t *testing.T) {
	for _, mode := range []string{"EVERY_DAY", "ON_CLOSE"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			every := mode == "EVERY_DAY"
			_, err := f.Pool.Exec(context.Background(), `UPDATE properties SET deposit_release_mode = $1 WHERE id = $2`, mode, f.propID)
			must(t, err)
			res := f.book(t, "2026-09-30", "2026-10-03", "PHONE", 1)
			_, err = f.Folios.Deposit(f.admin, f.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
			must(t, err)
			res = f.reload(t, res.ID)
			f.Clock.Set(roomstest.T0.Add(time.Hour)) // a payment taken before the check-in is a deposit
			in, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &f.r101.ID, GuestID: f.guest, AdultCount: 2})
			must(t, err)
			for i := 0; i < 3; i++ {
				f.audit(t)
			}
			// the open day is refused, whatever the mode
			_, err = f.Reports.GuestLedger(f.admin, f.propID, ptr(d("2026-10-03")))
			wantCode(t, err, "DATE_NOT_CLOSED")
			_, err = f.Reports.DepositLedger(f.admin, f.propID, ptr(d("2026-10-03")))
			wantCode(t, err, "DATE_NOT_CLOSED")
			_, err = f.Reports.GuestLedger(f.admin, f.propID, ptr(d("2030-01-01")))
			wantCode(t, err, "DATE_NOT_CLOSED")

			for i, date := range []string{"2026-09-30", "2026-10-01", "2026-10-02"} {
				n := int64(i + 1)
				g, err := f.Reports.GuestLedger(f.admin, f.propID, ptr(d(date)))
				must(t, err)
				liability, account1210 := int64(300000), n*1000000
				if every {
					liability, account1210 = 0, n*1000000-300000
				}
				if g.ReleaseMode != mode || !g.Reconciled || len(g.Rows) != 1 {
					t.Fatalf("%s %s: %+v", mode, date, g)
				}
				row := g.Rows[0]
				if row.ConfirmationNumber != res.ConfirmationNumber || row.Charges != money(n*1000000) || row.Deposits != "300000" || row.Balance != money(n*1000000-300000) || row.DepositHeld != money(liability) || row.Status != "OPEN" {
					t.Fatalf("%s %s row: %+v", mode, date, row)
				}
				if g.Balance != money(n*1000000-300000) || g.DepositHeld != money(liability) {
					t.Fatalf("%s %s totals: %+v", mode, date, g)
				}
				if c := checkOf(t, g.Checks, "ACCOUNT_GUEST_LEDGER"); c.Other != money(account1210) || c.Difference != "0" {
					t.Fatalf("%s %s account 1210: %+v", mode, date, c)
				}
				if c := checkOf(t, g.Checks, "FOLIO_BALANCE"); c.Other != money(n*1000000-300000) || c.Difference != "0" {
					t.Fatalf("%s %s folio balance: %+v", mode, date, c)
				}
				// the oldest unpaid night is posted on the first day and ages one day for each day
				bucket := "0-1"
				if n == 3 {
					bucket = "2-7"
				}
				if row.AgeDays == nil || *row.AgeDays != int(n-1) || row.AgeBucket != bucket {
					t.Fatalf("%s %s age: %+v", mode, date, row)
				}

				dl, err := f.Reports.DepositLedger(f.admin, f.propID, ptr(d(date)))
				must(t, err)
				open, rec, app, closing := int64(0), int64(0), int64(0), int64(300000)
				switch {
				case every:
					closing = 0
					if n == 1 {
						rec, app = 300000, 300000
					}
				case n == 1:
					rec = 300000
				default:
					open = 300000
				}
				if dl.Opening != money(open) || dl.Received != money(rec) || dl.Applied != money(app) || dl.Refunded != "0" || dl.Closing != money(closing) || !dl.Reconciled {
					t.Fatalf("%s %s deposit ledger: %+v", mode, date, dl)
				}
				for _, r := range dl.Rows {
					if !r.Balanced {
						t.Fatalf("%s %s: the identity of %s fails: %+v", mode, date, r.FolioNumber, r)
					}
				}
				if c := checkOf(t, dl.Checks, "ACCOUNT_ADVANCE_DEPOSITS"); c.Report != money(closing) || c.Difference != "0" {
					t.Fatalf("%s %s account 2310: %+v", mode, date, c)
				}
			}

			// the rest is paid and the guest leaves: the day of the check-out
			_, err = f.Folios.PostPayment(f.admin, f.propID, in.Folio.ID, "rest", folios.PaymentInput{Amount: "2700000", PaymentMethod: "CARD"})
			must(t, err)
			cur, err := f.Front.GetStay(f.admin, f.propID, in.Stay.ID)
			must(t, err)
			_, err = f.Front.CheckOut(f.admin, f.propID, in.Stay.ID, frontdesk.CheckOutInput{Version: cur.Stay.Version})
			must(t, err)
			f.audit(t)
			dl, err := f.Reports.DepositLedger(f.admin, f.propID, ptr(d("2026-10-03")))
			must(t, err)
			if every {
				if dl.Opening != "0" || dl.Applied != "0" || dl.Closing != "0" {
					t.Fatalf("EVERY_DAY after the check-out: %+v", dl)
				}
			} else if dl.Opening != "300000" || dl.Applied != "300000" || dl.Closing != "0" || !dl.Reconciled {
				t.Fatalf("ON_CLOSE after the check-out: %+v", dl)
			}
			g, err := f.Reports.GuestLedger(f.admin, f.propID, ptr(d("2026-10-03")))
			must(t, err)
			if g.Balance != "0" || g.DepositHeld != "0" || !g.Reconciled {
				t.Fatalf("%s guest ledger after the check-out: %+v", mode, g)
			}
			// the first day still reads as it did then
			first, err := f.Reports.DepositLedger(f.admin, f.propID, ptr(d("2026-09-30")))
			must(t, err)
			if first.Received != "300000" {
				t.Fatalf("%s: %+v", mode, first)
			}
		})
	}
}

// A balance that disagrees with the journals is shown, never hidden: the report still answers, with a difference and reconciled=false.
func TestGuestLedgerShowsAFixtureThatDoesNotAgree(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1")
	f.audit(t)
	g, err := f.Reports.GuestLedger(f.admin, f.propID, ptr(d("2026-09-30")))
	must(t, err)
	if !g.Reconciled || g.Balance != "1122100" {
		t.Fatalf("1,000,000 room night + 122,100 minibar: %+v", g)
	}
	// a journal line that the folios do not explain
	for _, q := range []string{`ALTER TABLE gl_journal_lines DISABLE TRIGGER USER`,
		`UPDATE gl_journal_lines SET debit = debit + 7000 WHERE id = (SELECT l.id FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE l.property_id = ` + strconv.FormatInt(f.propID, 10) + ` AND a.code = '1210' AND l.debit > 0 LIMIT 1)`,
		`ALTER TABLE gl_journal_lines ENABLE TRIGGER USER`} {
		_, err = f.Pool.Exec(context.Background(), q)
		must(t, err)
	}
	g, err = f.Reports.GuestLedger(f.admin, f.propID, ptr(d("2026-09-30")))
	must(t, err)
	if g.Reconciled || checkOf(t, g.Checks, "ACCOUNT_GUEST_LEDGER").Difference == "0" {
		t.Fatalf("a broken fixture must show a difference: %+v", g.Checks)
	}
}

func TestLedgerAccessAndDefaultDate(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	q := func(string) string { return "garbage" }
	_, err := f.Reports.AsOfFrom(clerk, f.propID, q)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Reports.AsOfFrom(f.admin, f.propID, q)
	wantCode(t, err, "VALIDATION_FAILED")
	other := f.Tenant(t, "XYZ")
	_, err = f.Reports.AsOfFrom(roomstest.Admin(other.ID), f.propID, q)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// no day has been closed yet: there is nothing to report
	_, err = f.Reports.GuestLedger(f.admin, f.propID, nil)
	wantCode(t, err, "BUSINESS_DAY_NOT_FOUND")
	f.audit(t)
	g, err := f.Reports.GuestLedger(f.admin, f.propID, nil)
	must(t, err)
	if g.AsOf.String() != "2026-09-30" {
		t.Fatalf("the default is the last closed day: %s", g.AsOf)
	}
}
