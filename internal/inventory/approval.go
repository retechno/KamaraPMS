package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The approval chain (35-inventory.md 5.8, I13): the levels of a purchase request, configured for each property; the snapshot of the levels for each round of each document; who may decide the active level;
// the notices. It is a small engine of its own, not iam.Verify: it remembers the levels, who decided and what they changed. The service methods that decide (Approve, Return) are in requests.go; chainpath_guard_test.go
// holds that nothing else writes the states SUBMITTED, APPROVED and RETURNED of a request.

// Documents that have a chain. The requisition (SR) joins in PR 3.
const (
	DocPR = "PR"
	DocSR = "SR"
)

// Approver kinds of a level, and the conditions.
const (
	ApproverDeptHead   = "DEPT_HEAD"
	ApproverPermission = "PERMISSION"
	CondAlways         = "ALWAYS"
	CondTotalAbove     = "TOTAL_ABOVE"
)

// States of a step.
const (
	StepWaiting    = "WAITING"
	StepActive     = "ACTIVE"
	StepApproved   = "APPROVED"
	StepReturned   = "RETURNED"
	StepSkipped    = "SKIPPED"
	StepNotReached = "NOT_REACHED"
)

const maxLevels = 5

// permissionsOfLevel are the permissions a level may name, by document.
var permissionsOfLevel = map[string][]string{
	DocPR: {string(auth.PermInventoryCostControl), string(auth.PermInventoryFinalApprove)},
	DocSR: {string(auth.PermInventoryCostControl), string(auth.PermInventoryFinalApprove), string(auth.PermInventorySRFinalApprove)},
}

// ApprovalLevel is a level of the chain of a document.
type ApprovalLevel struct {
	LevelNo       int              `json:"level_no"`
	Name          string           `json:"name"`
	ApproverKind  string           `json:"approver_kind"`
	Permission    *string          `json:"permission"`
	ConditionKind string           `json:"condition_kind"`
	Threshold     *decimal.Decimal `json:"threshold"`
}

// ApprovalChain is the chain of a property for a kind of document, with its two options.
type ApprovalChain struct {
	DocType          string          `json:"doc_type"`
	SkipLevel1IfHead bool            `json:"skip_level1_if_head"`
	ApproverMayEdit  bool            `json:"approver_may_edit"`
	Version          int32           `json:"version"`
	Levels           []ApprovalLevel `json:"levels"`
}

// ApprovalChainInput replaces the chain; the levels are numbered by their position.
type ApprovalChainInput struct {
	SkipLevel1IfHead bool            `json:"skip_level1_if_head"`
	ApproverMayEdit  bool            `json:"approver_may_edit"`
	Version          int32           `json:"version"`
	Levels           []ApprovalLevel `json:"levels"`
}

func defaultLevels(doc string) []ApprovalLevel {
	head := ApprovalLevel{LevelNo: 1, Name: "Kepala departemen", ApproverKind: ApproverDeptHead, ConditionKind: CondAlways}
	if doc == DocSR {
		sr := string(auth.PermInventorySRFinalApprove)
		return []ApprovalLevel{head, {LevelNo: 2, Name: "Persetujuan akhir SR", ApproverKind: ApproverPermission, Permission: &sr, ConditionKind: CondAlways}}
	}
	cc, fa := string(auth.PermInventoryCostControl), string(auth.PermInventoryFinalApprove)
	five := decimal.NewFromInt(5_000_000)
	return []ApprovalLevel{head,
		{LevelNo: 2, Name: "Cost control", ApproverKind: ApproverPermission, Permission: &cc, ConditionKind: CondAlways},
		{LevelNo: 3, Name: "Persetujuan akhir (FC/GM)", ApproverKind: ApproverPermission, Permission: &fa, ConditionKind: CondTotalAbove, Threshold: &five}}
}

func validDoc(doc string) bool { return doc == DocPR || doc == DocSR }

// ensureChain gives a property the default chain of a document the first time it is needed: the settings row and the levels, or nothing when they exist. A chain whose levels were all removed keeps its
// settings row, so "never configured" and "configured with no level" are told apart.
func (s *Service) ensureChain(ctx context.Context, p auth.Principal, propertyID int64, doc string) error {
	q := s.q(ctx)
	if _, err := q.GetApprovalSettings(ctx, inventorydb.GetApprovalSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc}); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	n, err := q.InsertApprovalSettings(ctx, inventorydb.InsertApprovalSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc, SkipLevel1IfHead: true, ApproverMayEdit: true, ActorID: p.ActorID()})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // another transaction made it
	}
	for _, l := range defaultLevels(doc) {
		if err := q.InsertApprovalLevel(ctx, inventorydb.InsertApprovalLevelParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc, LevelNo: int16(l.LevelNo), Name: l.Name, //nolint:gosec // G115: at most 5
			ApproverKind: l.ApproverKind, Permission: l.Permission, ConditionKind: l.ConditionKind, Threshold: l.Threshold, ActorID: p.ActorID()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) loadChain(ctx context.Context, tenantID, propertyID int64, doc string) (ApprovalChain, error) {
	q := s.q(ctx)
	cfg, err := q.GetApprovalSettings(ctx, inventorydb.GetApprovalSettingsParams{TenantID: tenantID, PropertyID: propertyID, DocType: doc})
	if err != nil {
		return ApprovalChain{}, err
	}
	rows, err := q.ListApprovalLevels(ctx, inventorydb.ListApprovalLevelsParams{TenantID: tenantID, PropertyID: propertyID, DocType: doc})
	if err != nil {
		return ApprovalChain{}, err
	}
	c := ApprovalChain{DocType: doc, SkipLevel1IfHead: cfg.SkipLevel1IfHead, ApproverMayEdit: cfg.ApproverMayEdit, Version: cfg.Version, Levels: make([]ApprovalLevel, 0, len(rows))}
	for _, r := range rows {
		c.Levels = append(c.Levels, ApprovalLevel{LevelNo: int(r.LevelNo), Name: r.Name, ApproverKind: r.ApproverKind, Permission: r.Permission, ConditionKind: r.ConditionKind, Threshold: r.Threshold})
	}
	return c, nil
}

// canSeeChain are the permissions that read a chain: the one that edits it, the ones of the module and the approvers.
var chainReaders = []auth.Permission{auth.PermPropertyManage, auth.PermInventoryView, auth.PermInventoryManage, auth.PermInventoryRequest, auth.PermInventoryPurchase}

// GetApprovalChain answers the chain of a kind of document (property.manage, or a permission of the module). A property that has none yet gets the default.
func (s *Service) GetApprovalChain(ctx context.Context, propertyID int64, doc string) (ApprovalChain, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return ApprovalChain{}, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, chainReaders...); err != nil {
		return ApprovalChain{}, err
	}
	if !validDoc(doc) {
		return ApprovalChain{}, notFound("APPROVAL_CHAIN_NOT_FOUND", "approval chain")
	}
	var out ApprovalChain
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.ensureChain(ctx, p, propertyID, doc); err != nil {
			return err
		}
		out, err = s.loadChain(ctx, p.TenantID, propertyID, doc)
		return err
	})
	return out, err
}

// SetApprovalChain replaces the levels and the options of a chain (property.manage). A document in flight keeps its snapshot; a document submitted afterwards, and a resubmission, gets this chain.
func (s *Service) SetApprovalChain(ctx context.Context, propertyID int64, doc string, in ApprovalChainInput) (ApprovalChain, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return ApprovalChain{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPropertyManage); err != nil {
		return ApprovalChain{}, err
	}
	if !validDoc(doc) {
		return ApprovalChain{}, notFound("APPROVAL_CHAIN_NOT_FOUND", "approval chain")
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return ApprovalChain{}, err
	}
	var f []apperr.FieldError
	if len(in.Levels) > maxLevels {
		f = append(f, fieldErr("levels", "OUT_OF_RANGE", "0 to 5 levels"))
	}
	for i, l := range in.Levels {
		at := fmt.Sprintf("levels[%d].", i)
		if n := len([]rune(trimmed(l.Name))); n < 1 || n > 60 {
			f = append(f, fieldErr(at+"name", "INVALID_VALUE", "1 to 60 characters"))
		}
		switch l.ApproverKind {
		case ApproverDeptHead:
			if l.Permission != nil {
				f = append(f, fieldErr(at+"permission", "INVALID_VALUE", "the head of the department is not a permission"))
			}
		case ApproverPermission:
			if l.Permission == nil || !slices.Contains(permissionsOfLevel[doc], *l.Permission) {
				f = append(f, fieldErr(at+"permission", "INVALID_VALUE", "one of "+fmt.Sprint(permissionsOfLevel[doc])))
			}
		default:
			f = append(f, fieldErr(at+"approver_kind", "INVALID_VALUE", "DEPT_HEAD or PERMISSION"))
		}
		switch l.ConditionKind {
		case CondAlways:
			if l.Threshold != nil {
				f = append(f, fieldErr(at+"threshold", "INVALID_VALUE", "a level that always applies has no amount"))
			}
		case CondTotalAbove:
			if l.Threshold == nil || l.Threshold.IsNegative() || !l.Threshold.Equal(l.Threshold.Round(prop.CurrencyDecimals)) {
				f = append(f, fieldErr(at+"threshold", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", prop.CurrencyDecimals)))
			}
		default:
			f = append(f, fieldErr(at+"condition_kind", "INVALID_VALUE", "ALWAYS or TOTAL_ABOVE"))
		}
	}
	if len(f) > 0 {
		e := apperr.Invalid("the approval chain is invalid", f...)
		e.Code = "APPROVAL_CHAIN_INVALID"
		return ApprovalChain{}, e
	}
	var out ApprovalChain
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := s.ensureChain(ctx, p, propertyID, doc); err != nil {
			return err
		}
		q := s.q(ctx)
		old, err := s.loadChain(ctx, p.TenantID, propertyID, doc)
		if err != nil {
			return err
		}
		if old.Version != in.Version {
			return versionConflict("approval chain", old.Version)
		}
		if _, err := q.UpdateApprovalSettings(ctx, inventorydb.UpdateApprovalSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc, SkipLevel1IfHead: in.SkipLevel1IfHead,
			ApproverMayEdit: in.ApproverMayEdit, Version: in.Version, ActorID: p.ActorID()}); err != nil {
			return orNotFound(err, versionConflict("approval chain", old.Version))
		}
		if err := q.DeleteApprovalLevels(ctx, inventorydb.DeleteApprovalLevelsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc}); err != nil {
			return err
		}
		for i, l := range in.Levels {
			if err := q.InsertApprovalLevel(ctx, inventorydb.InsertApprovalLevelParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc, LevelNo: int16(i + 1), //nolint:gosec // G115: at most 5
				Name: trimmed(l.Name), ApproverKind: l.ApproverKind, Permission: l.Permission, ConditionKind: l.ConditionKind, Threshold: l.Threshold, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		out, err = s.loadChain(ctx, p.TenantID, propertyID, doc)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.approval_chain_changed", "inv_approval_chain", propertyID, doc, old, out))
	})
	return out, err
}

// ------------------------------------------------------------------------------------------------ the steps

// ApprovalStep is a level in one round of one document: the snapshot of the chain and what was decided at it.
type ApprovalStep struct {
	ID            int64            `json:"id"`
	Cycle         int              `json:"cycle"`
	LevelNo       int              `json:"level_no"`
	Name          string           `json:"name"`
	ApproverKind  string           `json:"approver_kind"`
	Permission    *string          `json:"permission"`
	ConditionKind string           `json:"condition_kind"`
	Threshold     *decimal.Decimal `json:"threshold"`
	EditAllowed   bool             `json:"edit_allowed"`
	Status        string           `json:"status"`
	SkipReason    *string          `json:"skip_reason"`
	ActivatedAt   *time.Time       `json:"activated_at"`
	DecidedBy     *int64           `json:"decided_by"`
	DecidedByName string           `json:"decided_by_name,omitempty"`
	DecidedAt     *time.Time       `json:"decided_at"`
	TotalSeen     *decimal.Decimal `json:"total_seen"`
	Note          *string          `json:"note"`
	// Applies is the provisional answer for a step that is not reached yet: whether the condition holds on the total now.
	Applies bool `json:"applies"`
	// Approvers are the people who may decide an ACTIVE step (names), so that the requester sees who is being waited for.
	Approvers []string `json:"approvers,omitempty"`
}

// ApprovalEdit is a change an approver made to a line, as it was recorded.
type ApprovalEdit struct {
	ID           int64           `json:"id"`
	StepID       int64           `json:"step_id"`
	LevelNo      int             `json:"level_no"`
	Cycle        int             `json:"cycle"`
	LineNo       int             `json:"line_no"`
	Field        string          `json:"field"`
	OldValue     decimal.Decimal `json:"old_value"`
	NewValue     decimal.Decimal `json:"new_value"`
	EditedBy     int64           `json:"edited_by"`
	EditedByName string          `json:"edited_by_name"`
	EditedAt     time.Time       `json:"edited_at"`
}

func toStep(r inventorydb.ListStepsRow, total decimal.Decimal) ApprovalStep {
	st := ApprovalStep{ID: r.ID, Cycle: int(r.Cycle), LevelNo: int(r.LevelNo), Name: r.Name, ApproverKind: r.ApproverKind, Permission: r.Permission, ConditionKind: r.ConditionKind, Threshold: r.Threshold,
		EditAllowed: r.EditAllowed, Status: r.Status, SkipReason: r.SkipReason, ActivatedAt: r.ActivatedAt, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt, TotalSeen: r.TotalSeen, Note: r.Note}
	if r.DecidedByName != nil {
		st.DecidedByName = *r.DecidedByName
	}
	st.Applies = r.ConditionKind == CondAlways || (r.Threshold != nil && total.GreaterThan(*r.Threshold))
	return st
}

type person struct {
	ID   int64
	Name string
}

// eligible are the people who may decide a step of a request now: for a level of the head of the department, the head or the deputy of the department of the document (a sub-department with none takes its
// parent's) who holds inventory.dept_approve; for a level of a permission, the holders of it. Never the requester (R2), never one who decided another level of the round (R1).
func (s *Service) eligible(ctx context.Context, tenantID, propertyID int64, departmentID *int64, requesterID int64, kind string, permission *string, decided map[int64]bool) ([]person, error) {
	q := s.q(ctx)
	var out []person
	add := func(id int64, name string) {
		if id != requesterID && !decided[id] {
			out = append(out, person{id, name})
		}
	}
	switch kind {
	case ApproverPermission:
		if permission == nil {
			return nil, nil
		}
		users, err := q.ListUsersWithPermission(ctx, inventorydb.ListUsersWithPermissionParams{TenantID: tenantID, PropertyID: propertyID, Permission: *permission})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			add(u.ID, u.FullName)
		}
	case ApproverDeptHead:
		if departmentID == nil {
			return nil, nil
		}
		l, err := q.DepartmentLeaders(ctx, inventorydb.DepartmentLeadersParams{TenantID: tenantID, PropertyID: propertyID, ID: *departmentID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		head, deputy := leadersOf(l)
		for _, id := range []*int64{head, deputy} {
			if id == nil {
				continue
			}
			ok, err := q.UserCanAt(ctx, inventorydb.UserCanAtParams{TenantID: tenantID, PropertyID: propertyID, UserID: *id, Permission: string(auth.PermInventoryDeptApprove)})
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			name, err := q.GetUserName(ctx, inventorydb.GetUserNameParams{TenantID: tenantID, ID: *id})
			if err != nil {
				return nil, err
			}
			add(*id, name)
		}
	}
	return out, nil
}

// leaderOf says whether a user is the head or the deputy of the department (the parent's when it has none).
func (s *Service) leaderOf(ctx context.Context, tenantID, propertyID int64, departmentID *int64, userID int64) (bool, error) {
	if departmentID == nil {
		return false, nil
	}
	l, err := s.q(ctx).DepartmentLeaders(ctx, inventorydb.DepartmentLeadersParams{TenantID: tenantID, PropertyID: propertyID, ID: *departmentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	head, deputy := leadersOf(l)
	return (head != nil && *head == userID) || (deputy != nil && *deputy == userID), nil
}

// leadersOf are the head and the deputy of a department: its own, or the parent's when it has no head (3.3: a lookup, never a copy).
func leadersOf(l inventorydb.DepartmentLeadersRow) (head, deputy *int64) {
	if l.HeadUserID != nil {
		return l.HeadUserID, l.DeputyUserID
	}
	return l.ParentHeadUserID, l.ParentDeputyUserID
}

// satisfiable says whether each of the levels can be given to a different person (R1): a small matching, at most five levels.
func satisfiable(levels [][]person) bool {
	used := map[int64]bool{}
	var try func(i int) bool
	try = func(i int) bool {
		if i == len(levels) {
			return true
		}
		for _, p := range levels[i] {
			if used[p.ID] {
				continue
			}
			used[p.ID] = true
			if try(i + 1) {
				return true
			}
			delete(used, p.ID)
		}
		return false
	}
	return try(0)
}

func errChainUnsatisfiable(level, dept string) *apperr.Error {
	return apperr.Conflict("APPROVAL_CHAIN_UNSATISFIABLE", "nobody can approve the level \""+level+"\" of this request: ask the administrator to set the head of the department or the permission").
		WithContext("level", level).WithContext("department", dept)
}

// ------------------------------------------------------------------------------------------------ notices

// Kinds of a notice.
const (
	NoticeWaiting  = "APPROVAL_WAITING"
	NoticeApproved = "APPROVED"
	NoticeReturned = "RETURNED"
	NoticeStuck    = "CHAIN_STUCK"
)

func (s *Service) notify(ctx context.Context, tenantID, propertyID int64, users []int64, kind string, stepID, requestID *int64) error {
	now := s.clock.Now()
	for _, u := range users {
		if err := s.q(ctx).InsertNotice(ctx, inventorydb.InsertNoticeParams{TenantID: tenantID, PropertyID: propertyID, UserID: u, Kind: kind, StepID: stepID, RequestID: requestID, CreatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

func ids(ps []person) []int64 {
	out := make([]int64, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// Notice is a line of the bell of a user.
type Notice struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	RequestID     *int64     `json:"request_id"`
	RequestNumber *string    `json:"request_number"`
	StepID        *int64     `json:"step_id"`
	StepName      *string    `json:"step_name"`
	LevelNo       int        `json:"level_no"`
	CreatedAt     time.Time  `json:"created_at"`
	SeenAt        *time.Time `json:"seen_at"`
}

// NoticeList is the bell: the latest notices and how many are not seen.
type NoticeList struct {
	Unseen int      `json:"unseen"`
	Data   []Notice `json:"data"`
}

// Notices answers the notices of the signed-in user at the property. They are the user's own: no permission is needed.
func (s *Service) Notices(ctx context.Context, propertyID int64) (NoticeList, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return NoticeList{}, err
	}
	if _, err := s.days.GetProperty(ctx, propertyID); err != nil {
		return NoticeList{}, err
	}
	q := s.q(ctx)
	rows, err := q.ListNotices(ctx, inventorydb.ListNoticesParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, RowLimit: 30})
	if err != nil {
		return NoticeList{}, err
	}
	n, err := q.CountUnseenNotices(ctx, inventorydb.CountUnseenNoticesParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID})
	if err != nil {
		return NoticeList{}, err
	}
	out := NoticeList{Unseen: int(n), Data: make([]Notice, 0, len(rows))}
	for _, r := range rows {
		out.Data = append(out.Data, Notice{ID: r.ID, Kind: r.Kind, RequestID: r.RequestID, RequestNumber: r.RequestNumber, StepID: r.StepID, StepName: r.StepName, LevelNo: int(r.LevelNo), CreatedAt: r.CreatedAt, SeenAt: r.SeenAt})
	}
	return out, nil
}

// MarkNoticeSeen marks one notice of the signed-in user seen; id 0 marks all of them.
func (s *Service) MarkNoticeSeen(ctx context.Context, propertyID, id int64) error {
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if _, err := s.days.GetProperty(ctx, propertyID); err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		if id == 0 {
			return q.MarkAllNoticesSeen(ctx, inventorydb.MarkAllNoticesSeenParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, Now: &now})
		}
		_, err := q.MarkNoticeSeen(ctx, inventorydb.MarkNoticeSeenParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, ID: id, Now: &now})
		return err
	})
}

// InboxItem is a document that waits for the signed-in user ("Perlu saya setujui").
type InboxItem struct {
	RequestID      int64           `json:"request_id"`
	RequestNumber  string          `json:"request_number"`
	EstTotal       decimal.Decimal `json:"est_total"`
	DepartmentID   *int64          `json:"department_id"`
	DepartmentName *string         `json:"department_name"`
	RequesterID    int64           `json:"requester_id"`
	RequesterName  string          `json:"requester_name"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	StepID         int64           `json:"step_id"`
	LevelNo        int             `json:"level_no"`
	StepName       string          `json:"step_name"`
}

// Inbox answers the documents whose active step the signed-in user may decide now. It is the user's own query: no permission of the module is needed.
func (s *Service) Inbox(ctx context.Context, propertyID int64) ([]InboxItem, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.days.GetProperty(ctx, propertyID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ApprovalInbox(ctx, inventorydb.ApprovalInboxParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, IsAdmin: p.IsTenantAdmin})
	if err != nil {
		return nil, err
	}
	out := make([]InboxItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, InboxItem{RequestID: r.RequestID, RequestNumber: r.RequestNumber, EstTotal: r.EstTotal, DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName, RequesterID: r.RequesterID,
			RequesterName: r.RequesterName, SubmittedAt: r.SubmittedAt, StepID: r.StepID, LevelNo: int(r.LevelNo), StepName: r.StepName})
	}
	return out, nil
}
