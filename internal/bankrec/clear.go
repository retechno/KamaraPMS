package bankrec

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// clearPlan is one clearing to make: a part of a journal line, against a statement line or on its own.
type clearPlan struct {
	line   *int64
	jl     int64
	amount decimal.Decimal
	before bool // the journal line is dated before the statement starts
}

// planClearing turns the request into clearings and checks every one of them against what is left of the journal line
// and of the statement line. An amount has the sign of what is left and never exceeds it.
func (s *Service) planClearing(ctx context.Context, tenantID, propertyID int64, st Statement, ba BankAccount, in ClearInput) ([]clearPlan, error) {
	allocs := in.Allocations
	named := "allocations"
	if len(allocs) == 0 {
		named = "journal_line_ids"
		for _, id := range in.JournalLineIDs {
			allocs = append(allocs, ClearAllocation{StatementLineID: in.StatementLineID, JournalLineID: id})
		}
	}
	if n := len(allocs); n < 1 || n > maxClearBatch {
		return nil, apperr.Invalid("the matching is invalid", fieldErr(named, "INVALID_COUNT", "between 1 and 200 journal lines"))
	}
	q := s.q(ctx)
	usedJournal := map[int64]decimal.Decimal{} // what this request already takes of a journal line
	usedLine := map[int64]decimal.Decimal{}    // and of a statement line
	lineNeed := map[int64]decimal.Decimal{}
	pairs := map[[2]int64]bool{}
	plans := make([]clearPlan, 0, len(allocs))
	for i, a := range allocs {
		at := fmt.Sprintf("%s[%d]", named, i)
		jl, err := q.GetJournalLine(ctx, bankrecdb.GetJournalLineParams{TenantID: tenantID, PropertyID: propertyID, ID: a.JournalLineID})
		switch {
		case isNoRows(err):
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at, "NOT_FOUND", "no such journal line in this property"))
		case err != nil:
			return nil, err
		case jl.AccountID != ba.AccountID:
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at, "NOT_BANK_ACCOUNT", "the journal line is on another account than "+ba.AccountCode))
		case jl.JournalDate.After(st.PeriodTo):
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at, "AFTER_STATEMENT", "the journal line is dated after the end of the statement"))
		}
		left := jl.Amount.Sub(jl.Cleared).Sub(usedJournal[jl.ID])
		if left.IsZero() {
			return nil, apperr.Conflict("ALREADY_CLEARED", "the journal line is cleared already, in this or another statement").WithContext("journal_line_id", jl.ID)
		}
		amount := left
		if a.Amount != nil {
			amount = *a.Amount
		}
		switch {
		case amount.IsZero() || amount.Sign() != left.Sign():
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".amount", "WRONG_SIDE", "an amount on the side of what is left of the journal line ("+left.String()+")"))
		case amount.Abs().GreaterThan(left.Abs()):
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".amount", "EXCEEDS_LINE", "more than the "+left.String()+" left of the journal line"))
		}
		if a.StatementLineID != nil {
			lid := *a.StatementLineID
			if _, ok := lineNeed[lid]; !ok {
				line, err := q.GetStatementLine(ctx, bankrecdb.GetStatementLineParams{TenantID: tenantID, PropertyID: propertyID, StatementID: st.ID, ID: lid})
				if isNoRows(err) {
					return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".statement_line_id", "NOT_FOUND", "no such line in this statement"))
				} else if err != nil {
					return nil, err
				}
				done, err := q.LineCleared(ctx, bankrecdb.LineClearedParams{TenantID: tenantID, PropertyID: propertyID, StatementLineID: &lid})
				if err != nil {
					return nil, err
				}
				lineNeed[lid] = line.Amount.Sub(done)
			}
			need := lineNeed[lid].Sub(usedLine[lid])
			switch {
			case need.IsZero():
				return nil, apperr.Conflict("LINE_ALREADY_MATCHED", "the statement line is matched in full already")
			case amount.Sign() != need.Sign():
				return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".amount", "WRONG_SIDE", "the statement line has "+need.String()+" still to match: its side differs"))
			case amount.Abs().GreaterThan(need.Abs()):
				return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".amount", "EXCEEDS_STATEMENT_LINE", "more than the "+need.String()+" still to match of the statement line"))
			}
			if pairs[[2]int64{jl.ID, lid}] {
				return nil, apperr.Invalid("the matching is invalid", fieldErr(at, "DUPLICATE", "the journal line is listed twice for this statement line"))
			}
			pairs[[2]int64{jl.ID, lid}] = true
			usedLine[lid] = usedLine[lid].Add(amount)
		} else if !amount.Equal(left) {
			return nil, apperr.Invalid("the matching is invalid", fieldErr(at+".amount", "PARTIAL_WITHOUT_LINE", "a journal line cleared without a statement line is cleared in full"))
		}
		usedJournal[jl.ID] = usedJournal[jl.ID].Add(amount)
		plans = append(plans, clearPlan{line: a.StatementLineID, jl: jl.ID, amount: amount, before: jl.JournalDate.Before(st.PeriodFrom)})
	}
	return plans, nil
}

// Clear matches journal lines of the account with statement lines, or clears them on their own (bank.reconcile); see
// ClearInput. A journal line is cleared up to its amount, in one clearing or in parts.
func (s *Service) Clear(ctx context.Context, propertyID, statementID int64, in ClearInput) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	if len(in.JournalLineIDs) == 0 && len(in.Allocations) == 0 {
		return StatementDetail{}, apperr.Invalid("the matching is invalid", fieldErr("journal_line_ids", "INVALID_COUNT", "between 1 and 200 journal lines"))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		st, ba, err := s.openForWork(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		plans, err := s.planClearing(ctx, p.TenantID, propertyID, st, ba, in)
		if err != nil {
			return err
		}
		total := decimal.Zero
		allBefore, noLine := true, false
		for _, pl := range plans {
			total = total.Add(pl.amount)
			allBefore = allBefore && pl.before
			if pl.line == nil {
				noLine = true
			}
		}
		if noLine {
			for _, pl := range plans {
				if pl.line != nil {
					return apperr.Invalid("the matching is invalid", fieldErr("allocations", "MIXED", "journal lines cleared without a statement line are cleared apart from the others"))
				}
			}
			_, perr := q.PreviousStatement(ctx, bankrecdb.PreviousStatementParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, Before: st.PeriodFrom})
			if perr != nil && !isNoRows(perr) {
				return perr
			}
			first := isNoRows(perr)
			if !total.IsZero() && (!first || !allBefore) {
				return apperr.Invalid("the matching is invalid", fieldErr("statement_line_id", "REQUIRED",
					"journal lines are cleared without a statement line only when they offset each other, or when they are from before the first statement"))
			}
		}
		for _, pl := range plans {
			if err := q.InsertClearing(ctx, bankrecdb.InsertClearingParams{
				TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, StatementID: statementID, StatementLineID: pl.line, JournalLineID: pl.jl,
				Amount: pl.amount, Now: s.clock.Now(), ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.cleared", "bank_statement", statementID, auditlabel.BankStatement(ctx, propertyID, statementID), nil,
			map[string]any{"clearings": len(plans), "total": total.String()}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}
