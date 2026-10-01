package accounting

import (
	"context"
	"strings"
	"time"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Period is a calendar month of the books. A month can be closed once every business day of it is closed and has its
// journal; a closed month takes no journals. Months close in order and only the latest closed month reopens.
type Period struct {
	Start        civil.Date `json:"period_start"`
	End          civil.Date `json:"period_end"`
	Status       string     `json:"status"`
	Days         int        `json:"days"`
	PostedDays   int        `json:"posted_days"`
	Closable     bool       `json:"closable"`
	Reopenable   bool       `json:"reopenable"`
	ClosedAt     *time.Time `json:"closed_at"`
	ClosedBy     *int64     `json:"closed_by"`
	ReopenedAt   *time.Time `json:"reopened_at"`
	ReopenReason string     `json:"reopen_reason,omitempty"`
}

const maxPeriods = 240

func (s *Service) periods(ctx context.Context, tenantID, propertyID int64, cfg accountingdb.AccountingSetting, today civil.Date) ([]Period, error) {
	q := s.q(ctx)
	rows, err := q.ListPeriods(ctx, accountingdb.ListPeriodsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	closed := map[civil.Date]accountingdb.ListPeriodsRow{}
	var latestClosed civil.Date
	for _, r := range rows {
		closed[r.PeriodStart] = r
		if r.Status == "CLOSED" && r.PeriodStart.After(latestClosed) {
			latestClosed = r.PeriodStart
		}
	}
	var out []Period
	first := periodStart(cfg.StartDate)
	for start, i := first, 0; !start.After(periodStart(today)) && i < maxPeriods; i++ {
		end := periodEnd(start)
		from := start
		if cfg.StartDate.After(from) {
			from = cfg.StartDate
		}
		last := end
		if last.After(today) {
			last = today
		}
		posted, err := q.CountPostedDays(ctx, accountingdb.CountPostedDaysParams{TenantID: tenantID, PropertyID: propertyID, FirstDay: from, LastDay: last})
		if err != nil {
			return nil, err
		}
		per := Period{Start: start, End: end, Status: "OPEN", Days: from.DaysUntil(end) + 1, PostedDays: int(posted)}
		if r, ok := closed[start]; ok {
			per.Status, per.ClosedAt, per.ClosedBy, per.ReopenedAt, per.ReopenReason = r.Status, r.ClosedAt, r.ClosedBy, r.ReopenedAt, deref(r.ReopenReason)
		}
		per.Reopenable = per.Status == "CLOSED" && start.Equal(latestClosed)
		prevClosed := start.Equal(first)
		if !prevClosed {
			prev, ok := closed[periodStart(start.AddDays(-1))]
			prevClosed = ok && prev.Status == "CLOSED"
		}
		per.Closable = per.Status == "OPEN" && prevClosed && end.Before(today) && per.PostedDays == per.Days
		out = append(out, per)
		start = civil.NewDate(start.Year(), start.Month()+1, 1)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // newest first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Periods lists the months from the accounting start date to the current business date, newest first (accounting.view),
// with how many of their days have a journal and whether each can be closed.
func (s *Service) Periods(ctx context.Context, propertyID int64) ([]Period, error) {
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
	return s.periods(ctx, p.TenantID, propertyID, cfg, today)
}

func (s *Service) onePeriod(ctx context.Context, tenantID, propertyID int64, cfg accountingdb.AccountingSetting, today, start civil.Date) (Period, error) {
	list, err := s.periods(ctx, tenantID, propertyID, cfg, today)
	if err != nil {
		return Period{}, err
	}
	for _, per := range list {
		if per.Start.Equal(start) {
			return per, nil
		}
	}
	return Period{}, apperr.NotFound("PERIOD_NOT_FOUND", "the period is not between the accounting start date and today")
}

// ClosePeriod closes a month (accounting.close).
func (s *Service) ClosePeriod(ctx context.Context, propertyID int64, start civil.Date) (Period, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingClose)
	if err != nil {
		return Period{}, err
	}
	var out Period
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		per, err := s.onePeriod(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		if err != nil {
			return err
		}
		switch {
		case per.Status == "CLOSED":
			return apperr.Conflict("PERIOD_ALREADY_CLOSED", "the period is closed already")
		case !per.Closable:
			return apperr.Conflict("PERIOD_NOT_READY", "a period closes when it has ended, the month before it is closed and every business day of it is closed with its journal").
				WithContext("days", per.Days).WithContext("posted_days", per.PostedDays)
		}
		if err := s.q(ctx).ClosePeriod(ctx, accountingdb.ClosePeriodParams{TenantID: p.TenantID, PropertyID: propertyID, PeriodStart: start, Now: ptr(s.clock.Now()), ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.period_closed", "gl_period", propertyID, nil, map[string]any{"period": start.String()})); err != nil {
			return err
		}
		out, err = s.onePeriod(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		return err
	})
	return out, err
}

// ReopenPeriod reopens the latest closed month (accounting.close) with a reason.
func (s *Service) ReopenPeriod(ctx context.Context, propertyID int64, start civil.Date, reason string) (Period, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingClose)
	if err != nil {
		return Period{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Period{}, apperr.Invalid("the reopening is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	var out Period
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		per, err := s.onePeriod(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		if err != nil {
			return err
		}
		switch {
		case per.Status != "CLOSED":
			return apperr.Conflict("PERIOD_NOT_CLOSED", "the period is open")
		case !per.Reopenable:
			return apperr.Conflict("PERIOD_NOT_LATEST", "only the latest closed period can be reopened")
		}
		if err := s.requirePeriodNotInClosedYear(ctx, p.TenantID, propertyID, cfg, start); err != nil {
			return err
		}
		if err := s.q(ctx).ReopenPeriod(ctx, accountingdb.ReopenPeriodParams{TenantID: p.TenantID, PropertyID: propertyID, PeriodStart: start, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.period_reopened", "gl_period", propertyID, nil, map[string]any{"period": start.String(), "reason": reason})); err != nil {
			return err
		}
		out, err = s.onePeriod(ctx, p.TenantID, propertyID, cfg, day.BusinessDate, start)
		return err
	})
	return out, err
}

func ptr[T any](v T) *T { return &v }
