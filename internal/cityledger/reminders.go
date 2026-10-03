package cityledger

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/cityledger/cityledgerdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/tenancy"
)

// Overdue invoices, payment reminders and the late fee (design: docs/architecture/10-credit-notes-writeoffs.md). A reminder is a record that a letter of
// level 1, 2 or 3 was sent to a company, with the invoices it listed and what they owed that day frozen; it does not touch the books. The late fee is a
// setting that only decides the interest the overdue list shows; nothing is posted for it.

const maxReminderInvoices = 200

var overdueLabels = []string{"1-30", "31-60", "61-90", "90+"}

func overdueBucket(days int) string {
	switch {
	case days <= 30:
		return overdueLabels[0]
	case days <= 60:
		return overdueLabels[1]
	case days <= 90:
		return overdueLabels[2]
	}
	return overdueLabels[3]
}

// LateFee is the late fee of the property: a percent per month, counted on what is owed, after some days of grace. Off at 0.
type LateFee struct {
	MonthlyRate string `json:"monthly_rate"`
	GraceDays   int    `json:"grace_days"`
}

// LateFeeInput sets the late fee.
type LateFeeInput struct {
	MonthlyRate string `json:"monthly_rate"`
	GraceDays   int    `json:"grace_days"`
}

// ReminderRef says when an invoice was last put in a reminder.
type ReminderRef struct {
	Number string     `json:"number"`
	Date   civil.Date `json:"date"`
	Level  int        `json:"level"`
}

// OverdueInvoice is an invoice that is past its due date and still owes something.
type OverdueInvoice struct {
	InvoiceID     int64        `json:"invoice_id"`
	InvoiceNumber string       `json:"invoice_number"`
	InvoiceDate   civil.Date   `json:"invoice_date"`
	DueDate       civil.Date   `json:"due_date"`
	DaysOverdue   int          `json:"days_overdue"`
	Bucket        string       `json:"bucket"`
	Total         string       `json:"total"`
	Outstanding   string       `json:"outstanding"`
	Interest      string       `json:"interest"`
	LastReminder  *ReminderRef `json:"last_reminder"`
}

// OverdueCompany is the overdue invoices of one company.
type OverdueCompany struct {
	CompanyID   int64            `json:"company_id"`
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	Outstanding string           `json:"outstanding"`
	Interest    string           `json:"interest"`
	Oldest      int              `json:"oldest_days_overdue"`
	NextLevel   int              `json:"next_level"`
	Invoices    []OverdueInvoice `json:"invoices"`
}

// Overdue is what is past its due date as of the business date, by company.
type Overdue struct {
	AsOf        civil.Date       `json:"as_of"`
	Outstanding string           `json:"outstanding"`
	Interest    string           `json:"interest"`
	LateFee     LateFee          `json:"late_fee"`
	Companies   []OverdueCompany `json:"companies"`
}

// ReminderItem is an invoice a reminder listed, with what it owed that day.
type ReminderItem struct {
	InvoiceID     int64      `json:"invoice_id"`
	InvoiceNumber string     `json:"invoice_number"`
	InvoiceDate   civil.Date `json:"invoice_date"`
	DueDate       civil.Date `json:"due_date"`
	Outstanding   string     `json:"outstanding"`
	DaysOverdue   int        `json:"days_overdue"`
	Interest      string     `json:"interest"`
}

// Reminder is a payment reminder sent to a company.
type Reminder struct {
	ID               int64          `json:"id"`
	Number           string         `json:"number"`
	CompanyID        int64          `json:"company_id"`
	Level            int            `json:"level"`
	Date             civil.Date     `json:"reminder_date"`
	Note             string         `json:"note,omitempty"`
	TotalOutstanding string         `json:"total_outstanding"`
	TotalInterest    string         `json:"total_interest"`
	CreatedAt        time.Time      `json:"created_at"`
	CreatedBy        *int64         `json:"created_by"`
	Items            []ReminderItem `json:"items"`
}

// ReminderInput records a reminder. Without invoices it lists every overdue invoice of the company.
type ReminderInput struct {
	Level      int     `json:"level"`
	Note       string  `json:"note"`
	InvoiceIDs []int64 `json:"invoice_ids"`
}

func reminderAudit(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, Old: old, New: updated}
}

func errReminderNotFound() *apperr.Error {
	return apperr.NotFound("REMINDER_NOT_FOUND", "the reminder does not exist in this property")
}

// interestOf is the late fee on what an invoice owes: the monthly rate on the amount for the days past the grace, a month being 30 days.
func interestOf(outstanding decimal.Decimal, daysOverdue int, monthlyRate decimal.Decimal, graceDays int, decimals int32) decimal.Decimal {
	late := daysOverdue - graceDays
	if late <= 0 || !monthlyRate.IsPositive() || !outstanding.IsPositive() {
		return decimal.Zero
	}
	return outstanding.Mul(monthlyRate).Div(decimal.NewFromInt(100)).Mul(decimal.NewFromInt(int64(late))).Div(decimal.NewFromInt(30)).Round(decimals)
}

func (s *Service) lateFee(ctx context.Context, tenantID, propertyID int64) (decimal.Decimal, int, error) {
	r, err := s.q(ctx).GetLateFee(ctx, cityledgerdb.GetLateFeeParams{TenantID: tenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, 0, nil
	}
	if err != nil {
		return decimal.Zero, 0, err
	}
	return r.MonthlyRate, int(r.GraceDays), nil
}

// overdueInvoices is what is past its due date as of a date, with what each still owes and the interest on it, in the order of the companies.
func (s *Service) overdueInvoices(ctx context.Context, tenantID, propertyID int64, companyID *int64, asOf civil.Date, decimals int32) ([]OverdueCompany, error) {
	rate, grace, err := s.lateFee(ctx, tenantID, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListOverdueInvoices(ctx, cityledgerdb.ListOverdueInvoicesParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf, CompanyID: companyID})
	if err != nil {
		return nil, err
	}
	var ids []int64
	type row struct {
		r   cityledgerdb.ListOverdueInvoicesRow
		out decimal.Decimal
	}
	var open []row
	for _, r := range rows {
		out := r.Total.Sub(r.Paid).Sub(r.Adjusted)
		if out.IsPositive() {
			open = append(open, row{r, out})
			ids = append(ids, r.ID)
		}
	}
	last := map[int64]ReminderRef{}
	if len(ids) > 0 {
		refs, err := s.q(ctx).LastRemindersOfInvoices(ctx, cityledgerdb.LastRemindersOfInvoicesParams{TenantID: tenantID, PropertyID: propertyID, InvoiceIds: ids})
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			last[r.InvoiceID] = ReminderRef{Number: r.ReminderNumber, Date: r.ReminderDate, Level: int(r.Level)}
		}
	}
	var out []OverdueCompany
	idx := map[int64]int{}
	totals := map[int64][2]decimal.Decimal{}
	for _, o := range open {
		days := o.r.DueDate.DaysUntil(asOf)
		interest := interestOf(o.out, days, rate, grace, decimals)
		inv := OverdueInvoice{
			InvoiceID: o.r.ID, InvoiceNumber: o.r.InvoiceNumber, InvoiceDate: o.r.InvoiceDate, DueDate: o.r.DueDate, DaysOverdue: days, Bucket: overdueBucket(days),
			Total: o.r.Total.StringFixed(decimals), Outstanding: o.out.StringFixed(decimals), Interest: interest.StringFixed(decimals),
		}
		if ref, ok := last[o.r.ID]; ok {
			c := ref
			inv.LastReminder = &c
		}
		i, ok := idx[o.r.CompanyID]
		if !ok {
			i = len(out)
			idx[o.r.CompanyID] = i
			out = append(out, OverdueCompany{CompanyID: o.r.CompanyID, Code: o.r.CompanyCode, Name: o.r.CompanyName, NextLevel: 1, Invoices: []OverdueInvoice{}})
		}
		out[i].Invoices = append(out[i].Invoices, inv)
		t := totals[o.r.CompanyID]
		totals[o.r.CompanyID] = [2]decimal.Decimal{t[0].Add(o.out), t[1].Add(interest)}
		if days > out[i].Oldest {
			out[i].Oldest = days
		}
		if inv.LastReminder != nil && inv.LastReminder.Level+1 > out[i].NextLevel {
			out[i].NextLevel = min(inv.LastReminder.Level+1, 3)
		}
	}
	for i := range out {
		t := totals[out[i].CompanyID]
		out[i].Outstanding, out[i].Interest = t[0].StringFixed(decimals), t[1].StringFixed(decimals)
	}
	return out, nil
}

// OverdueList is what is past its due date as of the current business date, by company, with the days overdue, what each invoice still owes, the interest
// the late fee of the property comes to and the last reminder that listed it (cityledger.read). A company asks the reminder of the next level.
func (s *Service) OverdueList(ctx context.Context, propertyID int64) (Overdue, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Overdue{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Overdue{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Overdue{}, err
	}
	companies, err := s.overdueInvoices(ctx, p.TenantID, propertyID, nil, day.BusinessDate, decimals)
	if err != nil {
		return Overdue{}, err
	}
	rate, grace, err := s.lateFee(ctx, p.TenantID, propertyID)
	if err != nil {
		return Overdue{}, err
	}
	out := Overdue{AsOf: day.BusinessDate, LateFee: LateFee{MonthlyRate: rate.String(), GraceDays: grace}, Companies: companies}
	if out.Companies == nil {
		out.Companies = []OverdueCompany{}
	}
	total, interest := decimal.Zero, decimal.Zero
	for _, c := range companies {
		total = total.Add(decimal.RequireFromString(c.Outstanding))
		interest = interest.Add(decimal.RequireFromString(c.Interest))
	}
	out.Outstanding, out.Interest = total.StringFixed(decimals), interest.StringFixed(decimals)
	return out, nil
}

// GetLateFee is the late fee of the property (cityledger.read).
func (s *Service) GetLateFee(ctx context.Context, propertyID int64) (LateFee, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return LateFee{}, err
	}
	rate, grace, err := s.lateFee(ctx, p.TenantID, propertyID)
	return LateFee{MonthlyRate: rate.String(), GraceDays: grace}, err
}

// SetLateFee sets the late fee (cityledger.reminder): a percent per month, 0 to 100 with at most 4 decimals, and the days of grace, 0 to 365. It only
// changes the interest the overdue list shows; reminders already recorded keep what they froze.
func (s *Service) SetLateFee(ctx context.Context, propertyID int64, in LateFeeInput) (LateFee, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReminder)
	if err != nil {
		return LateFee{}, err
	}
	var fields []apperr.FieldError
	rate, perr := money.Parse(strings.TrimSpace(in.MonthlyRate))
	switch {
	case perr != nil || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(100)):
		fields = append(fields, field("monthly_rate", "INVALID_RATE", "a percentage from 0 to 100"))
	case !rate.Equal(rate.Round(4)):
		fields = append(fields, field("monthly_rate", "INVALID_RATE", "at most 4 decimals"))
	}
	if in.GraceDays < 0 || in.GraceDays > 365 {
		fields = append(fields, field("grace_days", "OUT_OF_RANGE", "between 0 and 365 days"))
	}
	if len(fields) > 0 {
		return LateFee{}, apperr.Invalid("the late fee is invalid", fields...)
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		oldRate, oldGrace, err := s.lateFee(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if err := s.q(ctx).UpsertLateFee(ctx, cityledgerdb.UpsertLateFeeParams{TenantID: p.TenantID, PropertyID: propertyID, MonthlyRate: rate, GraceDays: int16(in.GraceDays), ActorID: p.ActorID()}); err != nil { //nolint:gosec // G115: 0..365
			return err
		}
		return s.audit.Write(ctx, reminderAudit(p, propertyID, day.BusinessDate, "cityledger.late_fee_set", "city_ledger_late_fee", propertyID,
			map[string]any{"monthly_rate": oldRate.String(), "grace_days": oldGrace}, map[string]any{"monthly_rate": rate.String(), "grace_days": in.GraceDays}))
	})
	if err != nil {
		return LateFee{}, err
	}
	return LateFee{MonthlyRate: rate.String(), GraceDays: in.GraceDays}, nil
}

func (s *Service) listReminders(ctx context.Context, tenantID, propertyID int64, id, companyID *int64, decimals int32) ([]Reminder, error) {
	q := s.q(ctx)
	rows, err := q.ListReminders(ctx, cityledgerdb.ListRemindersParams{TenantID: tenantID, PropertyID: propertyID, ID: id, CompanyID: companyID})
	if err != nil {
		return nil, err
	}
	out := make([]Reminder, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, Reminder{
			ID: r.ID, Number: r.ReminderNumber, CompanyID: r.CompanyID, Level: int(r.Level), Date: r.ReminderDate, Note: deref(r.Note),
			TotalOutstanding: r.TotalOutstanding.StringFixed(decimals), TotalInterest: r.TotalInterest.StringFixed(decimals), CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, Items: []ReminderItem{},
		})
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := q.ListReminderItems(ctx, cityledgerdb.ListReminderItemsParams{TenantID: tenantID, PropertyID: propertyID, ReminderIds: ids})
	if err != nil {
		return nil, err
	}
	by := map[int64][]ReminderItem{}
	for _, it := range items {
		by[it.ReminderID] = append(by[it.ReminderID], ReminderItem{
			InvoiceID: it.InvoiceID, InvoiceNumber: it.InvoiceNumber, InvoiceDate: it.InvoiceDate, DueDate: it.DueDate, Outstanding: it.Outstanding.StringFixed(decimals),
			DaysOverdue: int(it.DaysOverdue), Interest: it.Interest.StringFixed(decimals),
		})
	}
	for i := range out {
		if list, ok := by[out[i].ID]; ok {
			out[i].Items = list
		}
	}
	return out, nil
}

// Reminders lists the reminders sent to a company, newest first (cityledger.read).
func (s *Service) Reminders(ctx context.Context, propertyID, companyID int64) ([]Reminder, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals); err != nil {
		return nil, err
	}
	return s.listReminders(ctx, p.TenantID, propertyID, nil, &companyID, decimals)
}

// GetReminder is one reminder with the invoices it listed (cityledger.read).
func (s *Service) GetReminder(ctx context.Context, propertyID, id int64) (Reminder, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Reminder{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Reminder{}, err
	}
	list, err := s.listReminders(ctx, p.TenantID, propertyID, &id, nil, decimals)
	if err != nil {
		return Reminder{}, err
	}
	if len(list) == 0 {
		return Reminder{}, errReminderNotFound()
	}
	return list[0], nil
}

// CreateReminder records that a reminder of level 1, 2 or 3 was sent to a company (cityledger.reminder), listing the given invoices (every overdue invoice
// of the company by default) with what each owes today and the interest, frozen. 409 `NO_OVERDUE_INVOICES` when nothing is overdue, `INVOICE_NOT_OVERDUE`
// (context.invoice_ids) for an invoice that is not. The company row is locked, so what the reminder freezes is what the account says. The Idempotency-Key
// makes a retry return the first reminder.
func (s *Service) CreateReminder(ctx context.Context, propertyID, companyID int64, key string, in ReminderInput) (Reminder, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReminder)
	if err != nil {
		return Reminder{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Reminder{}, err
	}
	note := strings.TrimSpace(in.Note)
	ids := slices.Clone(in.InvoiceIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var fields []apperr.FieldError
	if in.Level < 1 || in.Level > 3 {
		fields = append(fields, field("level", "OUT_OF_RANGE", "1, 2 or 3"))
	}
	if len([]rune(note)) > maxRemarksLen {
		fields = append(fields, field("note", "TOO_LONG", "at most 500 characters"))
	}
	if len(ids) > maxReminderInvoices || slices.ContainsFunc(ids, func(id int64) bool { return id < 1 }) {
		fields = append(fields, field("invoice_ids", "INVALID_VALUE", "up to 200 invoice ids"))
	}
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Reminder{}, apperr.Invalid("the reminder is invalid", fields...)
	}
	return receiptReplay(key,
		func() (Reminder, bool, error) {
			row, err := s.q(ctx).GetReminderByKey(ctx, cityledgerdb.GetReminderByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
			if errors.Is(err, pgx.ErrNoRows) {
				return Reminder{}, false, nil
			}
			if err != nil {
				return Reminder{}, false, err
			}
			if row.CompanyID != companyID {
				return Reminder{}, false, errKeyReused()
			}
			list, err := s.listReminders(ctx, p.TenantID, propertyID, &row.ID, nil, decimals)
			if err != nil || len(list) == 0 {
				return Reminder{}, false, err
			}
			return list[0], true, nil
		},
		func() (Reminder, error) {
			var out Reminder
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				if err := s.companies.Lock(ctx, propertyID, companyID); err != nil {
					return err
				}
				cos, err := s.overdueInvoices(ctx, p.TenantID, propertyID, &companyID, day.BusinessDate, decimals)
				if err != nil {
					return err
				}
				var listed []OverdueInvoice
				if len(cos) > 0 {
					listed = cos[0].Invoices
				}
				if len(listed) == 0 {
					return apperr.Conflict("NO_OVERDUE_INVOICES", "the company has no overdue invoice")
				}
				if len(ids) > 0 {
					have := map[int64]OverdueInvoice{}
					for _, inv := range listed {
						have[inv.InvoiceID] = inv
					}
					var missing []int64
					listed = nil
					for _, id := range ids {
						inv, ok := have[id]
						if !ok {
							missing = append(missing, id)
							continue
						}
						listed = append(listed, inv)
					}
					if len(missing) > 0 {
						return apperr.Conflict("INVOICE_NOT_OVERDUE", "an invoice is not this company's, is voided, is paid or is not past its due date").WithContext("invoice_ids", missing)
					}
				}
				total, interest := decimal.Zero, decimal.Zero
				for _, inv := range listed {
					total = total.Add(decimal.RequireFromString(inv.Outstanding))
					interest = interest.Add(decimal.RequireFromString(inv.Interest))
				}
				number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqReminder)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				r, err := q.InsertReminder(ctx, cityledgerdb.InsertReminderParams{
					TenantID: p.TenantID, PropertyID: propertyID, ReminderNumber: number, CompanyID: companyID, Level: int16(in.Level), ReminderDate: day.BusinessDate, //nolint:gosec // G115: 1..3
					Note: nullable(note), TotalOutstanding: total, TotalInterest: interest, IdempotencyKey: &key, ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				for _, inv := range listed {
					if err := q.InsertReminderItem(ctx, cityledgerdb.InsertReminderItemParams{
						TenantID: p.TenantID, PropertyID: propertyID, ReminderID: r.ID, CompanyID: companyID, InvoiceID: inv.InvoiceID,
						Outstanding: decimal.RequireFromString(inv.Outstanding), DaysOverdue: int32(inv.DaysOverdue), Interest: decimal.RequireFromString(inv.Interest), //nolint:gosec // G115: days
					}); err != nil {
						return err
					}
				}
				if err := s.audit.Write(ctx, reminderAudit(p, propertyID, day.BusinessDate, "cityledger.reminder_recorded", "city_ledger_reminder", r.ID, nil, map[string]any{
					"number": number, "company_id": companyID, "level": in.Level, "invoices": len(listed), "outstanding": total.String(), "interest": interest.String(),
				})); err != nil {
					return err
				}
				list, err := s.listReminders(ctx, p.TenantID, propertyID, &r.ID, nil, decimals)
				if err != nil {
					return err
				}
				out = list[0]
				return nil
			})
			return out, err
		})
}
