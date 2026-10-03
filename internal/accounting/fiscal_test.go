package accounting_test

import (
	"sync"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// yearHotel is the busy hotel with fiscal years that start in October, so that the year of the first day (30 Sep 2026)
// ends the same day, and with September closed as a period.
func yearHotel(t *testing.T) *hotel {
	t.Helper()
	h, _ := busyHotel(t)
	must(t, h.Exec(t, `UPDATE accounting_settings SET fiscal_year_start_month = 10 WHERE property_id = $1`, h.propID))
	return h
}

func (h *hotel) year(t *testing.T, label string) accounting.FiscalYear {
	t.Helper()
	list, err := h.Accounting.FiscalYears(h.admin, h.propID)
	must(t, err)
	for _, fy := range list {
		if fy.Label == label {
			return fy
		}
	}
	t.Fatalf("no fiscal year %s in %+v", label, list)
	return accounting.FiscalYear{}
}

func TestFiscalYearsAndTheClosingJournal(t *testing.T) {
	h := yearHotel(t)
	sep, oct := d("2026-09-01"), d("2026-10-01")
	list, err := h.Accounting.FiscalYears(h.admin, h.propID)
	must(t, err)
	if len(list) != 2 || list[0].Label != "FY2027" || list[1].Label != "FY2026" || list[1].Start != d("2025-10-01") || list[1].End != d("2026-09-30") {
		t.Fatalf("fiscal years: %+v", list)
	}
	fy := list[1]
	if fy.Months != 1 || fy.ClosedMonths != 0 || fy.Closable || fy.Status != "OPEN" || fy.NetIncome.IsZero() {
		t.Fatalf("FY2026 before its months are closed: %+v", fy)
	}
	net := fy.NetIncome
	is, err := h.Accounting.IncomeStatement(h.admin, h.propID, &sep, ptr(d("2026-09-30")))
	must(t, err)
	eq(t, "the year's result is the income statement's", net, is.NetIncome.String())
	// not ready until the month is closed
	_, err = h.Accounting.CloseFiscalYear(h.admin, h.propID, fy.Start)
	wantCode(t, err, "FISCAL_YEAR_NOT_READY")
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, sep)
	must(t, err)
	if !h.year(t, "FY2026").Closable || h.year(t, "FY2027").Closable {
		t.Fatalf("only the ended year is closable")
	}
	viewer := h.User(t, h.tenantID, h.propID, auth.PermAccountingView)
	_, err = h.Accounting.CloseFiscalYear(viewer, h.propID, fy.Start)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = h.Accounting.CloseFiscalYear(h.admin, h.propID, oct) // this year has not ended
	wantCode(t, err, "FISCAL_YEAR_NOT_READY")
	_, err = h.Accounting.CloseFiscalYear(h.admin, h.propID, d("2024-10-01"))
	wantCode(t, err, "FISCAL_YEAR_NOT_FOUND")

	closed, err := h.Accounting.CloseFiscalYear(h.admin, h.propID, fy.Start)
	must(t, err)
	if closed.Status != "CLOSED" || closed.ClosingID == nil || closed.ClosingNumber == "" || !closed.Reopenable || closed.ClosedAt == nil {
		t.Fatalf("closed: %+v", closed)
	}
	cj, err := h.Accounting.GetJournal(h.admin, h.propID, *closed.ClosingID)
	must(t, err)
	if cj.Type != accounting.JournalClosing || cj.Date != d("2026-09-30") || cj.Description != "Closing of FY2026" {
		t.Fatalf("closing journal: %+v", cj)
	}
	// every revenue and expense account is back at zero, and the result sits in retained earnings
	for _, code := range []string{"4110", "4210", "4230", "5110", "6110", "7110"} {
		if got := h.balance(t, code); !got.IsZero() {
			t.Errorf("%s after the closing: %s", code, got)
		}
	}
	eq(t, "retained earnings", h.balance(t, "3200"), net.Neg().String())
	h.requireBalanced(t)
	// the income statement of the year still shows the year; the balance sheet moved the result to equity
	is, err = h.Accounting.IncomeStatement(h.admin, h.propID, &sep, ptr(d("2026-09-30")))
	must(t, err)
	eq(t, "income statement after the closing", is.NetIncome, net.String())
	asOf := d("2026-09-30")
	bs, err := h.Accounting.BalanceSheet(h.admin, h.propID, &asOf)
	must(t, err)
	for _, l := range bs.Lines {
		if l.Key == "EARNINGS" {
			t.Fatalf("nothing is left to carry: %+v", l)
		}
	}
	if !bs.Difference.IsZero() {
		t.Fatalf("difference %s", bs.Difference)
	}
	var equityAccounts int
	for _, a := range lineOf(t, bs.Lines, "EQUITY").Accounts {
		if a.Code == "3200" {
			equityAccounts++
			eq(t, "retained earnings on the balance sheet", a.Amount, net.String())
		}
	}
	if equityAccounts != 1 {
		t.Fatal("retained earnings is on the balance sheet")
	}
	// the closed year protects its months; the closing journal cannot be reversed by hand
	_, err = h.Accounting.ReopenPeriod(h.admin, h.propID, sep, "late invoice")
	wantCode(t, err, "PERIOD_IN_CLOSED_YEAR")
	_, err = h.Accounting.Reverse(h.admin, h.propID, cj.ID, accounting.ReverseInput{Reason: "x", Approval: h.approval()})
	wantCode(t, err, "JOURNAL_NOT_REVERSIBLE")
	_, err = h.Accounting.CloseFiscalYear(h.admin, h.propID, fy.Start)
	wantCode(t, err, "FISCAL_YEAR_ALREADY_CLOSED")
	if !h.year(t, "FY2027").NetIncome.IsZero() {
		t.Fatal("the next year starts from zero")
	}

	// reopening needs a reason and an approval, and reverses the closing
	_, err = h.Accounting.ReopenFiscalYear(h.admin, h.propID, fy.Start, accounting.ReopenYearInput{Approval: h.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = h.Accounting.ReopenFiscalYear(h.admin, h.propID, fy.Start, accounting.ReopenYearInput{Reason: "audit adjustment"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	reopened, err := h.Accounting.ReopenFiscalYear(h.admin, h.propID, fy.Start, accounting.ReopenYearInput{Reason: "audit adjustment", Approval: h.approval()})
	must(t, err)
	if reopened.Status != "OPEN" || reopened.ReopenReason != "audit adjustment" || reopened.Reopenable {
		t.Fatalf("reopened: %+v", reopened)
	}
	eq(t, "retained earnings after the reopening", h.balance(t, "3200"), "0")
	eq(t, "revenue is back", h.balance(t, "4230"), "-1000000")
	eq(t, "the year's result again", h.year(t, "FY2026").NetIncome, net.String())
	h.requireBalanced(t)
	_, err = h.Accounting.ReopenFiscalYear(h.admin, h.propID, fy.Start, accounting.ReopenYearInput{Reason: "again", Approval: h.approval()})
	wantCode(t, err, "FISCAL_YEAR_NOT_CLOSED")
	// with the year open the month can be reopened and corrected, and the year closes again with a new journal
	_, err = h.Accounting.ReopenPeriod(h.admin, h.propID, sep, "late invoice")
	must(t, err)
	h.manual(t, "2026-09-30", "6110", "1110", "5000", "late")
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, sep)
	must(t, err)
	again, err := h.Accounting.CloseFiscalYear(h.admin, h.propID, fy.Start)
	must(t, err)
	if again.ClosingID == nil || *again.ClosingID == *closed.ClosingID {
		t.Fatalf("a new closing journal: %+v", again)
	}
	eq(t, "retained earnings after the correction", h.balance(t, "3200"), net.Sub(dec("5000")).Neg().String())
	if h.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'CLOSING'`) != 2 || h.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'accounting.fiscal_year_%'`) != 3 {
		t.Fatal("two closings and a reopening are audited")
	}
	h.requireBalanced(t)
}

func TestClosingTheYearOnceUnderConcurrency(t *testing.T) {
	h := yearHotel(t)
	_, err := h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	must(t, err)
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.Accounting.CloseFiscalYear(h.admin, h.propID, d("2025-10-01"))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "FISCAL_YEAR_ALREADY_CLOSED")
		}
	}
	if ok != 1 || h.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'CLOSING'`) != 1 {
		t.Fatalf("one closing wins: %d", ok)
	}
	h.requireBalanced(t)
}

func TestAYearWithoutResultClosesWithoutAJournalAndTheMapIsComplete(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `UPDATE accounting_settings SET fiscal_year_start_month = 10 WHERE property_id = $1`, h.propID))
	h.closeDay(t)
	_, err := h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	must(t, err)
	fy, err := h.Accounting.CloseFiscalYear(h.admin, h.propID, d("2025-10-01"))
	must(t, err)
	if fy.Status != "CLOSED" || fy.ClosingID != nil || h.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'CLOSING'`) != 0 {
		t.Fatalf("a year without result: %+v", fy)
	}
	// the system account the closing needs is part of the map of every property
	m, err := h.Accounting.AccountMap(h.admin, h.propID)
	must(t, err)
	found := false
	for _, e := range m {
		if e.Key == "RETAINED_EARNINGS" && e.AccountCode == "3200" && e.AccountType == accounting.TypeEquity {
			found = true
		}
	}
	if !found || len(m) != 13 {
		t.Fatalf("map: %+v", m)
	}
	other := h.Tenant(t, "XYZ")
	h.Clock.Set(roomstest.T0)
	h.Property(t, other.ID, "SG")
	_, err = h.Accounting.FiscalYears(roomstest.Admin(other.ID), h.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
