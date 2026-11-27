package bankrec

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The states of a settlement.
const (
	SettlementPosted = "POSTED"
	SettlementVoided = "VOIDED"
)

// VoidSettlementInput is the void of a settlement: why, and the approval of the kind BANK_SETTLEMENT_VOID.
type VoidSettlementInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

func errSettlementNotFound() *apperr.Error {
	return apperr.NotFound("SETTLEMENT_NOT_FOUND", "the settlement does not exist in this property")
}

func errSettlementVoided() *apperr.Error {
	return apperr.Conflict("SETTLEMENT_ALREADY_VOIDED", "the settlement is voided already")
}

// voidPeriodClosed is the refusal of a void whose month is closed in the books (docs/architecture/40-card-settlement-void.md 3, V1): the reversal is dated the
// settlement, so the month has to be open, and reopening it is a decision of the accountant because it changes the figures of that month's reports.
func voidPeriodClosed(date civil.Date) *apperr.Error {
	month := fmt.Sprintf("%04d-%02d", date.Year(), int(date.Month()))
	return apperr.Conflict("PERIOD_CLOSED", "the books of "+month+" are closed, and the void of this settlement is dated "+date.String()+", the day of the settlement: "+
		"reopening "+month+" changes the figures of that month's reports, so it is the accountant's decision; "+
		"otherwise leave the settlement and post a manual journal in the current month").
		WithContext("period", month+"-01").WithContext("month", month)
}

// VoidSettlement voids a card, e-wallet or QRIS settlement (bank.reconcile plus an approval of the kind BANK_SETTLEMENT_VOID for the gross, with a reason;
// docs/architecture/40-card-settlement-void.md). In one transaction it posts the mirror image of the settlement journal dated the settlement, marks the settlement
// VOIDED, releases its payment lines so they can be settled again, removes the matching of its bank line (whose statements must be open), and clears the bank line and
// its mirror against each other. A VAT claim of the settlement on a filed return is taken back by the next return.
func (s *Service) VoidSettlement(ctx context.Context, propertyID, settlementID int64, in VoidSettlementInput) (SettlementRow, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return SettlementRow{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return SettlementRow{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	pre, err := s.q(ctx).GetSettlement(ctx, bankrecdb.GetSettlementParams{TenantID: p.TenantID, PropertyID: propertyID, ID: settlementID})
	if isNoRows(err) {
		return SettlementRow{}, errSettlementNotFound()
	}
	if err != nil {
		return SettlementRow{}, err
	}
	if pre.Status != SettlementPosted {
		return SettlementRow{}, errSettlementVoided()
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindBankSettlementVoid, Amount: pre.Gross})
	if err != nil {
		return SettlementRow{}, err
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
		if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{pre.BankAccountID}); err != nil {
			return errBankAccountNotFound()
		}
		q := s.q(ctx)
		// read again under the lock of the bank account: two voids of one settlement are serialised here, the second sees it voided
		st, err := q.GetSettlement(ctx, bankrecdb.GetSettlementParams{TenantID: p.TenantID, PropertyID: propertyID, ID: settlementID})
		if err != nil {
			return err
		}
		if st.Status != SettlementPosted {
			return errSettlementVoided()
		}
		ba, err := s.loadBankAccount(ctx, p.TenantID, propertyID, st.BankAccountID)
		if err != nil {
			return err
		}
		if err := po.CheckDate(ctx, st.JournalDate, "date"); err != nil {
			if e, ok := apperr.As(err); ok && e.Code == "PERIOD_CLOSED" {
				return voidPeriodClosed(st.JournalDate)
			}
			return err
		}
		clearings, err := q.SettlementClearings(ctx, bankrecdb.SettlementClearingsParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: st.JournalID, AccountID: ba.AccountID})
		if err != nil {
			return err
		}
		for _, c := range clearings {
			if c.StatementStatus != StatementOpen {
				return apperr.Conflict("STATEMENT_RECONCILED", "the statement that matches the settlement is reconciled: reopen it first").WithContext("statement_id", c.StatementID)
			}
		}
		revID, revNumber, err := po.ReverseSettlement(ctx, st.JournalID, st.JournalDate, reason, approval.UserID())
		if err != nil {
			return err
		}
		now := s.clock.Now()
		n, err := q.MarkSettlementVoided(ctx, bankrecdb.MarkSettlementVoidedParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: settlementID, Now: &now, ActorID: p.ActorID(), Reason: &reason, ApprovedBy: ptr(approval.UserID()), VoidJournalID: &revID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return errSettlementVoided()
		}
		released, err := q.ReleaseSettlementItems(ctx, bankrecdb.ReleaseSettlementItemsParams{TenantID: p.TenantID, PropertyID: propertyID, SettlementID: settlementID, Now: &now})
		if err != nil {
			return err
		}
		// the matching goes; the audit row keeps what it was (40 4.3)
		deleted := make([]map[string]any, 0, len(clearings))
		for _, c := range clearings {
			if err := q.DeleteClearing(ctx, bankrecdb.DeleteClearingParams{TenantID: p.TenantID, PropertyID: propertyID, ID: c.ID}); err != nil {
				return err
			}
			d := map[string]any{"id": c.ID, "statement_id": c.StatementID, "amount": c.Amount.String()}
			if c.LineNo.Valid {
				d["line"] = c.LineNo.Int32
			}
			deleted = append(deleted, d)
		}
		// the bank line and its mirror add up to zero: they are cleared together in the statement whose matching was removed
		bankLine, err := q.JournalLineOfAccount(ctx, bankrecdb.JournalLineOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: st.JournalID, AccountID: ba.AccountID})
		if err != nil {
			return err
		}
		mirror, err := q.JournalLineOfAccount(ctx, bankrecdb.JournalLineOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: revID, AccountID: ba.AccountID})
		if err != nil {
			return err
		}
		target, targetType, label := ba.ID, "bank_account", auditlabel.BankAccount(ctx, propertyID, ba.ID)
		if len(clearings) > 0 {
			stID := clearings[0].StatementID
			target, targetType, label = stID, "bank_statement", auditlabel.BankStatement(ctx, propertyID, stID)
			orig, err := q.GetJournalLine(ctx, bankrecdb.GetJournalLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: bankLine})
			if err != nil {
				return err
			}
			if orig.Cleared.IsZero() {
				pair := []struct {
					line   int64
					amount decimal.Decimal
				}{{bankLine, orig.Amount}, {mirror, orig.Amount.Neg()}}
				for _, c := range pair {
					if err := q.InsertClearing(ctx, bankrecdb.InsertClearingParams{
						TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: ba.ID, StatementID: stID, JournalLineID: c.line, Amount: c.amount, Now: now, ActorID: p.ActorID(),
					}); err != nil {
						return err
					}
				}
			}
		}
		treatment := ""
		if st.VatTreatment != nil {
			treatment = *st.VatTreatment
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.settlement_voided", targetType, target, label, nil, approval.AuditFields(map[string]any{
			"settlement_id": settlementID, "key": st.AccountKey, "gross": st.Gross.String(), "net": st.Net.String(), "fee": st.Fee.String(), "vat": st.VatAmount.String(), "vat_treatment": treatment,
			"journal": st.JournalNumber, "reversal": revNumber, "date": st.JournalDate.String(), "reason": reason, "payments_released": released, "clearings_deleted": deleted,
		})))
	})
	if err != nil {
		return SettlementRow{}, err
	}
	return s.settlementRow(ctx, p.TenantID, propertyID, settlementID)
}
