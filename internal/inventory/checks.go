package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// What the stock gives to the other modules: the lines of the day-close journal, the value by inventory account for the reconciliation of the general ledger, the accounts that only the
// inventory writes (the control accounts), and the checks of the night audit. They are plain methods, without a permission: the callers are the night audit and the accounting, which
// have asked for theirs (internal/app wires them through small adapters, so that inventory does not import accounting).

// Finding is a thing the checks of the night audit or the setup check report.
//
// The web translates a finding by its code and its params (the language of the person, not of the server); Message is the English sentence for logs and for a client that does not know the code.
// Codes lists every code the checks can report: a test keeps each of them translated in English and Indonesian.
type Finding struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Ref     string            `json:"ref,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

// DayLine is one line the stock adds to the journal of a business date: the account, the department (on the cost side only), the source and the signed amount (debit +, credit -).
type DayLine struct {
	AccountID    int64
	DepartmentID int64 // 0: none
	SourceType   string
	SourceRef    string
	Amount       decimal.Decimal
}

// sourceTypes name what a line of the day close comes from (6.2).
var sourceTypes = map[string]string{
	KindOpening: "INV_OPENING", KindReceipt: "INV_RECEIPT", KindReceiptReversal: "INV_RECEIPT", KindSupplierReturn: "INV_RETURN", KindCostAdjust: "INV_COST_ADJUST",
	KindPosSale: "INV_SALE_COST", KindPosSaleReturn: "INV_SALE_COST", KindNegSettle: "INV_SALE_COST", KindCogsToWaste: "INV_WASTE", KindIssue: "INV_ISSUE", KindIssueReversal: "INV_ISSUE", KindWaste: "INV_WASTE",
	KindStaffMeal: "INV_STAFF_MEAL", KindEntertainment: "INV_ENTERTAINMENT", KindCountLoss: "INV_COUNT", KindCountGain: "INV_COUNT",
}

// StockDayLines are the lines the stock adds to the journal of a business date (accounting.StockSource). The lines of one document and one kind are one line per account; the
// department goes with the account of the cost, never with the inventory account.
func (s *Service) StockDayLines(ctx context.Context, tenantID, propertyID int64, bd civil.Date) ([]DayLine, error) {
	rows, err := s.q(ctx).StockDayLines(ctx, inventorydb.StockDayLinesParams{TenantID: tenantID, PropertyID: propertyID, BusinessDate: bd})
	if err != nil {
		return nil, err
	}
	out := make([]DayLine, 0, len(rows))
	for _, r := range rows {
		l := DayLine{AccountID: r.AccountID, SourceType: sourceTypeOf(r.Kind, r.SourceType), SourceRef: r.SourceRef, Amount: r.Amount}
		if r.DepartmentID != nil && !r.IsInventory {
			l.DepartmentID = *r.DepartmentID
		}
		out = append(out, l)
	}
	return out, nil
}

func sourceTypeOf(kind, source string) string {
	if t, ok := sourceTypes[kind]; ok {
		return t
	}
	return source
}

// StockValueByAccount is the stock value by inventory account as of a business date, from the ledger (6.3): what the general ledger must show on those accounts once the days are closed.
func (s *Service) StockValueByAccount(ctx context.Context, tenantID, propertyID int64, asOf civil.Date) (map[int64]decimal.Decimal, error) {
	rows, err := s.q(ctx).StockValueByAccount(ctx, inventorydb.StockValueByAccountParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	out := map[int64]decimal.Decimal{}
	for _, r := range rows {
		out[r.AccountID] = out[r.AccountID].Add(r.Value)
	}
	// goods received not invoiced is the second control of the stock (6.3): what the receipts, the returns, the bills and the credit notes leave on it, in the sign of the ledger (debits less credits)
	g, err := s.q(ctx).GRNIFromSources(ctx, inventorydb.GRNIFromSourcesParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	if g.AccountID != 0 {
		out[g.AccountID] = g.Balance
	}
	return out, nil
}

// ControlAccountIDs are the accounts that only the inventory path may post to, from the first stock movement on (6.4): the inventory account of every category and goods received not
// invoiced. Before the first movement they are ordinary accounts, and the answer is empty.
func (s *Service) ControlAccountIDs(ctx context.Context, tenantID, propertyID int64) ([]int64, error) {
	q := s.q(ctx)
	has, err := q.HasMovements(ctx, propertyID)
	if err != nil || !has {
		return nil, err
	}
	return q.ControlAccounts(ctx, inventorydb.ControlAccountsParams{TenantID: tenantID, PropertyID: propertyID})
}

// DriftRow is a balance that differs from the sum of the ledger.
type DriftRow struct {
	WarehouseID  int64           `json:"warehouse_id"`
	ItemID       int64           `json:"item_id"`
	BalanceQty   decimal.Decimal `json:"balance_qty"`
	BalanceValue decimal.Decimal `json:"balance_value"`
	LedgerQty    decimal.Decimal `json:"ledger_qty"`
	LedgerValue  decimal.Decimal `json:"ledger_value"`
}

// LayerDriftRow is a balance that differs from the layers and the open shortfalls (FIFO).
type LayerDriftRow struct {
	WarehouseID  int64           `json:"warehouse_id"`
	ItemID       int64           `json:"item_id"`
	BalanceQty   decimal.Decimal `json:"balance_qty"`
	BalanceValue decimal.Decimal `json:"balance_value"`
	LayerQty     decimal.Decimal `json:"layer_qty"`
	LayerValue   decimal.Decimal `json:"layer_value"`
	ShortQty     decimal.Decimal `json:"short_qty"`
	ShortValue   decimal.Decimal `json:"short_value"`
}

// Drift is the drift check of 7.5: the balances against the ledger and, under FIFO, the layers against the balances. Both lists are empty when all is well.
type Drift struct {
	Balances []DriftRow      `json:"balances"`
	Layers   []LayerDriftRow `json:"layers"`
}

// Drift runs the drift check for a property. It reads; it needs no permission of its own (the setup check and the night audit call it).
func (s *Service) Drift(ctx context.Context, tenantID, propertyID int64) (Drift, error) {
	q := s.q(ctx)
	out := Drift{Balances: []DriftRow{}, Layers: []LayerDriftRow{}}
	rows, err := q.DriftBalances(ctx, inventorydb.DriftBalancesParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Balances = append(out.Balances, DriftRow{WarehouseID: r.WarehouseID, ItemID: r.ItemID, BalanceQty: r.BalanceQty, BalanceValue: r.BalanceValue, LedgerQty: r.LedgerQty, LedgerValue: r.LedgerValue})
	}
	cfg, err := q.GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cfg.CostingMethod != MethodFIFO) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	layers, err := q.DriftLayers(ctx, inventorydb.DriftLayersParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, r := range layers {
		out.Layers = append(out.Layers, LayerDriftRow{WarehouseID: r.WarehouseID, ItemID: r.ItemID, BalanceQty: r.BalanceQty, BalanceValue: r.BalanceValue, LayerQty: r.LayerQty,
			LayerValue: r.LayerValue, ShortQty: r.ShortQty, ShortValue: r.ShortValue})
	}
	return out, nil
}

// GLMismatch is an inventory account whose balance in the general ledger differs from the value of the stock.
type GLMismatch struct {
	AccountID    int64
	AccountCode  string
	AccountName  string
	Ledger, Book decimal.Decimal // the general ledger and the stock
}

// GLMismatches compares the inventory accounts of the general ledger with the stock value, both as of the last business day that has a journal (6.3). A property that has no stock
// movement yet, or no journal yet, has nothing to compare.
func (s *Service) GLMismatches(ctx context.Context, tenantID, propertyID int64) ([]GLMismatch, error) {
	q := s.q(ctx)
	if has, err := q.HasMovements(ctx, propertyID); err != nil || !has {
		return nil, err
	}
	last, err := q.LastJournaledDay(ctx, inventorydb.LastJournaledDayParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil || !last.Found {
		return nil, err
	}
	book, err := s.StockValueByAccount(ctx, tenantID, propertyID, last.BusinessDate)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(book))
	for id := range book {
		ids = append(ids, id)
	}
	gl := map[int64]decimal.Decimal{}
	rows, err := q.GLBalancesOf(ctx, inventorydb.GLBalancesOfParams{TenantID: tenantID, PropertyID: propertyID, AccountIds: ids, AsOf: last.BusinessDate})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		gl[r.AccountID] = r.Balance
	}
	var out []GLMismatch
	names := map[int64]inventorydb.ListInventoryAccountsRow{}
	accs, err := q.ListInventoryAccounts(ctx, inventorydb.ListInventoryAccountsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		names[a.ID] = a
	}
	if g, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: tenantID, PropertyID: propertyID, MapKey: "INV_GRNI"}); err == nil {
		names[g.ID] = inventorydb.ListInventoryAccountsRow{ID: g.ID, Code: g.Code, Name: g.Name}
	}
	for _, id := range ids {
		if !gl[id].Equal(book[id]) {
			a := names[id]
			out = append(out, GLMismatch{AccountID: id, AccountCode: a.Code, AccountName: a.Name, Ledger: gl[id], Book: book[id]})
		}
	}
	return out, nil
}

// Codes of the findings of the night audit and of the setup check.
const (
	FindBalanceDrift = "INVENTORY_BALANCE_DRIFT"
	FindGLMismatch   = "INVENTORY_GL_MISMATCH"
	FindNegative     = "INVENTORY_NEGATIVE_STOCK"
	FindUnbilled     = "INVENTORY_RECEIPT_UNBILLED"
	FindQueueFailed  = "INVENTORY_QUEUE_FAILED"
)

// FindingCodes are the codes of the findings of the stock, in the night audit and in the setup check.
var FindingCodes = []string{FindBalanceDrift, FindGLMismatch, FindNegative, FindUnbilled, FindQueueFailed, FindCountOpen, FindAmenityPlanInvalid, FindAmenityHeld, FindAmenityShort,
	"NO_CATEGORY", "NO_WAREHOUSE", "NO_ITEM", "ACCOUNT_MAP_INCOMPLETE", "DEPARTMENT_HEAD_INACTIVE", "APPROVAL_CHAIN_EMPTY"}

func str(v any) string { return fmt.Sprint(v) }

// NightAuditStock is the stock's part of the analysis of the close of a business day (35-inventory.md 5.7): a blocker when a balance disagrees with the ledger or the stock value
// disagrees with the general ledger, a warning for the balances below zero. It reads only.
func (s *Service) NightAuditStock(ctx context.Context, tenantID, propertyID int64, bd civil.Date) (blockers, warnings []Finding, err error) {
	d, err := s.Drift(ctx, tenantID, propertyID)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range d.Balances {
		blockers = append(blockers, Finding{Code: FindBalanceDrift, Ref: fmt.Sprintf("%d/%d", r.WarehouseID, r.ItemID),
			Message: fmt.Sprintf("the balance of item %d in warehouse %d (%s, value %s) differs from the ledger (%s, value %s)", r.ItemID, r.WarehouseID, r.BalanceQty, r.BalanceValue, r.LedgerQty, r.LedgerValue),
			Params:  map[string]string{"part": "balance", "item": str(r.ItemID), "warehouse": str(r.WarehouseID), "qty": str(r.BalanceQty), "value": str(r.BalanceValue), "ledger_qty": str(r.LedgerQty), "ledger_value": str(r.LedgerValue)}})
	}
	for _, r := range d.Layers {
		blockers = append(blockers, Finding{Code: FindBalanceDrift, Ref: fmt.Sprintf("%d/%d", r.WarehouseID, r.ItemID),
			Message: fmt.Sprintf("the layers of item %d in warehouse %d (%s, value %s, less %s of stock sold below zero) differ from the balance (%s, value %s)", r.ItemID, r.WarehouseID, r.LayerQty, r.LayerValue, r.ShortQty, r.BalanceQty, r.BalanceValue),
			Params:  map[string]string{"part": "layers", "item": str(r.ItemID), "warehouse": str(r.WarehouseID), "qty": str(r.LayerQty), "value": str(r.LayerValue), "short_qty": str(r.ShortQty), "balance_qty": str(r.BalanceQty), "balance_value": str(r.BalanceValue)}})
	}
	mm, err := s.GLMismatches(ctx, tenantID, propertyID)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range mm {
		blockers = append(blockers, Finding{Code: FindGLMismatch, Ref: m.AccountCode,
			Message: fmt.Sprintf("the general ledger shows %s on %s %s and the stock is worth %s: a posting dated before the module started, or a journal on a control account, is the usual cause", m.Ledger, m.AccountCode, m.AccountName, m.Book),
			Params:  map[string]string{"account": m.AccountCode, "account_name": m.AccountName, "ledger": str(m.Ledger), "book": str(m.Book)}})
	}
	if n, err := s.q(ctx).CountNegativeBalances(ctx, inventorydb.CountNegativeBalancesParams{TenantID: tenantID, PropertyID: propertyID}); err != nil {
		return nil, nil, err
	} else if n > 0 {
		warnings = append(warnings, Finding{Code: FindNegative, Message: fmt.Sprintf("%d item(s) are below zero in a warehouse: stock was sold before the goods came in", n), Params: map[string]string{"n": str(n)}})
	}
	if n, err := s.q(ctx).CountFailedConsumption(ctx, inventorydb.CountFailedConsumptionParams{TenantID: tenantID, PropertyID: propertyID}); err != nil {
		return nil, nil, err
	} else if n > 0 {
		blockers = append(blockers, Finding{Code: FindQueueFailed, Message: fmt.Sprintf("%d POS sale(s) could not be deducted from stock: retry or cancel them (Inventory > Setup check) before the day closes", n), Params: map[string]string{"n": str(n)}})
	}
	if !bd.IsZero() { // the counts that are open and the amenities of tonight (5.7)
		b, w, err := s.amenityFindings(ctx, tenantID, propertyID, bd)
		if err != nil {
			return nil, nil, err
		}
		blockers, warnings = append(blockers, b...), append(warnings, w...)
	}
	if !bd.IsZero() { // goods received that nobody has billed for 30 days (5.7)
		if n, err := s.q(ctx).CountUnbilledReceiptsOlderThan(ctx, inventorydb.CountUnbilledReceiptsOlderThanParams{TenantID: tenantID, PropertyID: propertyID, Before: bd.AddDays(-30)}); err != nil {
			return nil, nil, err
		} else if n > 0 {
			warnings = append(warnings, Finding{Code: FindUnbilled, Message: fmt.Sprintf("%d receipt(s) older than 30 days have no bill: the supplier's invoice is missing, or the delivery was not charged", n), Params: map[string]string{"n": str(n)}})
		}
	}
	return blockers, warnings, nil
}

// CloseStock is the stock's step of the close of a business day, inside the night audit's transaction: the first close after the first movement audits the lock of the costing method
// once (as accounting.deposit_release_locked is audited). It takes the stock level, which is below the accounting settings the close takes next.
func (s *Service) CloseStock(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	if err := db.EnterLockLevel(ctx, db.LevelStock); err != nil {
		return err
	}
	// the POS queue first (5.7 step 1): every sale of the day is in the stock before the journal; a row that cannot be applied stops the close
	if _, err := s.DrainQueue(ctx, p, propertyID, bd); err != nil {
		return err
	}
	if n, err := s.q(ctx).CountFailedConsumption(ctx, inventorydb.CountFailedConsumptionParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
		return err
	} else if n > 0 {
		return apperr.Conflict("INVENTORY_QUEUE_FAILED", "POS sales could not be deducted from stock: retry or cancel them before the day closes").WithContext("rows", n)
	}
	// the amenities of the day, after the room charges and before the journal (5.7 step 2); idempotent, a shortage never stops it
	if err := s.RunAmenities(ctx, p, propertyID, bd); err != nil {
		return err
	}
	// the snapshots of the day (5.7 step 3, 3.6): the stock is final now, the journal comes next; taking the day again writes the same rows
	if err := s.TakeSnapshots(ctx, p, propertyID, bd); err != nil {
		return err
	}
	q := s.q(ctx)
	lock, err := q.CostingLockToAudit(ctx, inventorydb.CostingLockToAuditParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	done, err := q.CostingLockAudited(ctx, inventorydb.CostingLockAuditedParams{TenantID: p.TenantID, PropertyID: &propertyID})
	if err != nil || done {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.costing_locked", "inventory_settings", propertyID, "inventory settings", nil,
		map[string]any{"costing_method": lock.CostingMethod, "locked_on": lock.CostingLockedOn}))
}
