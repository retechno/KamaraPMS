package bankrec

import (
	"context"
	"fmt"
	"strings"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

const maxClearBatch = 200

// openForWork locks the bank account of a statement (level 48) and returns the statement, which must still be open.
func (s *Service) openForWork(ctx context.Context, tenantID, propertyID, statementID int64) (Statement, BankAccount, error) {
	st, err := s.statementRow(ctx, tenantID, propertyID, statementID)
	if err != nil {
		return Statement{}, BankAccount{}, err
	}
	if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{st.BankAccountID}); err != nil {
		return Statement{}, BankAccount{}, errBankAccountNotFound()
	}
	st, err = s.statementRow(ctx, tenantID, propertyID, statementID)
	if err != nil {
		return Statement{}, BankAccount{}, err
	}
	if st.Status != StatementOpen {
		return Statement{}, BankAccount{}, apperr.Conflict("STATEMENT_RECONCILED", "the statement is reconciled: reopen it to change what is matched")
	}
	ba, err := s.loadBankAccount(ctx, tenantID, propertyID, st.BankAccountID)
	return st, ba, err
}

// Unclear undoes one clearing of a statement that is not reconciled (bank.reconcile).
func (s *Service) Unclear(ctx context.Context, propertyID, statementID, clearingID int64) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if _, _, err := s.openForWork(ctx, p.TenantID, propertyID, statementID); err != nil {
			return err
		}
		q := s.q(ctx)
		c, err := q.GetClearing(ctx, bankrecdb.GetClearingParams{TenantID: p.TenantID, PropertyID: propertyID, ID: clearingID})
		if isNoRows(err) || (err == nil && c.StatementID != statementID) {
			return apperr.NotFound("CLEARING_NOT_FOUND", "the matching does not exist in this statement")
		}
		if err != nil {
			return err
		}
		if err := q.DeleteClearing(ctx, bankrecdb.DeleteClearingParams{TenantID: p.TenantID, PropertyID: propertyID, ID: clearingID}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.uncleared", "bank_statement", statementID,
			map[string]any{"journal_line_id": c.JournalLineID, "amount": c.Amount.String()}, nil))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}

func absDays(a, b civil.Date) int {
	d := a.DaysUntil(b)
	if d < 0 {
		return -d
	}
	return d
}

// AutoMatch matches every statement line that has nothing matched yet with the one uncleared journal line of the same
// amount that is dated within three days of it (the nearest, when it is the only nearest) (bank.reconcile). What it
// cannot decide is left for a person.
func (s *Service) AutoMatch(ctx context.Context, propertyID, statementID int64) (AutoMatchResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return AutoMatchResult{}, err
	}
	var res AutoMatchResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		res = AutoMatchResult{}
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		st, _, err := s.openForWork(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		lines, err := s.q(ctx).ListStatementLines(ctx, bankrecdb.ListStatementLinesParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: statementID})
		if err != nil {
			return err
		}
		pool, err := s.unclearedFor(ctx, p.TenantID, propertyID, st)
		if err != nil {
			return err
		}
		used := map[int64]bool{}
		for _, l := range lines {
			if !l.Cleared.IsZero() {
				continue
			}
			// The nearest journal line of the same amount wins; one that carries the reference of the statement line (the
			// transfer reference the guest gave) beats one that does not, and may be further away.
			best, bestScore, ties := -1, 1<<30, 0
			ref := strings.ToLower(strings.TrimSpace(deref(l.Reference)))
			for i, c := range pool {
				if used[c.JournalLineID] || !c.Remaining.Equal(l.Amount) {
					continue
				}
				d := absDays(l.LineDate, c.Date)
				hit := ref != "" && strings.Contains(strings.ToLower(c.Description+" "+c.Reference), ref)
				if d > autoMatchDays && (!hit || d > autoMatchRefDays) {
					continue
				}
				score := d
				if hit {
					score = d - 1000
				}
				switch {
				case score < bestScore:
					best, bestScore, ties = i, score, 1
				case score == bestScore:
					ties++
				}
			}
			if best < 0 || ties != 1 {
				res.Remaining++
				continue
			}
			c := pool[best]
			used[c.JournalLineID] = true
			if err := s.q(ctx).InsertClearing(ctx, bankrecdb.InsertClearingParams{
				TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, StatementID: statementID, StatementLineID: &l.ID, JournalLineID: c.JournalLineID,
				Amount: c.Remaining, Now: s.clock.Now(), ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
			res.Matched++
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.auto_matched", "bank_statement", statementID, nil, map[string]any{"matched": res.Matched, "remaining": res.Remaining}))
	})
	return res, err
}

// Adjust posts what the bank shows and the books lack (a bank fee, interest earned) from a statement line that has
// nothing matched (bank.reconcile): a BANK journal dated the day of the line, the bank account against the account chosen,
// which is cleared against the line. The date must be in an open accounting period.
func (s *Service) Adjust(ctx context.Context, propertyID, statementID, lineID int64, in AdjustInput) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	if len([]rune(in.Description)) > 300 {
		return StatementDetail{}, apperr.Invalid("the adjustment is invalid", fieldErr("description", "TOO_LONG", "at most 300 characters"))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the bank account (48)
		if err != nil {
			return err
		}
		st, ba, err := s.openForWork(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		line, err := q.GetStatementLine(ctx, bankrecdb.GetStatementLineParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: statementID, ID: lineID})
		if isNoRows(err) {
			return apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement")
		}
		if err != nil {
			return err
		}
		lines, err := q.ListStatementLines(ctx, bankrecdb.ListStatementLinesParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: statementID})
		if err != nil {
			return err
		}
		for _, l := range lines {
			if l.ID == lineID && !l.Cleared.IsZero() {
				return apperr.Conflict("LINE_ALREADY_MATCHED", "the line has something matched already: unmatch it first")
			}
		}
		if in.AccountID == ba.AccountID {
			return apperr.Invalid("the adjustment is invalid", fieldErr("account_id", "SAME_ACCOUNT", "choose the account the bank item belongs to, not the bank account itself"))
		}
		if err := po.CheckAccount(ctx, in.AccountID, "account_id"); err != nil {
			return err
		}
		desc := strings.TrimSpace(in.Description)
		if desc == "" {
			desc = deref(line.Description)
		}
		if desc == "" {
			desc = "Bank statement line " + itoa(int(line.LineNo))
		}
		ref := deref(line.Reference)
		if ref == "" {
			ref = fmt.Sprintf("ST%d-%d", statementID, line.LineNo)
		}
		amount := line.Amount.Abs()
		bankLine := accounting.SystemLine{AccountID: ba.AccountID, Description: desc, SourceType: "BANK_LINE", SourceRef: ref}
		other := accounting.SystemLine{AccountID: in.AccountID, Description: desc, SourceType: "BANK_LINE", SourceRef: ref}
		if line.Amount.IsPositive() { // money in: the bank account is debited
			bankLine.Debit, other.Credit = amount, amount
		} else {
			bankLine.Credit, other.Debit = amount, amount
		}
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{
			Type: accounting.JournalBank, Date: line.LineDate, Description: "Bank: " + desc, Reference: ref, Lines: []accounting.SystemLine{bankLine, other},
		})
		if err != nil {
			return err
		}
		jl, err := q.JournalLineOfAccount(ctx, bankrecdb.JournalLineOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: jid, AccountID: ba.AccountID})
		if err != nil {
			return err
		}
		if err := q.InsertClearing(ctx, bankrecdb.InsertClearingParams{
			TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, StatementID: statementID, StatementLineID: &lineID, JournalLineID: jl,
			Amount: line.Amount, Now: s.clock.Now(), ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.adjusted", "bank_statement", statementID, nil,
			map[string]any{"line": line.LineNo, "amount": line.Amount.String(), "account_id": in.AccountID, "journal": jnum}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}

// Reconcile closes a statement (bank.reconcile): the statement before it is reconciled, every line is matched and the
// journal lines cleared so far, with those of the statements before it, add up to the closing balance of the bank.
func (s *Service) Reconcile(ctx context.Context, propertyID, statementID int64) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if _, _, err := s.openForWork(ctx, p.TenantID, propertyID, statementID); err != nil {
			return err
		}
		d, err := s.loadDetail(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		if !d.Summary.CanReconcile {
			e := apperr.Conflict("STATEMENT_NOT_READY", "the statement cannot be reconciled yet").WithContext("blockers", d.Summary.Blockers)
			return e
		}
		if err := s.q(ctx).MarkReconciled(ctx, bankrecdb.MarkReconciledParams{TenantID: p.TenantID, PropertyID: propertyID, ID: statementID, Now: ptr(s.clock.Now()), ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.statement_reconciled", "bank_statement", statementID, nil,
			map[string]any{"to": d.PeriodTo.String(), "closing": d.ClosingBalance.String(), "book_balance": d.Summary.BookBalance.String()}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}

// Reopen reopens the latest reconciled statement of an account (bank.reconcile plus an approval, with a reason).
func (s *Service) Reopen(ctx context.Context, propertyID, statementID int64, in ReopenInput) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return StatementDetail{}, apperr.Invalid("the reopening is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return StatementDetail{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		st, err := s.statementRow(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{st.BankAccountID}); err != nil {
			return errBankAccountNotFound()
		}
		st, err = s.statementRow(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		if st.Status != StatementReconciled {
			return apperr.Conflict("STATEMENT_NOT_RECONCILED", "the statement is open")
		}
		later, err := s.q(ctx).LaterReconciledExists(ctx, bankrecdb.LaterReconciledExistsParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, After: st.PeriodTo})
		if err != nil {
			return err
		}
		if later {
			return apperr.Conflict("STATEMENT_NOT_LATEST", "only the latest reconciled statement of an account can be reopened")
		}
		if err := s.q(ctx).MarkReopened(ctx, bankrecdb.MarkReopenedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: statementID, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.statement_reopened", "bank_statement", statementID, nil,
			map[string]any{"reason": reason, "approved_by": approval.UserID()}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}
