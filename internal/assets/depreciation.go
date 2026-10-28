package assets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The monthly depreciation (docs/architecture/37-fixed-assets.md 4.3): the preview, the run of a month, its reversal, and the two calls the month close makes (PeriodGuard). One run is one journal of
// type ASSET dated the last day of the month, split by category and department; months are run in order; the state of the assets changes only through ledger.go.

// Statuses of a month on the depreciation screen.
const (
	MonthNotRun   = "NOT_RUN"
	MonthPosted   = "POSTED"
	MonthReversed = "REVERSED"
)

// Codes of the blockers of a run (the preview lists them; the run refuses with the first of them, or with DEPRECIATION_BLOCKED).
const (
	BlockRegisterNotStarted = "REGISTER_NOT_STARTED"
	BlockMonthInvalid       = "DEPRECIATION_MONTH_INVALID"
	BlockAlreadyRun         = "DEPRECIATION_ALREADY_RUN"
	BlockPeriodClosed       = "PERIOD_CLOSED"
	BlockNotSetUp           = "ACCOUNTING_NOT_SET_UP"
	BlockBeforeStart        = "BEFORE_ACCOUNTING_START"
	BlockNothingDue         = "NOTHING_DUE"
	BlockDepartment         = "DEPARTMENT_REQUIRED"
	BlockCategoryAccount    = "CATEGORY_ACCOUNT_MISSING"
)

// DepreciationBlocker is a reason a month cannot be run.
type DepreciationBlocker struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Params  map[string]string `json:"params,omitempty"`
}

// DepreciationLine is what a run does to one asset: the months it depreciates (more than one for a catch-up), the amount and the state after it.
type DepreciationLine struct {
	AssetID            int64           `json:"asset_id"`
	AssetNumber        string          `json:"asset_number"`
	AssetName          string          `json:"asset_name"`
	CategoryID         int64           `json:"category_id"`
	CategoryCode       string          `json:"category_code"`
	CategoryName       string          `json:"category_name"`
	ExpenseAccountID   int64           `json:"expense_account_id"`
	ExpenseAccountCode string          `json:"expense_account_code"`
	DepartmentID       *int64          `json:"department_id"`
	DepartmentCode     *string         `json:"department_code"`
	DepartmentName     *string         `json:"department_name"`
	Months             int32           `json:"months"`
	FromMonth          civil.Date      `json:"from_month"`
	CatchUp            bool            `json:"catch_up"`
	Amount             decimal.Decimal `json:"amount"`
	AccumAfter         decimal.Decimal `json:"accum_after"`
	CarryingAfter      decimal.Decimal `json:"carrying_after"`
	MonthsDoneAfter    int32           `json:"months_done_after"`
}

// DepreciationGroup is a line of the journal's debit side: a category and a department.
type DepreciationGroup struct {
	CategoryID             int64           `json:"category_id"`
	CategoryCode           string          `json:"category_code"`
	CategoryName           string          `json:"category_name"`
	ExpenseAccountID       int64           `json:"expense_account_id"`
	ExpenseAccountCode     string          `json:"expense_account_code"`
	ExpenseAccountName     string          `json:"expense_account_name"`
	AccumulatedAccountID   int64           `json:"accumulated_account_id"`
	AccumulatedAccountCode string          `json:"accumulated_account_code"`
	AccumulatedAccountName string          `json:"accumulated_account_name"`
	DepartmentID           *int64          `json:"department_id"`
	DepartmentCode         *string         `json:"department_code"`
	DepartmentName         *string         `json:"department_name"`
	Assets                 int             `json:"assets"`
	Amount                 decimal.Decimal `json:"amount"`
}

// DepreciationPreview is the run of a month computed and not written.
type DepreciationPreview struct {
	Month        civil.Date            `json:"month"`
	JournalDate  civil.Date            `json:"journal_date"`
	Status       string                `json:"status"` // NOT_RUN, or POSTED when the month has a live run
	NextMonth    *civil.Date           `json:"next_month"`
	Lines        []DepreciationLine    `json:"lines"`
	Groups       []DepreciationGroup   `json:"groups"`
	Total        decimal.Decimal       `json:"total"`
	AssetCount   int                   `json:"asset_count"`
	CatchUpCount int                   `json:"catch_up_count"`
	Blockers     []DepreciationBlocker `json:"blockers"`
	CanRun       bool                  `json:"can_run"`
}

// DepreciationRun is a run that was made.
type DepreciationRun struct {
	ID                    int64           `json:"id"`
	Month                 civil.Date      `json:"month"`
	Status                string          `json:"status"`
	JournalID             int64           `json:"journal_id"`
	JournalNumber         string          `json:"journal_number"`
	ReversalJournalID     *int64          `json:"reversal_journal_id"`
	ReversalJournalNumber *string         `json:"reversal_journal_number"`
	Total                 decimal.Decimal `json:"total"`
	AssetCount            int             `json:"asset_count"`
	RunBy                 *int64          `json:"run_by"`
	RunAt                 time.Time       `json:"run_at"`
	ReversedBy            *int64          `json:"reversed_by"`
	ReversedAt            *time.Time      `json:"reversed_at"`
	ReversalReason        *string         `json:"reversal_reason"`
}

// DepreciationRunResult is the answer of a run: the run, its lines and the groups of its journal.
type DepreciationRunResult struct {
	Run      DepreciationRun     `json:"run"`
	Lines    []DepreciationLine  `json:"lines"`
	Groups   []DepreciationGroup `json:"groups"`
	Replayed bool                `json:"replayed"`
}

// DepreciationMonth is a month on the screen "Depreciation".
type DepreciationMonth struct {
	Month                 civil.Date      `json:"month"`
	Status                string          `json:"status"` // NOT_RUN, POSTED or REVERSED
	RunID                 *int64          `json:"run_id"`
	JournalNumber         *string         `json:"journal_number"`
	ReversalJournalNumber *string         `json:"reversal_journal_number"`
	Total                 decimal.Decimal `json:"total"`
	AssetCount            int             `json:"asset_count"`
	RunAt                 *time.Time      `json:"run_at"`
	PeriodClosed          bool            `json:"period_closed"`
	IsNext                bool            `json:"is_next"`
	Runnable              bool            `json:"runnable"` // the next month, and it has ended
	Reversible            bool            `json:"reversible"`
}

// DepreciationOverview is the screen "Depreciation": the months from the start of the register, the newest first.
type DepreciationOverview struct {
	RegisterFrom *civil.Date         `json:"register_from"`
	NextMonth    *civil.Date         `json:"next_month"`
	BusinessDate civil.Date          `json:"business_date"`
	Months       []DepreciationMonth `json:"months"`
}

// ReverseRunInput reverses the latest run.
type ReverseRunInput struct {
	Reason string `json:"reason"`
}

const maxOverviewMonths = 240

// plannedAsset is an asset of a run with its place in the journal.
type plannedAsset struct {
	row         assetsdb.ListDepreciationAssetsRow
	line        DepreciationLine
	accumulated int64
	dept        *int64
}

type deptKey struct{ acct, dept int64 }

// deprPlan is the run of a month computed: the preview, and what the run writes.
type deprPlan struct {
	prev     DepreciationPreview
	planned  []plannedAsset
	live     *assetsdb.FaDeprRun
	everyone []int64 // every depreciable asset of the property, ascending: what the run locks
	decimals int32
}

func blocker(code, msg string, kv ...string) DepreciationBlocker {
	return DepreciationBlocker{Code: code, Message: msg, Params: params(kv...)}
}

func monthLabel(m civil.Date) string { return m.String()[:7] }

// deprState is the state of an asset that the amount is computed from.
func deprState(r assetsdb.ListDepreciationAssetsRow, decimals int32) DeprState {
	s := DeprState{Cost: r.Cost, Residual: r.ResidualValue, Accum: r.AccumDepr, MonthsDone: int(r.DepMonthsDone), Decimals: decimals, Factor: decimal.NewFromInt(2), Method: MethodSL}
	if r.LifeMonths != nil {
		s.LifeMonths = int(*r.LifeMonths)
	}
	if r.Method != nil {
		s.Method = *r.Method
	}
	if r.DbFactor != nil {
		s.Factor = *r.DbFactor
	}
	return s
}

// ParseMonth reads a month as the API writes it: 2026-10 or 2026-10-01.
func ParseMonth(v string) (civil.Date, error) {
	v = strings.TrimSpace(v)
	if len(v) == 7 {
		v += "-01"
	}
	m, err := civil.ParseDate(v)
	if err != nil || !m.Equal(MonthStart(m)) {
		return civil.Date{}, invalid("month", fieldErr("month", "INVALID_FORMAT", "YYYY-MM"))
	}
	return m, nil
}

// planRun computes the run of a month. po is nil when accounting is not set up (the accounts and the departments are not checked then). It writes nothing; the caller has taken the locks the use case needs.
func (s *Service) planRun(ctx context.Context, p auth.Principal, propertyID int64, month, today civil.Date, cfg assetsdb.FaSetting, po *accounting.Poster, decimals int32) (*deprPlan, error) {
	q := s.q(ctx)
	month = MonthStart(month)
	monthEnd := MonthEnd(month)
	pl := &deprPlan{decimals: decimals, prev: DepreciationPreview{Month: month, JournalDate: monthEnd, Status: MonthNotRun, Lines: []DepreciationLine{}, Groups: []DepreciationGroup{}, Blockers: []DepreciationBlocker{}}}
	block := func(b DepreciationBlocker) { pl.prev.Blockers = append(pl.prev.Blockers, b) }
	if po == nil {
		block(blocker(BlockNotSetUp, "accounting is not set up for this property"))
	}
	if cfg.RegisterFrom == nil {
		block(blocker(BlockRegisterNotStarted, "the register has not started: set its first month in the settings"))
		return pl, nil
	}
	registerFrom := *cfg.RegisterFrom
	if live, err := q.GetLiveRunByMonth(ctx, assetsdb.GetLiveRunByMonthParams{TenantID: p.TenantID, PropertyID: propertyID, PeriodMonth: month}); err == nil {
		pl.live = &live
		pl.prev.Status = MonthPosted
		block(blocker(BlockAlreadyRun, "the month has been run already", "month", monthLabel(month), "journal", fmt.Sprint(live.JournalID)))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := q.ListDepreciationAssets(ctx, assetsdb.ListDepreciationAssetsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	pl.everyone = make([]int64, len(rows))
	// the next month: the first one after the last live run in which an asset is due (a month in which nothing is due is not run)
	start := registerFrom
	if last, err := q.LastLiveRun(ctx, assetsdb.LastLiveRunParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil {
		if m := AddMonths(last.PeriodMonth, 1); m.After(start) {
			start = m
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var next *civil.Date
	for i, r := range rows {
		pl.everyone[i] = r.ID
		if !deprState(r, decimals).Due() {
			continue
		}
		if from := firstDue(int(r.DepMonthsDone), r.LastDepMonth, r.FirstDepMonth, registerFrom); next == nil || from.Before(*next) {
			next = &from
		}
	}
	if next != nil && next.Before(start) {
		next = &start
	}
	pl.prev.NextMonth = next
	switch {
	case month.Before(registerFrom):
		block(blocker(BlockMonthInvalid, "the month is before the register starts", "reason", "BEFORE_REGISTER", "month", monthLabel(month), "register_from", monthLabel(registerFrom)))
	case pl.live == nil && next != nil && !month.Equal(*next):
		block(blocker(BlockMonthInvalid, "months are run in order", "reason", "NOT_NEXT", "month", monthLabel(month), "next", monthLabel(*next)))
	}
	if !monthEnd.Before(today) {
		block(blocker(BlockMonthInvalid, "the month has not ended", "reason", "NOT_ENDED", "month", monthLabel(month)))
	}

	moves, err := s.movesByAsset(ctx, p.TenantID, propertyID, pl.everyone)
	if err != nil {
		return nil, err
	}
	// the assets that are due in the month
	for _, r := range rows {
		from := firstDue(int(r.DepMonthsDone), r.LastDepMonth, r.FirstDepMonth, registerFrom)
		if from.After(month) {
			continue
		}
		total, after, applied := Advance(deprState(r, decimals), MonthsBetween(from, month))
		if applied == 0 {
			continue
		}
		if r.ExpenseAccountID == nil || r.AccumulatedAccountID == nil {
			block(blocker(BlockCategoryAccount, "the category of the asset has no depreciation account", "asset", r.Number, "category", r.CategoryCode))
			continue
		}
		pa := plannedAsset{row: r, accumulated: *r.AccumulatedAccountID}
		pa.line = DepreciationLine{AssetID: r.ID, AssetNumber: r.Number, AssetName: r.Name, CategoryID: r.CategoryID, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName,
			ExpenseAccountID: *r.ExpenseAccountID, Months: int32(applied), FromMonth: from, CatchUp: applied > 1, Amount: total, AccumAfter: after.Accum, CarryingAfter: after.Carrying(), //nolint:gosec // G115: at most the life
			MonthsDoneAfter: int32(after.MonthsDone)} //nolint:gosec // G115: at most the life
		pa.dept = departmentAt(r.DepartmentID, moves[r.ID], monthEnd) // the department the asset has at the end of the month, not the one it has today (4.6)
		if pa.dept == nil {
			pa.dept = r.CategoryDepartmentID
		}
		pl.planned = append(pl.planned, pa)
	}

	if po != nil {
		if err := s.checkPlanAccounts(ctx, po, pl, monthEnd, today, block); err != nil {
			return nil, err
		}
	}
	if err := s.groupPlan(ctx, propertyID, po, pl); err != nil {
		return nil, err
	}
	if len(pl.planned) == 0 || !pl.prev.Total.IsPositive() {
		if pl.live == nil && len(pl.prev.Blockers) == 0 {
			block(blocker(BlockNothingDue, "no asset has anything to depreciate in this month", "month", monthLabel(month)))
		}
	}
	pl.prev.CanRun = len(pl.prev.Blockers) == 0
	return pl, nil
}

// checkPlanAccounts asks the accounting what it would refuse: the date, the accounts of the categories and the department of each asset.
func (s *Service) checkPlanAccounts(ctx context.Context, po *accounting.Poster, pl *deprPlan, monthEnd, today civil.Date, block func(DepreciationBlocker)) error {
	if monthEnd.Before(today) {
		if err := po.CheckDate(ctx, monthEnd, "date"); err != nil {
			ae, ok := apperr.As(err)
			if !ok {
				return err
			}
			switch ae.Code {
			case "PERIOD_CLOSED":
				block(blocker(BlockPeriodClosed, "the month is closed in the books: reopen it first", "month", monthLabel(pl.prev.Month)))
			default:
				for _, fe := range ae.Fields {
					if fe.Code == "BEFORE_START" {
						block(blocker(BlockBeforeStart, fe.Message, "month", monthLabel(pl.prev.Month)))
					}
				}
			}
		}
	}
	seen := map[int64]bool{}
	for _, a := range pl.planned {
		for _, id := range []int64{a.line.ExpenseAccountID, a.accumulated} {
			if seen[id] {
				continue
			}
			seen[id] = true
			if err := po.CheckAccount(ctx, id, "account_id"); err != nil {
				ae, ok := apperr.As(err)
				if !ok {
					return err
				}
				code, _, _, _, _ := po.AccountInfo(ctx, id)
				reason := "ACCOUNT_INVALID"
				for _, fe := range ae.Fields {
					reason = "ACCOUNT_" + fe.Code
				}
				block(blocker(reason, "an account of the depreciation cannot be posted to", "account", code, "category", a.line.CategoryCode))
			}
		}
	}
	for i := range pl.planned {
		a := &pl.planned[i]
		d, err := po.ResolveDepartment(ctx, a.line.ExpenseAccountID, a.dept, "department_id")
		if err != nil {
			ae, ok := apperr.As(err)
			if !ok {
				return err
			}
			notAllowed := false
			for _, fe := range ae.Fields {
				notAllowed = notAllowed || fe.Code == accounting.ErrDepartmentNotAllowed
			}
			if notAllowed && a.dept != nil { // the account takes no department: the line has none
				a.dept = nil
				continue
			}
			code := "DEPARTMENT_INVALID"
			for _, fe := range ae.Fields {
				if fe.Code == accounting.ErrDepartmentRequired {
					code = BlockDepartment
				}
			}
			acct, _, _, _, _ := po.AccountInfo(ctx, a.line.ExpenseAccountID)
			block(blocker(code, "the department of the asset does not suit the expense account", "asset", a.line.AssetNumber, "account", acct))
			continue
		}
		a.dept = d
	}
	return nil
}

// groupPlan fills the preview from the planned assets: the lines with the names of their departments and accounts, the groups of the journal and the totals.
func (s *Service) groupPlan(ctx context.Context, propertyID int64, po *accounting.Poster, pl *deprPlan) error {
	var deptIDs []int64
	for _, a := range pl.planned {
		if a.dept != nil {
			deptIDs = append(deptIDs, *a.dept)
		}
	}
	names := map[int64]assetsdb.DepartmentNamesRow{}
	if len(deptIDs) > 0 {
		rows, err := s.q(ctx).DepartmentNames(ctx, assetsdb.DepartmentNamesParams{PropertyID: propertyID, Ids: deptIDs})
		if err != nil {
			return err
		}
		for _, r := range rows {
			names[r.ID] = r
		}
	}
	code := func(id int64) string {
		if po == nil {
			return ""
		}
		c, _, _, _, _ := po.AccountInfo(ctx, id)
		return c
	}
	nameOf := func(id int64) string {
		if po == nil {
			return ""
		}
		_, n, _, _, _ := po.AccountInfo(ctx, id)
		return n
	}
	type gkey struct {
		cat  int64
		dept int64
	}
	groups := map[gkey]*DepreciationGroup{}
	for i := range pl.planned {
		a := &pl.planned[i]
		a.line.DepartmentID = a.dept
		a.line.ExpenseAccountCode = code(a.line.ExpenseAccountID)
		k := gkey{a.line.CategoryID, 0}
		if a.dept != nil {
			k.dept = *a.dept
			if n, ok := names[*a.dept]; ok {
				a.line.DepartmentCode, a.line.DepartmentName = ptr(n.Code), ptr(n.Name)
			}
		}
		g := groups[k]
		if g == nil {
			g = &DepreciationGroup{CategoryID: a.line.CategoryID, CategoryCode: a.line.CategoryCode, CategoryName: a.line.CategoryName, ExpenseAccountID: a.line.ExpenseAccountID,
				ExpenseAccountCode: a.line.ExpenseAccountCode, ExpenseAccountName: nameOf(a.line.ExpenseAccountID), AccumulatedAccountID: a.accumulated, AccumulatedAccountCode: code(a.accumulated),
				AccumulatedAccountName: nameOf(a.accumulated), DepartmentID: a.dept,
				DepartmentCode: a.line.DepartmentCode, DepartmentName: a.line.DepartmentName, Amount: decimal.Zero}
			groups[k] = g
		}
		g.Assets++
		g.Amount = g.Amount.Add(a.line.Amount)
		pl.prev.Total = pl.prev.Total.Add(a.line.Amount)
		pl.prev.Lines = append(pl.prev.Lines, a.line)
		if a.line.CatchUp {
			pl.prev.CatchUpCount++
		}
	}
	pl.prev.AssetCount = len(pl.planned)
	for _, g := range groups {
		pl.prev.Groups = append(pl.prev.Groups, *g)
	}
	sort.Slice(pl.prev.Groups, func(i, j int) bool {
		a, b := pl.prev.Groups[i], pl.prev.Groups[j]
		if a.CategoryCode != b.CategoryCode {
			return a.CategoryCode < b.CategoryCode
		}
		return derefString(a.DepartmentCode) < derefString(b.DepartmentCode)
	})
	return nil
}

// begin takes the poster of the accounting, or answers nil when accounting is not set up (the preview says so as a blocker).
func (s *Service) begin(ctx context.Context, propertyID int64) (*accounting.Poster, error) {
	po, err := s.acct.BeginPosting(ctx, propertyID)
	if err != nil {
		if ae, ok := apperr.As(err); ok && ae.Code == "ACCOUNTING_NOT_SET_UP" {
			return nil, nil //nolint:nilnil // the caller reports it as a blocker
		}
		return nil, err
	}
	po.AllowControl(accounting.KeyFixedAssets) // the accumulated depreciation accounts are the register's own
	return po, nil
}

// PreviewDepreciation computes the run of a month and writes nothing (asset.depreciate).
func (s *Service) PreviewDepreciation(ctx context.Context, propertyID int64, month civil.Date) (DepreciationPreview, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDepreciate)
	if err != nil {
		return DepreciationPreview{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DepreciationPreview{}, err
	}
	var out DepreciationPreview
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		po, err := s.begin(ctx, propertyID)
		if err != nil {
			return err
		}
		cfg, err := s.lockSettings(ctx, p, propertyID, db.ForShare)
		if err != nil {
			return err
		}
		pl, err := s.planRun(ctx, p, propertyID, month, day.BusinessDate, cfg, po, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		out = pl.prev
		return nil
	})
	return out, err
}

// RunDepreciation runs a month (asset.depreciate, an Idempotency-Key): the assets that are due are depreciated, one journal is posted and the run is recorded.
func (s *Service) RunDepreciation(ctx context.Context, propertyID int64, month civil.Date, key string) (DepreciationRunResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDepreciate)
	if err != nil {
		return DepreciationRunResult{}, err
	}
	key = trimmed(key)
	if key == "" {
		return DepreciationRunResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	if len(key) > 64 {
		return DepreciationRunResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_INVALID", "the Idempotency-Key is at most 64 characters")
	}
	month = MonthStart(month)
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DepreciationRunResult{}, err
	}
	var res DepreciationRunResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		po, err := s.begin(ctx, propertyID) // the accounting settings (46) first
		if err != nil {
			return err
		}
		if po == nil {
			return apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
		}
		cfg, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate) // then the register (47): it serialises the runs, the imports and the reversals
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if prev, err := q.GetRunByKey(ctx, assetsdb.GetRunByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			res, err = s.runResult(ctx, p, propertyID, prev.ID)
			res.Replayed = true
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		pl, err := s.planRun(ctx, p, propertyID, month, day, cfg, po, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		if err := refuseBlocked(pl.prev.Blockers); err != nil {
			return err
		}
		if len(pl.everyone) > 0 { // every depreciable asset, ascending, in one call
			if err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, pl.everyone); err != nil {
				return err
			}
		}
		return s.postRun(ctx, p, propertyID, day, po, pl, key, &res)
	})
	return res, err
}

// refuseBlocked answers the first blocker with its own code (the month, the date), else DEPRECIATION_BLOCKED with all of them.
func refuseBlocked(bs []DepreciationBlocker) error {
	if len(bs) == 0 {
		return nil
	}
	first := bs[0]
	ctxOf := func(e *apperr.Error) *apperr.Error {
		for k, v := range first.Params {
			e = e.WithContext(k, v)
		}
		return e.WithContext("blockers", bs)
	}
	switch first.Code {
	case BlockAlreadyRun:
		return ctxOf(apperr.Conflict("DEPRECIATION_ALREADY_RUN", "the month has been run already"))
	case BlockMonthInvalid:
		return ctxOf(apperr.Conflict("DEPRECIATION_MONTH_INVALID", "the month cannot be run now: months are run in order, once they have ended"))
	case BlockPeriodClosed:
		return ctxOf(apperr.Conflict("PERIOD_CLOSED", "the month is closed in the books"))
	case BlockRegisterNotStarted:
		return ctxOf(apperr.Conflict("REGISTER_NOT_STARTED", "the register has not started: set its first month in the settings"))
	}
	return apperr.Conflict("DEPRECIATION_BLOCKED", "the month cannot be run: resolve the findings first").WithContext("blockers", bs)
}

// postRun writes the run: the journal, the run and its lines, and the ledger rows that change the assets. The caller has taken the locks.
func (s *Service) postRun(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, po *accounting.Poster, pl *deprPlan, key string, res *DepreciationRunResult) error {
	q := s.q(ctx)
	month, monthEnd := pl.prev.Month, pl.prev.JournalDate
	// the journal: a debit for each expense account and department, a credit for each accumulated account
	debits := map[deptKey]decimal.Decimal{}
	credits := map[int64]decimal.Decimal{}
	for _, a := range pl.planned {
		k := deptKey{acct: a.line.ExpenseAccountID}
		if a.dept != nil {
			k.dept = *a.dept
		}
		debits[k] = debits[k].Add(a.line.Amount)
		credits[a.accumulated] = credits[a.accumulated].Add(a.line.Amount)
	}
	codeOf := func(id int64) string { c, _, _, _, _ := po.AccountInfo(ctx, id); return c }
	dk := make([]deptKey, 0, len(debits))
	for k := range debits {
		dk = append(dk, k)
	}
	sort.Slice(dk, func(i, j int) bool {
		if ci, cj := codeOf(dk[i].acct), codeOf(dk[j].acct); ci != cj {
			return ci < cj
		}
		return dk[i].dept < dk[j].dept
	})
	ck := make([]int64, 0, len(credits))
	for id := range credits {
		ck = append(ck, id)
	}
	sort.Slice(ck, func(i, j int) bool { return codeOf(ck[i]) < codeOf(ck[j]) })
	label := monthLabel(month)
	var lines []accounting.SystemLine
	for _, k := range dk {
		if !debits[k].IsPositive() {
			continue
		}
		l := accounting.SystemLine{AccountID: k.acct, Debit: debits[k], Description: "Depreciation " + label, SourceType: journalSourceAsset, SourceRef: label}
		if k.dept != 0 {
			l.DepartmentID = ptr(k.dept)
		}
		lines = append(lines, l)
	}
	for _, id := range ck {
		if credits[id].IsPositive() {
			lines = append(lines, accounting.SystemLine{AccountID: id, Credit: credits[id], Description: "Accumulated depreciation " + label, SourceType: journalSourceAsset, SourceRef: label})
		}
	}
	jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: monthEnd, Description: "Depreciation of fixed assets " + label, Reference: "FADEP " + label, Lines: lines})
	if err != nil {
		return err
	}
	runID, err := q.InsertRun(ctx, assetsdb.InsertRunParams{TenantID: p.TenantID, PropertyID: propertyID, PeriodMonth: month, JournalID: jid, Total: pl.prev.Total, AssetCount: int32(len(pl.planned)), //nolint:gosec // G115: bounded
		IdempotencyKey: &key, ActorID: p.ActorID()})
	if err != nil {
		return err
	}
	for _, a := range pl.planned {
		lineID, err := q.InsertRunLine(ctx, assetsdb.InsertRunLineParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: runID, AssetID: a.line.AssetID, CategoryID: a.line.CategoryID,
			ExpenseAccountID: a.line.ExpenseAccountID, DepartmentID: a.dept, Months: a.line.Months, FromMonth: a.line.FromMonth, Amount: a.line.Amount, AccumAfter: a.line.AccumAfter,
			CarryingAfter: a.line.CarryingAfter, MonthsDoneAfter: a.line.MonthsDoneAfter})
		if err != nil {
			return err
		}
		if err := s.depreciateAsset(ctx, p, propertyID, a.line.AssetID, a.line.CategoryID, lineID, month, a.line.Amount, a.line.Months, jid); err != nil {
			return err
		}
	}
	if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.depreciation_run", "fa_depr_run", runID, label, nil,
		map[string]any{"month": label, "total": pl.prev.Total, "assets": len(pl.planned), "journal": jnum})); err != nil {
		return err
	}
	out, err := s.runResult(ctx, p, propertyID, runID)
	*res = out
	return err
}

func runOf(r assetsdb.GetRunRow) DepreciationRun {
	return DepreciationRun{ID: r.ID, Month: r.PeriodMonth, Status: r.Status, JournalID: r.JournalID, JournalNumber: r.JournalNumber, ReversalJournalID: r.ReversalJournalID,
		ReversalJournalNumber: r.ReversalJournalNumber, Total: r.Total, AssetCount: int(r.AssetCount), RunBy: r.RunBy, RunAt: r.RunAt, ReversedBy: r.ReversedBy, ReversedAt: r.ReversedAt,
		ReversalReason: r.ReversalReason}
}

// runResult reads a run back with its lines and its groups.
func (s *Service) runResult(ctx context.Context, p auth.Principal, propertyID, runID int64) (DepreciationRunResult, error) {
	q := s.q(ctx)
	r, err := q.GetRun(ctx, assetsdb.GetRunParams{TenantID: p.TenantID, PropertyID: propertyID, ID: runID})
	if err != nil {
		return DepreciationRunResult{}, orNotFound(err, notFound("DEPRECIATION_RUN_NOT_FOUND", "depreciation run"))
	}
	rows, err := q.ListRunLines(ctx, assetsdb.ListRunLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: runID})
	if err != nil {
		return DepreciationRunResult{}, err
	}
	out := DepreciationRunResult{Run: runOf(r), Lines: []DepreciationLine{}, Groups: []DepreciationGroup{}}
	type gkey struct{ cat, dept int64 }
	groups := map[gkey]*DepreciationGroup{}
	for _, l := range rows {
		out.Lines = append(out.Lines, DepreciationLine{AssetID: l.AssetID, AssetNumber: l.AssetNumber, AssetName: l.AssetName, CategoryID: l.CategoryID, CategoryCode: l.CategoryCode,
			CategoryName: l.CategoryName, ExpenseAccountID: l.ExpenseAccountID, ExpenseAccountCode: l.ExpenseAccountCode, DepartmentID: l.DepartmentID, DepartmentCode: l.DepartmentCode,
			DepartmentName: l.DepartmentName, Months: l.Months, FromMonth: l.FromMonth, CatchUp: l.Months > 1, Amount: l.Amount, AccumAfter: l.AccumAfter, CarryingAfter: l.CarryingAfter,
			MonthsDoneAfter: l.MonthsDoneAfter})
		k := gkey{cat: l.CategoryID}
		if l.DepartmentID != nil {
			k.dept = *l.DepartmentID
		}
		g := groups[k]
		if g == nil {
			g = &DepreciationGroup{CategoryID: l.CategoryID, CategoryCode: l.CategoryCode, CategoryName: l.CategoryName, ExpenseAccountID: l.ExpenseAccountID, ExpenseAccountCode: l.ExpenseAccountCode,
				DepartmentID: l.DepartmentID, DepartmentCode: l.DepartmentCode, DepartmentName: l.DepartmentName, Amount: decimal.Zero}
			groups[k] = g
		}
		g.Assets++
		g.Amount = g.Amount.Add(l.Amount)
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.CategoryCode != b.CategoryCode {
			return a.CategoryCode < b.CategoryCode
		}
		return derefString(a.DepartmentCode) < derefString(b.DepartmentCode)
	})
	return out, nil
}

// ReverseDepreciation reverses the latest run while its book month is open (asset.depreciate, a reason).
func (s *Service) ReverseDepreciation(ctx context.Context, propertyID int64, month civil.Date, in ReverseRunInput) (DepreciationRunResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDepreciate)
	if err != nil {
		return DepreciationRunResult{}, err
	}
	in.Reason = trimmed(in.Reason)
	if n := len([]rune(in.Reason)); n < 1 || n > 500 {
		return DepreciationRunResult{}, invalid("reversal", fieldErr("reason", "INVALID_VALUE", "a reason of 1 to 500 characters"))
	}
	month = MonthStart(month)
	var res DepreciationRunResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		po, err := s.begin(ctx, propertyID)
		if err != nil {
			return err
		}
		if po == nil {
			return apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
		}
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate); err != nil {
			return err
		}
		q := s.q(ctx)
		run, err := q.GetLiveRunByMonth(ctx, assetsdb.GetLiveRunByMonthParams{TenantID: p.TenantID, PropertyID: propertyID, PeriodMonth: month})
		if err != nil {
			return orNotFound(err, notFound("DEPRECIATION_RUN_NOT_FOUND", "depreciation run"))
		}
		if last, err := q.LastLiveRun(ctx, assetsdb.LastLiveRunParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
			return err
		} else if last.ID != run.ID {
			return apperr.Conflict("DEPRECIATION_MONTH_INVALID", "only the latest run can be reversed").WithContext("reason", "NOT_LATEST").WithContext("month", monthLabel(month)).
				WithContext("latest", monthLabel(last.PeriodMonth))
		}
		if err := s.reverseRuns(ctx, p, propertyID, day, po, []assetsdb.FaDeprRun{run}, in.Reason); err != nil {
			return err
		}
		res, err = s.runResult(ctx, p, propertyID, run.ID)
		return err
	})
	return res, err
}

// reverseRuns reverses runs, the latest first: every asset of them is locked in one call (ascending) before the first journal number is taken, then each run is reversed by the mirror journal of the
// Poster, its ledger rows are negated and the run is marked. It is refused when an asset of a run was disposed of or changed category after the month (F9).
func (s *Service) reverseRuns(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, po *accounting.Poster, runs []assetsdb.FaDeprRun, reason string) error {
	q := s.q(ctx)
	type runLines struct {
		run   assetsdb.FaDeprRun
		lines []assetsdb.ListRunLinesRow
	}
	all := make([]runLines, 0, len(runs))
	seen := map[int64]bool{}
	var ids []int64
	for _, r := range runs {
		lines, err := q.ListRunLines(ctx, assetsdb.ListRunLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: r.ID})
		if err != nil {
			return err
		}
		all = append(all, runLines{r, lines})
		for _, l := range lines {
			if !seen[l.AssetID] {
				seen[l.AssetID] = true
				ids = append(ids, l.AssetID)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > 0 {
		if err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, ids); err != nil {
			return err
		}
	}
	for _, rl := range all {
		numbers, err := q.LaterAssetEvents(ctx, assetsdb.LaterAssetEventsParams{TenantID: p.TenantID, PropertyID: propertyID, AssetIds: idsOfLines(rl.lines), MonthEnd: MonthEnd(rl.run.PeriodMonth)})
		if err != nil {
			return err
		}
		if len(numbers) > 0 {
			return apperr.Conflict("DEPRECIATION_NOT_REVERSIBLE", "an asset of the run was disposed of or changed category after the month: the run cannot be reversed").
				WithContext("month", monthLabel(rl.run.PeriodMonth)).WithContext("assets", numbers)
		}
	}
	for _, rl := range all { // the latest first
		monthEnd := MonthEnd(rl.run.PeriodMonth)
		rid, err := po.Reverse(ctx, rl.run.JournalID, monthEnd, reason, p.UserID)
		if err != nil {
			return err
		}
		for _, l := range rl.lines {
			if err := s.reverseDepreciation(ctx, p, propertyID, l.ID, rid); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		if err := q.MarkRunReversed(ctx, assetsdb.MarkRunReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: rl.run.ID, ReversalJournalID: &rid, ActorID: p.ActorID(), ReversedAt: &now, Reason: &reason}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.depreciation_reversed", "fa_depr_run", rl.run.ID, monthLabel(rl.run.PeriodMonth), nil,
			map[string]any{"month": monthLabel(rl.run.PeriodMonth), "total": rl.run.Total, "assets": rl.run.AssetCount, "reason": reason})); err != nil {
			return err
		}
	}
	return nil
}

func idsOfLines(lines []assetsdb.ListRunLinesRow) []int64 {
	out := make([]int64, len(lines))
	for i, l := range lines {
		out[i] = l.AssetID
	}
	return out
}

// ListDepreciation is the screen "Depreciation": the months from the start of the register to the current month, the newest first, with the run of each and the month to run next.
func (s *Service) ListDepreciation(ctx context.Context, propertyID int64) (DepreciationOverview, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDepreciate)
	if err != nil {
		return DepreciationOverview{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DepreciationOverview{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return DepreciationOverview{}, err
	}
	q := s.q(ctx)
	cfg, err := q.EnsureSettings(ctx, assetsdb.EnsureSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()})
	if err != nil {
		return DepreciationOverview{}, err
	}
	out := DepreciationOverview{RegisterFrom: cfg.RegisterFrom, BusinessDate: day.BusinessDate, Months: []DepreciationMonth{}}
	if cfg.RegisterFrom == nil {
		return out, nil
	}
	runs, err := q.ListRuns(ctx, assetsdb.ListRunsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	states, err := q.ListPeriodStates(ctx, assetsdb.ListPeriodStatesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	closed := map[civil.Date]bool{}
	for _, st := range states {
		closed[st.PeriodStart] = st.Status == "CLOSED"
	}
	byMonth := map[civil.Date]assetsdb.ListRunsRow{} // the live run of a month, else its latest reversed one (the rows come newest first)
	var latestLive civil.Date
	for _, r := range runs {
		if cur, ok := byMonth[r.PeriodMonth]; !ok || (cur.Status != MonthPosted && r.Status == MonthPosted) {
			byMonth[r.PeriodMonth] = r
		}
		if r.Status == MonthPosted && r.PeriodMonth.After(latestLive) {
			latestLive = r.PeriodMonth
		}
	}
	rows, err := q.ListDepreciationAssets(ctx, assetsdb.ListDepreciationAssetsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	start := *cfg.RegisterFrom
	if !latestLive.IsZero() {
		if m := AddMonths(latestLive, 1); m.After(start) {
			start = m
		}
	}
	for _, r := range rows {
		if !deprState(r, prop.CurrencyDecimals).Due() {
			continue
		}
		if from := firstDue(int(r.DepMonthsDone), r.LastDepMonth, r.FirstDepMonth, *cfg.RegisterFrom); out.NextMonth == nil || from.Before(*out.NextMonth) {
			out.NextMonth = ptr(from)
		}
	}
	if out.NextMonth != nil && out.NextMonth.Before(start) {
		out.NextMonth = ptr(start)
	}
	for m, n := MonthStart(day.BusinessDate), 0; !m.Before(*cfg.RegisterFrom) && n < maxOverviewMonths; m, n = AddMonths(m, -1), n+1 {
		dm := DepreciationMonth{Month: m, Status: MonthNotRun, Total: decimal.Zero, PeriodClosed: closed[m]}
		if r, ok := byMonth[m]; ok {
			dm.Status, dm.RunID, dm.JournalNumber, dm.ReversalJournalNumber, dm.Total, dm.AssetCount, dm.RunAt = r.Status, ptr(r.ID), ptr(r.JournalNumber), r.ReversalJournalNumber, r.Total, int(r.AssetCount), ptr(r.RunAt)
		}
		dm.IsNext = out.NextMonth != nil && m.Equal(*out.NextMonth)
		dm.Runnable = dm.IsNext && MonthEnd(m).Before(day.BusinessDate)
		dm.Reversible = dm.Status == MonthPosted && m.Equal(latestLive) && !dm.PeriodClosed
		out.Months = append(out.Months, dm)
	}
	return out, nil
}

// CloseBlockers is what the month close asks (accounting.PeriodGuard): DEPRECIATION_NOT_RUN when assets are due in the month and the month has no live run, and no later month has one either (the
// months after a gap are caught up in the next run). Before the register starts, or without a register, nothing stops the close.
func (s *Service) CloseBlockers(ctx context.Context, tenantID, propertyID int64, month civil.Date) ([]accounting.PeriodBlocker, error) {
	q := s.q(ctx)
	cfg, err := q.GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cfg.RegisterFrom == nil || month.Before(*cfg.RegisterFrom) {
		return nil, nil
	}
	if n, err := q.LiveRunFrom(ctx, assetsdb.LiveRunFromParams{TenantID: tenantID, PropertyID: propertyID, PeriodMonth: month}); err != nil || n > 0 {
		return nil, err
	}
	due, err := q.CountAssetsDueIn(ctx, assetsdb.CountAssetsDueInParams{TenantID: tenantID, PropertyID: propertyID, RegisterFrom: *cfg.RegisterFrom, Month: month})
	if err != nil || due == 0 {
		return nil, err
	}
	return []accounting.PeriodBlocker{{Code: "DEPRECIATION_NOT_RUN", Message: "the depreciation of the fixed assets has not been run for the month", Params: params("month", monthLabel(month), "assets", fmt.Sprint(due))}}, nil
}

// OnReopen is called inside ReopenPeriod, after the month is open again: the runs of the month and of the months after it are reversed, the latest first (accounting.PeriodGuard). The user who reopens
// the month needs accounting.close, not asset.depreciate: the reopen is the authority. It is refused (DEPRECIATION_NOT_REVERSIBLE) when an asset of those runs was disposed of or changed category
// after the month end.
func (s *Service) OnReopen(ctx context.Context, tenantID, propertyID int64, month civil.Date, reason string) error {
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if cfg, err := s.q(ctx).GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID}); errors.Is(err, pgx.ErrNoRows) || (err == nil && cfg.RegisterFrom == nil) {
		return nil
	} else if err != nil {
		return err
	}
	po, err := s.begin(ctx, propertyID) // the accounting settings are locked by the caller already; this is the same level
	if err != nil || po == nil {
		return err
	}
	if _, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate); err != nil {
		return err
	}
	runs, err := s.q(ctx).ListLiveRunsFrom(ctx, assetsdb.ListLiveRunsFromParams{TenantID: tenantID, PropertyID: propertyID, PeriodMonth: month})
	if err != nil || len(runs) == 0 {
		return err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return err
	}
	why := "Month reopened: " + reason
	if r := []rune(why); len(r) > 500 {
		why = string(r[:500])
	}
	return s.reverseRuns(ctx, p, propertyID, day.BusinessDate, po, runs, why)
}

// ---------------------------------------------------------------------------------------------------------------------
// The schedule of an asset

// Kinds of a row of the schedule.
const (
	ScheduleOpening   = "OPENING"
	SchedulePosted    = "POSTED"
	ScheduleProjected = "PROJECTED"
)

// ScheduleRow is a month of the commercial schedule of an asset: posted in a run (and which), brought by the opening balances, or still to come.
type ScheduleRow struct {
	Month         civil.Date      `json:"month"`
	Kind          string          `json:"kind"`
	Months        int             `json:"months"` // the months in the row: more than 1 for the opening balance
	Amount        decimal.Decimal `json:"amount"`
	AccumAfter    decimal.Decimal `json:"accum_after"`
	CarryingAfter decimal.Decimal `json:"carrying_after"`
	MonthsDone    int             `json:"months_done"`
	RunMonth      *civil.Date     `json:"run_month"`      // the month of the run that booked it (later than Month for a catch-up)
	JournalNumber *string         `json:"journal_number"` // the journal of that run
	// the fiscal book (4.9) for the same month: what the rates give, computed, never posted; absent for an asset without a fiscal group
	FiscalAmount     *decimal.Decimal `json:"fiscal_amount"`
	FiscalAccumAfter *decimal.Decimal `json:"fiscal_accum_after"`
}

// AssetSchedule is the commercial depreciation schedule of an asset.
type AssetSchedule struct {
	AssetID     int64           `json:"asset_id"`
	Number      string          `json:"number"`
	Name        string          `json:"name"`
	Depreciable bool            `json:"depreciable"`
	Cost        decimal.Decimal `json:"cost"`
	Residual    decimal.Decimal `json:"residual_value"`
	LifeMonths  *int32          `json:"life_months"`
	Method      *string         `json:"method"`
	// the fiscal group and method of the asset when it has them
	FiscalGroup  *string       `json:"fiscal_group"`
	FiscalMethod *string       `json:"fiscal_method"`
	Rows         []ScheduleRow `json:"rows"`
}

// AssetSchedule lists the months of an asset: what the opening balances brought, what the runs posted and what is projected to the end of the life (asset.read).
func (s *Service) AssetSchedule(ctx context.Context, propertyID, assetID int64) (AssetSchedule, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return AssetSchedule{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return AssetSchedule{}, err
	}
	q := s.q(ctx)
	a, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID})
	if err != nil {
		return AssetSchedule{}, orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
	}
	out := AssetSchedule{AssetID: a.ID, Number: a.Number, Name: a.Name, Depreciable: a.LifeMonths != nil, Cost: a.Cost, Residual: a.ResidualValue, LifeMonths: a.LifeMonths, Method: a.Method, Rows: []ScheduleRow{}}
	if a.LifeMonths == nil {
		return out, nil
	}
	cfg, err := q.GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	base := DeprState{Cost: a.Cost, Residual: a.ResidualValue, LifeMonths: int(*a.LifeMonths), Method: derefString(a.Method), Factor: decimal.NewFromInt(2), Decimals: prop.CurrencyDecimals}
	if a.DbFactor != nil {
		base.Factor = *a.DbFactor
	}
	if first, err := q.LedgerOpenEntry(ctx, assetsdb.LedgerOpenEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID}); err == nil && first.Kind == LedgerOpen && first.MonthsDelta > 0 && first.BookMonth != nil {
		out.Rows = append(out.Rows, ScheduleRow{Month: *first.BookMonth, Kind: ScheduleOpening, Months: int(first.MonthsDelta), Amount: first.AccumDelta, AccumAfter: first.AccumDelta,
			CarryingAfter: a.Cost.Sub(first.AccumDelta), MonthsDone: int(first.MonthsDelta)})
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	lines, err := q.ListDeprLinesOfAsset(ctx, assetsdb.ListDeprLinesOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return out, err
	}
	for _, l := range lines {
		run, journal := l.PeriodMonth, l.JournalNumber
		st := base
		st.Accum, st.MonthsDone = l.AccumAfter.Sub(l.Amount), int(l.MonthsDoneAfter-l.Months)
		var rows []ScheduleRow
		sum := decimal.Zero
		for k := 0; k < int(l.Months); k++ {
			var m decimal.Decimal
			m, st = Step(st)
			sum = sum.Add(m)
			rows = append(rows, ScheduleRow{Month: AddMonths(l.FromMonth, k), Kind: SchedulePosted, Months: 1, Amount: m, AccumAfter: st.Accum, CarryingAfter: st.Carrying(), MonthsDone: st.MonthsDone,
				RunMonth: &run, JournalNumber: &journal})
		}
		if !sum.Equal(l.Amount) { // the terms changed since: one row for the line as it was booked
			rows = []ScheduleRow{{Month: l.PeriodMonth, Kind: SchedulePosted, Months: int(l.Months), Amount: l.Amount, AccumAfter: l.AccumAfter, CarryingAfter: l.CarryingAfter, MonthsDone: int(l.MonthsDoneAfter),
				RunMonth: &run, JournalNumber: &journal}}
		}
		out.Rows = append(out.Rows, rows...)
	}
	if a.Status == statusActive && cfg.RegisterFrom != nil {
		st := base
		st.Accum, st.MonthsDone = a.AccumDepr, int(a.DepMonthsDone)
		from := firstDue(int(a.DepMonthsDone), a.LastDepMonth, a.FirstDepMonth, *cfg.RegisterFrom)
		for _, m := range Project(st, from) {
			out.Rows = append(out.Rows, ScheduleRow{Month: m.Month, Kind: ScheduleProjected, Months: 1, Amount: m.Amount, AccumAfter: m.Accum, CarryingAfter: m.Carrying, MonthsDone: m.MonthsDone})
		}
	}
	if err := s.fillFiscalSchedule(ctx, p.TenantID, propertyID, assetID, &out); err != nil {
		return out, err
	}
	return out, nil
}
