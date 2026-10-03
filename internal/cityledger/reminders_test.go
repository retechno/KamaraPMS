package cityledger_test

import (
	"testing"
	"time"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// advance closes the business day, so the next day is the business date.
func (f *fx) advance(t *testing.T) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

// dueToday makes a company that pays at once (an invoice is due the day it is made) and an invoice to it of amount.
func (f *fx) dueToday(t *testing.T, code, room, amount string) (companies.Company, cityledger.Invoice) {
	t.Helper()
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: code, Name: code + " Ltd", PaymentTermsDays: 0, IsActive: true})
	must(t, err)
	folio, stay := f.stayFolio(t, room, amount)
	res, err := f.transfer(folio, co.ID, "tr-"+room, amount)
	must(t, err)
	f.checkOut(t, stay)
	inv, err := f.invoice(co.ID, "inv-"+room, res.Payment.ID)
	must(t, err)
	return co, inv
}

func TestTheOverdueListShowsWhatIsPastDueWithItsInterest(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	co, inv := f.dueToday(t, "ZERO", "101", "300000")
	f.invoicedOne(t, "102", "100000") // ACME pays in 30 days: not overdue
	od, err := f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 0 {
		t.Fatalf("an invoice due today is not overdue: %+v", od.Companies)
	}
	f.advance(t) // the invoice was due yesterday
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 1 || od.Companies[0].CompanyID != co.ID || len(od.Companies[0].Invoices) != 1 {
		t.Fatalf("overdue: %+v", od.Companies)
	}
	oi := od.Companies[0].Invoices[0]
	if oi.InvoiceID != inv.ID || oi.DaysOverdue != 1 || oi.Bucket != "1-30" || oi.LastReminder != nil || od.Companies[0].NextLevel != 1 {
		t.Fatalf("invoice: %+v", oi)
	}
	eqd(t, "outstanding", oi.Outstanding, "300000")
	eqd(t, "no late fee yet", oi.Interest, "0")
	eqd(t, "total", od.Outstanding, "300000")
	// a credit note takes off what is owed
	_, err = f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	eqd(t, "outstanding after the credit note", od.Companies[0].Invoices[0].Outstanding, "200000")
	// the late fee: 3% a month, no grace: one day late is 200,000 x 3% / 30
	_, err = f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "101", GraceDays: 0})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 400})
	wantCode(t, err, "VALIDATION_FAILED")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	_, err = f.CityLedger.SetLateFee(viewer, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 0})
	wantCode(t, err, "PERMISSION_DENIED")
	lf, err := f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 0})
	must(t, err)
	if lf.MonthlyRate != "3" || lf.GraceDays != 0 {
		t.Fatalf("late fee: %+v", lf)
	}
	od, err = f.CityLedger.OverdueList(viewer, f.propID)
	must(t, err)
	eqd(t, "interest", od.Companies[0].Invoices[0].Interest, "200")
	eqd(t, "interest of the company", od.Companies[0].Interest, "200")
	eqd(t, "interest in all", od.Interest, "200")
	// a day of grace takes it away
	_, err = f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 1})
	must(t, err)
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	eqd(t, "no interest within the grace", od.Interest, "0")
	// once paid it is no longer overdue
	_, err = f.CityLedger.Receive(f.admin, f.propID, co.ID, "r1", cityledger.ReceiptInput{Amount: "200000", PaymentMethod: "CASH", Allocations: []cityledger.AllocationInput{{InvoiceID: inv.ID, Amount: "200000"}}})
	must(t, err)
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 0 {
		t.Fatalf("a paid invoice is not overdue: %+v", od.Companies)
	}
}

func TestAReminderFreezesWhatItListedAndAsksTheNextLevelAfterwards(t *testing.T) {
	f := setup(t)
	co, inv := f.dueToday(t, "ZERO", "101", "300000")
	other, inv2 := f.dueToday(t, "LATE", "102", "50000")
	_, err := f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 0})
	must(t, err)
	f.advance(t)
	today, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	rem := func(company int64, key string, level int, ids ...int64) (cityledger.Reminder, error) {
		return f.CityLedger.CreateReminder(f.admin, f.propID, company, key, cityledger.ReminderInput{Level: level, Note: "please pay", InvoiceIDs: ids})
	}
	_, err = rem(co.ID, "k0", 4)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = rem(co.ID, "k1", 1, inv2.ID) // another company's invoice
	wantCode(t, err, "INVOICE_NOT_OVERDUE")
	r1, err := rem(co.ID, "k2", 1)
	must(t, err)
	if r1.Number != "REM000001" || r1.Level != 1 || r1.Date != today.BusinessDate || len(r1.Items) != 1 || r1.Items[0].InvoiceID != inv.ID || r1.Note != "please pay" {
		t.Fatalf("reminder: %+v", r1)
	}
	eqd(t, "frozen outstanding", r1.TotalOutstanding, "300000")
	eqd(t, "frozen interest", r1.TotalInterest, "300")
	eqd(t, "the item", r1.Items[0].Outstanding, "300000")
	if r1.Items[0].DaysOverdue != 1 {
		t.Fatalf("days: %+v", r1.Items[0])
	}
	// a retry makes nothing more
	again, err := rem(co.ID, "k2", 1)
	must(t, err)
	if again.ID != r1.ID || f.Count(t, `SELECT count(*) FROM city_ledger_reminders`) != 1 {
		t.Fatalf("a replay records nothing: %+v", again)
	}
	_, err = rem(other.ID, "k2", 1)
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
	// what it froze does not move with the late fee; the list shows the last reminder and asks the next level
	_, err = f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "6", GraceDays: 0})
	must(t, err)
	got, err := f.CityLedger.GetReminder(f.admin, f.propID, r1.ID)
	must(t, err)
	eqd(t, "still frozen", got.TotalInterest, "300")
	od, err := f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	var zero *cityledger.OverdueCompany
	for i := range od.Companies {
		if od.Companies[i].CompanyID == co.ID {
			zero = &od.Companies[i]
		}
	}
	if zero == nil || zero.NextLevel != 2 || zero.Invoices[0].LastReminder == nil || zero.Invoices[0].LastReminder.Level != 1 || zero.Invoices[0].LastReminder.Number != "REM000001" {
		t.Fatalf("after a reminder: %+v", zero)
	}
	eqd(t, "interest at the new rate", zero.Invoices[0].Interest, "600")
	list, err := f.CityLedger.Reminders(f.admin, f.propID, co.ID)
	must(t, err)
	if len(list) != 1 || list[0].ID != r1.ID {
		t.Fatalf("list: %+v", list)
	}
	if none, err := f.CityLedger.Reminders(f.admin, f.propID, other.ID); err != nil || len(none) != 0 {
		t.Fatalf("another company has none: %v", err)
	}
	// the next level, for the same invoice; nothing is overdue for a company that paid
	r2, err := rem(co.ID, "k3", 2)
	must(t, err)
	if r2.Level != 2 || r2.Number != "REM000002" {
		t.Fatalf("second: %+v", r2)
	}
	_, err = f.CityLedger.Receive(f.admin, f.propID, other.ID, "p", cityledger.ReceiptInput{Amount: "50000", PaymentMethod: "CASH", Allocations: []cityledger.AllocationInput{{InvoiceID: inv2.ID, Amount: "50000"}}})
	must(t, err)
	_, err = rem(other.ID, "k4", 1)
	wantCode(t, err, "NO_OVERDUE_INVOICES")
	if err := f.Exec(t, `UPDATE city_ledger_reminders SET level = 3`); err == nil {
		t.Error("a reminder was changed")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_reminder_items`); err == nil {
		t.Error("a reminder item was deleted")
	}
	viewer := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	_, err = f.CityLedger.CreateReminder(viewer, f.propID, co.ID, "v", cityledger.ReminderInput{Level: 1})
	wantCode(t, err, "PERMISSION_DENIED")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.reminder_recorded'`); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestTheOverdueListAndTheRemindersAreIsolatedByTenant(t *testing.T) {
	f := setup(t)
	co, _ := f.dueToday(t, "ZERO", "101", "300000")
	f.advance(t)
	r, err := f.CityLedger.CreateReminder(f.admin, f.propID, co.ID, "k", cityledger.ReminderInput{Level: 1})
	must(t, err)
	f.Clock.Set(roomstest.T0) // a property is opened on the date the clock says
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, other.ID)
	_, err = f.CityLedger.OverdueList(otherAdmin, f.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.CityLedger.GetReminder(otherAdmin, f.propID, r.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.CityLedger.GetReminder(f.admin, f.propID, 999999)
	wantCode(t, err, "REMINDER_NOT_FOUND")
}
