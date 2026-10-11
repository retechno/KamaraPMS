package accounting

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// FiscalYear is a year of the books, from the first day of the fiscal year start month. It closes when all its months
// are closed: the closing journal then moves the result of the year from the revenue and expense accounts to retained
// earnings. Only the latest closed year reopens (the closing journal is reversed).
type FiscalYear struct {
	Start         civil.Date      `json:"year_start"`
	End           civil.Date      `json:"year_end"`
	Label         string          `json:"label"`
	Status        string          `json:"status"`
	Months        int             `json:"months"`
	ClosedMonths  int             `json:"closed_months"`
	NetIncome     decimal.Decimal `json:"net_income"`
	Closable      bool            `json:"closable"`
	Reopenable    bool            `json:"reopenable"`
	ClosingID     *int64          `json:"closing_journal_id"`
	ClosingNumber string          `json:"closing_journal_number,omitempty"`
	ClosedAt      *time.Time      `json:"closed_at"`
	ReopenedAt    *time.Time      `json:"reopened_at"`
	ReopenReason  string          `json:"reopen_reason,omitempty"`
}

// ReopenYearInput reopens a closed fiscal year.
type ReopenYearInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// fiscalYearOf is the fiscal year (first and last day) that contains a date when years start in startMonth.
func fiscalYearOf(d civil.Date, startMonth int) (start, end civil.Date) {
	y := d.Year()
	if int(d.Month()) < startMonth {
		y--
	}
	start = civil.NewDate(y, time.Month(startMonth), 1)
	end = civil.NewDate(y, time.Month(startMonth)+12, 1).AddDays(-1)
	return start, end
}

func fiscalLabel(end civil.Date) string { return fmt.Sprintf("FY%d", end.Year()) }

// monthsBetween counts the calendar months from the month of a to the month of b, both included.
func monthsBetween(a, b civil.Date) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month()) + 1
}

func (s *Service) fiscalYears(ctx context.Context, tenantID, propertyID int64, cfg accountingdb.AccountingSetting, today civil.Date) ([]FiscalYear, error) {
	q := s.q(ctx)
	rows, err := q.ListFiscalYears(ctx, accountingdb.ListFiscalYearsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	rec := map[civil.Date]accountingdb.ListFiscalYearsRow{}
	var latestClosed civil.Date
	for _, r := range rows {
		rec[r.YearStart] = r
		if r.Status == "CLOSED" && r.YearStart.After(latestClosed) {
			latestClosed = r.YearStart
		}
	}
	startMonth := int(cfg.FiscalYearStartMonth)
	first, _ := fiscalYearOf(cfg.StartDate, startMonth)
	var out []FiscalYear
	prevClosed := true
	for start := first; !start.After(today) && len(out) < 50; {
		_, end := fiscalYearOf(start, startMonth)
		from := periodStart(start)
		if cfg.StartDate.After(from) {
			from = periodStart(cfg.StartDate)
		}
		closedMonths, err := q.CountClosedPeriodsBetween(ctx, accountingdb.CountClosedPeriodsBetweenParams{TenantID: tenantID, PropertyID: propertyID, FirstDay: from, LastDay: end})
		if err != nil {
			return nil, err
		}
		months := monthsBetween(from, end)
		fy := FiscalYear{Start: start, End: end, Label: fiscalLabel(end), Status: "OPEN", Months: months, ClosedMonths: int(closedMonths)}
		if r, ok := rec[start]; ok {
			fy.Status, fy.ClosingID, fy.ClosingNumber, fy.ClosedAt, fy.ReopenedAt, fy.ReopenReason = r.Status, r.ClosingJournalID, deref(r.ClosingNumber), r.ClosedAt, r.ReopenedAt, deref(r.ReopenReason)
		}
		lo := start
		if cfg.StartDate.After(lo) {
			lo = cfg.StartDate
		}
		bal, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{
			TenantID: tenantID, PropertyID: propertyID, FromDate: &lo, ToDate: end, AccountTypes: []string{TypeRevenue, TypeExpense}, ExcludeClosing: true,
		})
		if err != nil {
			return nil, err
		}
		for _, b := range bal {
			fy.NetIncome = fy.NetIncome.Sub(b.Balance)
		}
		fy.Reopenable = fy.Status == "CLOSED" && start.Equal(latestClosed)
		fy.Closable = fy.Status == "OPEN" && prevClosed && end.Before(today) && fy.ClosedMonths == fy.Months
		prevClosed = fy.Status == "CLOSED"
		out = append(out, fy)
		start = end.AddDays(1)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // newest first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// FiscalYears lists the fiscal years from the accounting start date to the current business date, newest first, with
// the result of each (accounting.view).
func (s *Service) FiscalYears(ctx context.Context, propertyID int64) ([]FiscalYear, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return nil, err
	}
	cfg, err := s.q(ctx).GetSettings(ctx, accountingdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, settingsErr(err)
	}
	today, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	return s.fiscalYears(ctx, p.TenantID, propertyID, cfg, today)
}

func (s *Service) oneYear(ctx context.Context, tenantID, propertyID int64, cfg accountingdb.AccountingSetting, today, start civil.Date) (FiscalYear, error) {
	list, err := s.fiscalYears(ctx, tenantID, propertyID, cfg, today)
	if err != nil {
		return FiscalYear{}, err
	}
	for _, fy := range list {
		if fy.Start.Equal(start) {
			return fy, nil
		}
	}
	return FiscalYear{}, apperr.NotFound("FISCAL_YEAR_NOT_FOUND", "the fiscal year is not between the accounting start date and today")
}

// CloseFiscalYear closes a fiscal year (accounting.close): the closing journal, dated on the last day of the year,
// moves every revenue and expense balance of the year to the retained earnings account.
func (s *Service) CloseFiscalYear(ctx context.Context, propertyID int64, start civil.Date) (FiscalYear, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingClose)
	if err != nil {
		return FiscalYear{}, err
	}
	var out FiscalYear
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		fy, err := s.oneYear(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		if err != nil {
			return err
		}
		switch {
		case fy.Status == "CLOSED":
			return apperr.Conflict("FISCAL_YEAR_ALREADY_CLOSED", "the fiscal year is closed already")
		case !fy.Closable:
			return apperr.Conflict("FISCAL_YEAR_NOT_READY", "a fiscal year closes when it has ended, the year before it is closed and every month of it is closed").
				WithContext("months", fy.Months).WithContext("closed_months", fy.ClosedMonths)
		}
		res, err := s.newResolver(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		re, ok := res.byKey[KeyRetainedEarnings]
		if !ok {
			return apperr.Conflict("ACCOUNT_MAP_INCOMPLETE", "the system account RETAINED_EARNINGS is not mapped")
		}
		q := s.q(ctx)
		lo := fy.Start
		if cfg.StartDate.After(lo) {
			lo = cfg.StartDate
		}
		bal, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{
			TenantID: p.TenantID, PropertyID: propertyID, FromDate: &lo, ToDate: fy.End, AccountTypes: []string{TypeRevenue, TypeExpense}, ExcludeClosing: true,
		})
		if err != nil {
			return err
		}
		var journalID *int64
		now := s.clock.Now()
		if len(bal) > 0 {
			sort.Slice(bal, func(i, j int) bool { return bal[i].Code < bal[j].Code })
			number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqJournal)
			if err != nil {
				return err
			}
			id, err := q.InsertJournal(ctx, accountingdb.InsertJournalParams{
				TenantID: p.TenantID, PropertyID: propertyID, JournalNumber: number, JournalType: JournalClosing, JournalDate: fy.End,
				Description: "Closing of " + fy.Label, PostedAt: now, ActorID: p.ActorID(), IsClosing: true,
			})
			if err != nil {
				return err
			}
			net := decimal.Zero
			lines := 0
			for _, b := range bal {
				net = net.Add(b.Balance)
				lines++
				line := accountingdb.InsertJournalLineParams{
					TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: int32(lines), AccountID: b.ID, Description: nullable("Closed to retained earnings"),
				}
				if b.Balance.IsPositive() { // a debit balance is closed with a credit
					line.Debit, line.Credit = decimal.Zero, b.Balance
				} else {
					line.Debit, line.Credit = b.Balance.Neg(), decimal.Zero
				}
				if err := q.InsertJournalLine(ctx, line); err != nil {
					return err
				}
			}
			if !net.IsZero() { // the result: a profit (credit balances) is a credit to retained earnings
				lines++
				line := accountingdb.InsertJournalLineParams{
					TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: int32(lines), AccountID: re.AccountID, Debit: decimal.Zero, Credit: decimal.Zero,
					Description: nullable("Result of " + fy.Label),
				}
				if net.IsNegative() {
					line.Credit = net.Neg()
				} else {
					line.Debit = net
				}
				if err := q.InsertJournalLine(ctx, line); err != nil {
					return err
				}
			}
			journalID = &id
		}
		if err := q.UpsertFiscalYearClosed(ctx, accountingdb.UpsertFiscalYearClosedParams{
			TenantID: p.TenantID, PropertyID: propertyID, YearStart: fy.Start, YearEnd: fy.End, ClosingJournalID: journalID, Now: ptr(now), ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.fiscal_year_closed", "gl_fiscal_year", propertyID, fy.Label, nil,
			map[string]any{"year": fy.Label, "start": fy.Start.String(), "net_income": fy.NetIncome.String(), "journal_id": journalID})); err != nil {
			return err
		}
		out, err = s.oneYear(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		return err
	})
	return out, err
}

// ReopenFiscalYear reopens the latest closed fiscal year (accounting.close plus an approval, with a reason): the closing
// journal is reversed, so the income statement accounts carry the result of the year again.
func (s *Service) ReopenFiscalYear(ctx context.Context, propertyID int64, start civil.Date, in ReopenYearInput) (FiscalYear, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingClose)
	if err != nil {
		return FiscalYear{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return FiscalYear{}, apperr.Invalid("the reopening is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return FiscalYear{}, err
	}
	var out FiscalYear
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		fy, err := s.oneYear(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		if err != nil {
			return err
		}
		switch {
		case fy.Status != "CLOSED":
			return apperr.Conflict("FISCAL_YEAR_NOT_CLOSED", "the fiscal year is open")
		case !fy.Reopenable:
			return apperr.Conflict("FISCAL_YEAR_NOT_LATEST", "only the latest closed fiscal year can be reopened")
		}
		q := s.q(ctx)
		by := approval.UserID()
		var reversalID *int64
		if fy.ClosingID != nil {
			orig, err := s.loadJournal(ctx, p.TenantID, propertyID, *fy.ClosingID)
			if err != nil {
				return err
			}
			number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqJournal)
			if err != nil {
				return err
			}
			id, err := q.InsertJournal(ctx, accountingdb.InsertJournalParams{
				TenantID: p.TenantID, PropertyID: propertyID, JournalNumber: number, JournalType: JournalReversal, JournalDate: fy.End,
				Description: "Reopening of " + fy.Label + ": reversal of " + orig.Number, ReversesJournalID: &orig.ID, Reason: &reason, PostedAt: s.clock.Now(),
				ActorID: p.ActorID(), ApprovedBy: &by, IsClosing: true,
			})
			if err != nil {
				return err
			}
			for _, l := range orig.Lines {
				if err := q.InsertJournalLine(ctx, accountingdb.InsertJournalLineParams{
					TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: l.LineNo, AccountID: l.AccountID, Debit: l.Credit, Credit: l.Debit, Description: nullable(l.Description), DepartmentID: l.DepartmentID,
				}); err != nil {
					return err
				}
			}
			reversalID = &id
		}
		if err := q.MarkFiscalYearReopened(ctx, accountingdb.MarkFiscalYearReopenedParams{
			TenantID: p.TenantID, PropertyID: propertyID, YearStart: fy.Start, ReversalJournalID: reversalID, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by,
		}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.fiscal_year_reopened", "gl_fiscal_year", propertyID, fy.Label, nil,
			map[string]any{"year": fy.Label, "start": fy.Start.String(), "reason": reason, "approved_by": by})); err != nil {
			return err
		}
		out, err = s.oneYear(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		return err
	})
	return out, err
}

// requirePeriodNotInClosedYear keeps a month of a closed fiscal year closed: reopening it would change a year whose
// result has been closed to retained earnings.
func (s *Service) requirePeriodNotInClosedYear(ctx context.Context, tenantID, propertyID int64, cfg accountingdb.AccountingSetting, month civil.Date) error {
	start, _ := fiscalYearOf(month, int(cfg.FiscalYearStartMonth))
	rows, err := s.q(ctx).ListFiscalYears(ctx, accountingdb.ListFiscalYearsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.YearStart.Equal(start) && r.Status == "CLOSED" {
			return apperr.Conflict("PERIOD_IN_CLOSED_YEAR", "the month belongs to a closed fiscal year: reopen the year first").WithContext("fiscal_year", fiscalLabel(r.YearEnd))
		}
	}
	return nil
}
