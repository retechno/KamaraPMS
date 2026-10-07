package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Go-live readiness: whether the night audit of a business date can make its journal. The rules are not a wish list. Each one is a case in which the day close either skips the journal
// without a word (PostDay returns nil: no settings, a date before the start date) or stops in the middle of the close with an error that names one thing (the system account map,
// the department of a line, the journal number), or puts the revenue of the night in the suspense account (a room charge code with no revenue account). The night audit asks for
// this check before it closes a day, so that a property cannot go live and close days without a journal (audit F-15).

// Codes of the readiness blockers (stable: the screens and the tests read them).
const (
	BlockNoSettings         = "ACCOUNTING_NOT_SET_UP"       // no accounting_settings row: PostDay would skip every day
	BlockNotStarted         = "ACCOUNTING_NOT_STARTED"      // the business date is before the accounting start date: PostDay would skip it
	BlockMapMissing         = "ACCOUNT_MAP_MISSING"         // a system account the day close posts to has no account
	BlockMapUnusable        = "ACCOUNT_MAP_UNUSABLE"        // it points at an account that is inactive, a header or of the wrong type
	BlockRoomCodeUnmapped   = "ROOM_CHARGE_CODE_UNMAPPED"   // the room revenue would go to the suspense account
	BlockDepartmentSetup    = "DEPARTMENT_SETUP_INCOMPLETE" // a required department is missing: the day close would stop at that line
	BlockJournalSequence    = "JOURNAL_SEQUENCE_MISSING"    // the journal cannot be numbered
	WarnCodeUnmapped        = "CODE_UNMAPPED"               // another charge code, tax or service charge posts to a fallback account (allowed, listed in /unmapped)
	WarnMapOptionalMissing  = "ACCOUNT_MAP_OPTIONAL"        // a key that other modules use (fiscal year close, payables, cash over and short) is not mapped
	readinessSystemRequired = "the day close posts to it"
)

// dayCloseKeys are the system accounts the day close resolves (resolver.system): the ledgers, one account per payment method and the three fallbacks. They are the keys the night audit
// needs; the others of mapKeys are used by the fiscal year close, the payables and the cashier shifts.
var dayCloseKeys = []string{KeyGuestLedger, KeyAdvanceDeposits, KeyCityLedger, KeyCash, KeyCard, KeyBankTransfer, KeyOtherPayment, KeySuspense, KeyTaxPayable, KeyServicePayable}

// ReadinessBlocker is one thing that stops the night audit.
type ReadinessBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Ref     string `json:"ref,omitempty"` // the map key, charge code or source the blocker is about
}

// Readiness is the answer: Ready is true when there are no blockers. Warnings never stop the night audit.
type Readiness struct {
	Ready    bool               `json:"ready"`
	Status   string             `json:"status"` // READY or NOT_READY
	Blockers []ReadinessBlocker `json:"blockers"`
	Warnings []ReadinessBlocker `json:"warnings"`
}

// Readiness checks the property for the current business date (accounting.view). It writes nothing.
func (s *Service) Readiness(ctx context.Context, propertyID int64) (Readiness, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return Readiness{}, err
	}
	bd, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return Readiness{}, err
	}
	return s.readiness(ctx, p.TenantID, propertyID, bd, false)
}

// JournalReadiness is the check the night audit makes (nightaudit.Journaler). With lock it takes the accounting settings row FOR SHARE, as PostDay does, so that the account map cannot be
// changed between the check and the journal of the same transaction. It must be called after the locks of lower levels (the night audit calls it after the room charges).
func (s *Service) JournalReadiness(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, lock bool) (Readiness, error) {
	return s.readiness(ctx, p.TenantID, propertyID, bd, lock)
}

func (s *Service) readiness(ctx context.Context, tenantID, propertyID int64, bd civil.Date, lock bool) (Readiness, error) {
	q := s.q(ctx)
	out := Readiness{Blockers: []ReadinessBlocker{}, Warnings: []ReadinessBlocker{}}
	block := func(code, ref, msg string) {
		out.Blockers = append(out.Blockers, ReadinessBlocker{Code: code, Ref: ref, Message: msg})
	}

	cfg, err := q.GetSettings(ctx, accountingdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		block(BlockNoSettings, "", "accounting is not set up for this property: a night audit would close the day without a journal")
	case err != nil:
		return out, err
	default:
		if lock {
			if err := db.LockRows(ctx, db.Accounting, db.ForShare, propertyID, []int64{cfg.ID}); err != nil {
				return out, err
			}
		}
		if bd.Before(cfg.StartDate) {
			block(BlockNotStarted, cfg.StartDate.String(), fmt.Sprintf("the business date %s is before the accounting start date %s: its day close would have no journal", bd, cfg.StartDate))
		}
	}

	// the system accounts
	entries, err := s.accountMap(ctx, tenantID, propertyID)
	if err != nil {
		return out, err
	}
	mapped := map[string]MapEntry{}
	for _, e := range entries {
		mapped[e.Key] = e
	}
	accs, err := q.ListAccounts(ctx, accountingdb.ListAccountsParams{TenantID: tenantID, PropertyID: propertyID, RowLimit: maxAccounts + 1})
	if err != nil {
		return out, err
	}
	byID := map[int64]accountingdb.ListAccountsRow{}
	for _, a := range accs {
		byID[a.ID] = a
	}
	required := map[string]bool{}
	for _, k := range dayCloseKeys {
		required[k] = true
	}
	for _, mk := range mapKeys {
		e, ok := mapped[mk.Key]
		if !ok {
			if required[mk.Key] {
				block(BlockMapMissing, mk.Key, fmt.Sprintf("the system account %s (%s) is not mapped; %s", mk.Key, mk.Meaning, readinessSystemRequired))
			} else {
				out.Warnings = append(out.Warnings, ReadinessBlocker{Code: WarnMapOptionalMissing, Ref: mk.Key, Message: fmt.Sprintf("the system account %s (%s) is not mapped; the night audit does not need it", mk.Key, mk.Meaning)})
			}
			continue
		}
		a, found := byID[e.AccountID]
		switch {
		case !found || !a.IsActive || !a.IsPostable:
			if required[mk.Key] {
				block(BlockMapUnusable, mk.Key, fmt.Sprintf("the system account %s points at account %s, which is inactive or takes no postings", mk.Key, e.AccountCode))
			}
		case mk.Type != "" && a.AccountType != mk.Type:
			if required[mk.Key] {
				block(BlockMapUnusable, mk.Key, fmt.Sprintf("the system account %s points at account %s of type %s, it must be %s", mk.Key, e.AccountCode, a.AccountType, mk.Type))
			}
		}
	}

	// the revenue of the room nights
	rooms, err := q.ListRoomChargeCodes(ctx, accountingdb.ListRoomChargeCodesParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, c := range rooms {
		switch {
		case c.GlAccountCode == nil:
			block(BlockRoomCodeUnmapped, c.Code, fmt.Sprintf("the room charge code %s has no account code: the room revenue would be posted to the suspense account", c.Code))
		case c.AccountID == nil:
			block(BlockRoomCodeUnmapped, c.Code, fmt.Sprintf("the room charge code %s points at account %s, which is not in the chart: the room revenue would be posted to the suspense account", c.Code, *c.GlAccountCode))
		case !deref2(c.AccountActive) || !deref2(c.AccountPostable) || c.AccountType == nil || !typeFits("CHARGE_CODE", *c.AccountType):
			block(BlockRoomCodeUnmapped, c.Code, fmt.Sprintf("the room charge code %s points at account %s, which is inactive, a header or not a revenue account: the room revenue would be posted to the suspense account", c.Code, *c.GlAccountCode))
		}
	}

	// a required department that nothing supplies stops the day close at that line
	issues, err := s.setupIssues(ctx, tenantID, propertyID, nil)
	if err != nil {
		return out, err
	}
	for _, i := range issues {
		if i.Severity == SetupError {
			block(BlockDepartmentSetup, i.SourceRef, i.Message)
		}
	}

	// the journal number
	n, err := q.CountJournalSequence(ctx, accountingdb.CountJournalSequenceParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	if n == 0 {
		block(BlockJournalSequence, "", "the document sequence of the journals does not exist: the day close could not number its journal")
	}

	// everything else that falls back to a suspense, tax payable or service payable account is allowed (that is what those accounts are for) and is listed
	usage, err := q.ListCodeUsage(ctx, accountingdb.ListCodeUsageParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	roomCode := map[string]bool{}
	for _, c := range rooms {
		roomCode[c.Code] = true
	}
	for _, r := range usage {
		if !r.IsActive || (r.Kind == "CHARGE_CODE" && roomCode[r.Code]) {
			continue
		}
		bad := r.GlAccountCode == nil || r.AccountID == nil || (r.AccountActive != nil && !*r.AccountActive) || (r.AccountPostable != nil && !*r.AccountPostable) || (r.AccountType != nil && !typeFits(r.Kind, *r.AccountType))
		if bad {
			out.Warnings = append(out.Warnings, ReadinessBlocker{Code: WarnCodeUnmapped, Ref: r.Code, Message: fmt.Sprintf("%s %s has no usable account: its amounts go to a fallback account (see /accounting/unmapped)", sourceNoun(r.Kind), r.Code)})
		}
	}

	out.Ready = len(out.Blockers) == 0
	out.Status = "READY"
	if !out.Ready {
		out.Status = "NOT_READY"
	}
	return out, nil
}

func deref2(b *bool) bool { return b != nil && *b }
