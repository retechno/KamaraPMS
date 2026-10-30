package reports

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// The report of the voids, corrections, discounts and allowances (docs/architecture/38-reports-night-audit-pack.md, 7.4): what was taken back, reduced or given away, by whom, with whose approval and why.

// The kinds of a row and of the filter.
const (
	CorrAll       = "ALL"
	CorrVoid      = "VOID"
	CorrAdjust    = "ADJUSTMENT"
	CorrAllowance = "ALLOWANCE"
	CorrRefund    = "REFUND"
	CorrDiscount  = "DISCOUNT"
	CorrComp      = "COMP"
	CorrPosVoid   = "POS_VOID"
)

var corrKinds = []string{CorrVoid, CorrAdjust, CorrAllowance, CorrRefund, CorrDiscount, CorrComp, CorrPosVoid}

// CorrectionParams are the parameters of the report.
type CorrectionParams struct {
	From, To civil.Date
	Kind     string
	UserID   *int64
	OutletID *int64
}

// CorrectionRow is one event. The amounts of a ledger item are signed as the ledger moves them (a reversal or a discount is negative: it reduces the revenue); a refund, a payment void and a
// complimentary payment are money movements (PaymentMethod is set) and carry only the gross amount, which is negative for a refund and for a payment void: money that went back.
type CorrectionRow struct {
	BusinessDate   civil.Date  `json:"business_date"`
	At             time.Time   `json:"at"`
	Kind           string      `json:"kind"`
	Folio          string      `json:"folio"`
	Reservation    string      `json:"reservation"`
	Bill           string      `json:"bill"`
	Outlet         string      `json:"outlet"`
	ChargeCode     string      `json:"charge_code"`
	ChargeCodeName string      `json:"charge_code_name"`
	PaymentMethod  string      `json:"payment_method"`
	Department     string      `json:"department"`
	Description    string      `json:"description"`
	OriginalItemID *int64      `json:"original_item_id"`
	OriginalDate   *civil.Date `json:"original_date"`
	Net            string      `json:"net"`
	Service        string      `json:"service"`
	Tax            string      `json:"tax"`
	Gross          string      `json:"gross"`
	Reason         string      `json:"reason"`
	DoneBy         string      `json:"done_by"`
	ApprovedBy     string      `json:"approved_by"`
}

// CorrectionTotal is a total under one key (a kind, a reason, a user, an outlet).
type CorrectionTotal struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
	Net   string `json:"net"`
	Gross string `json:"gross"`
}

// CorrectionSum is the revenue the events reduced or gave away: the ledger items, never a payment.
type CorrectionSum struct {
	Count   int    `json:"count"`
	Net     string `json:"net"`
	Service string `json:"service"`
	Tax     string `json:"tax"`
	Gross   string `json:"gross"`
}

// CorrectionMoney is the money that was cancelled or given back: the payment voids and the refunds. It is not revenue.
type CorrectionMoney struct {
	Count  int    `json:"count"`
	Amount string `json:"amount"`
}

// CorrectionReport is the report.
type CorrectionReport struct {
	From             civil.Date        `json:"from"`
	To               civil.Date        `json:"to"`
	Kind             string            `json:"kind"`
	Rows             []CorrectionRow   `json:"rows"`
	ByKind           []CorrectionTotal `json:"by_kind"`
	ByReason         []CorrectionTotal `json:"by_reason"`
	ByUser           []CorrectionTotal `json:"by_user"`
	ByOutlet         []CorrectionTotal `json:"by_outlet"`
	Reduced          CorrectionSum     `json:"reduced"`
	MoneyBack        CorrectionMoney   `json:"money_back"`
	RevenueTakenBack string            `json:"revenue_taken_back"`
	NetRevenue       string            `json:"net_revenue"`
	SharePercent     *string           `json:"share_percent"`
}

// CSV is the list of the events.
func (r CorrectionReport) CSV() ([]string, [][]string) {
	h := []string{"business_date", "at", "kind", "folio", "reservation", "bill", "outlet", "charge_code", "charge_code_name", "payment_method", "department", "description", "original_date", "net", "service", "tax", "gross", "reason", "done_by", "approved_by"}
	rows := make([][]string, 0, len(r.Rows))
	for _, x := range r.Rows {
		rows = append(rows, []string{x.BusinessDate.String(), x.At.UTC().Format(time.RFC3339), x.Kind, x.Folio, x.Reservation, x.Bill, x.Outlet, x.ChargeCode, x.ChargeCodeName, x.PaymentMethod, x.Department, x.Description, optDate(x.OriginalDate),
			x.Net, x.Service, x.Tax, x.Gross, x.Reason, x.DoneBy, x.ApprovedBy})
	}
	return h, rows
}

// CorrectionParamsFrom checks report.view and then reads the query: the permission is answered before the query is looked at.
func (s *Service) CorrectionParamsFrom(ctx context.Context, propertyID int64, q func(string) string) (CorrectionParams, error) {
	if _, _, err := s.access(ctx, propertyID); err != nil {
		return CorrectionParams{}, err
	}
	var errs []apperr.FieldError
	out := CorrectionParams{Kind: CorrAll}
	out.From, out.To = queryDate(q, "from", &errs), queryDate(q, "to", &errs)
	if k := q("kind"); k != "" && k != CorrAll {
		ok := false
		for _, x := range corrKinds {
			ok = ok || x == k
		}
		if !ok {
			errs = append(errs, apperr.FieldError{Field: "kind", Code: "INVALID_VALUE", Message: "ALL, " + strings.Join(corrKinds, ", ")})
		} else {
			out.Kind = k
		}
	}
	for _, f := range []struct {
		name string
		dst  **int64
	}{{"user_id", &out.UserID}, {"outlet_id", &out.OutletID}} {
		if v := q(f.name); v != "" {
			id, err := parseID(v)
			if err != nil {
				errs = append(errs, apperr.FieldError{Field: f.name, Code: "INVALID_VALUE", Message: "a number"})
				continue
			}
			*f.dst = &id
		}
	}
	if len(errs) > 0 {
		return out, apperr.Invalid("the query is invalid", errs...)
	}
	return out, nil
}

func idPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// Corrections answers the report (report.view).
func (s *Service) Corrections(ctx context.Context, propertyID int64, in CorrectionParams) (CorrectionReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return CorrectionReport{}, err
	}
	if err := checkRange(in.From, in.To); err != nil {
		return CorrectionReport{}, err
	}
	q := s.q(ctx)
	items, err := q.CorrectionFolioItems(ctx, reportsdb.CorrectionFolioItemsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: in.From, ToDate: in.To, OutletID: in.OutletID, UserID: in.UserID})
	if err != nil {
		return CorrectionReport{}, err
	}
	pays, err := q.CorrectionPayments(ctx, reportsdb.CorrectionPaymentsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: in.From, ToDate: in.To, OutletID: in.OutletID, UserID: in.UserID})
	if err != nil {
		return CorrectionReport{}, err
	}
	posv, err := q.CorrectionPosVoids(ctx, reportsdb.CorrectionPosVoidsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: in.From, ToDate: in.To, OutletID: in.OutletID, UserID: in.UserID})
	if err != nil {
		return CorrectionReport{}, err
	}
	zero := fx(decimal.Zero, decimals)
	var rows []CorrectionRow
	for _, i := range items {
		kind := ""
		switch {
		case i.IsComp:
			kind = CorrComp
		case i.IsAllowance && (i.TransactionType == "CHARGE" || i.TransactionType == "ADJUSTMENT"):
			kind = CorrAllowance
		case i.TransactionType == "REVERSAL":
			kind = CorrVoid
		case i.TransactionType == "ADJUSTMENT":
			kind = CorrAdjust
		case i.DiscountAmount.IsPositive():
			kind = CorrDiscount
		default:
			continue
		}
		r := CorrectionRow{BusinessDate: i.BusinessDate, At: i.TransactionAt, Kind: kind, Folio: i.FolioNumber, Reservation: i.ConfirmationNumber, Bill: i.BillNumber, Outlet: i.OutletName,
			ChargeCode: i.ChargeCode, ChargeCodeName: i.ChargeCodeName, Department: i.Department, Description: i.Description, OriginalItemID: idPtr(i.OriginalItemID), OriginalDate: i.OriginalDate,
			Net: fx(i.NetAmount, decimals), Service: fx(i.ServiceChargeTotal, decimals), Tax: fx(i.TaxTotal, decimals), Gross: fx(i.Gross, decimals), Reason: i.Reason, DoneBy: i.DoneBy, ApprovedBy: i.ApprovedBy}
		if kind == CorrDiscount {
			net := i.DiscountAmount.Neg()
			r.Net, r.Service, r.Tax, r.Gross = fx(net, decimals), zero, zero, fx(net, decimals)
			if i.DiscountReason != "" {
				r.Reason = i.DiscountReason
			}
			if i.DiscountBy != "" {
				r.ApprovedBy = i.DiscountBy
			}
		}
		rows = append(rows, r)
	}
	for _, y := range pays {
		r := CorrectionRow{BusinessDate: y.BusinessDate, At: y.PaidAt, Folio: y.FolioNumber, Reservation: y.ConfirmationNumber, Outlet: y.OutletName, PaymentMethod: y.PaymentMethod,
			Description: y.PaymentNumber, Net: zero, Service: zero, Tax: zero, Gross: fx(y.Amount, decimals), Reason: y.Remarks, DoneBy: y.DoneBy, ApprovedBy: y.ApprovedBy}
		switch {
		case y.Status == "VOIDED":
			r.Kind, r.Reason, r.Gross = CorrVoid, y.VoidReason, fx(y.Amount.Neg(), decimals)
			if y.VoidedAt != nil {
				r.At = *y.VoidedAt
			}
			if y.VoidedBy != "" {
				r.DoneBy = y.VoidedBy
			}
		case y.PaymentType == "REFUND":
			r.Kind, r.Gross = CorrRefund, fx(y.Amount.Neg(), decimals)
		default:
			r.Kind = CorrComp
		}
		rows = append(rows, r)
	}
	for _, v := range posv {
		r := CorrectionRow{BusinessDate: v.BusinessDate, At: v.VoidedAt, Kind: CorrPosVoid, Folio: "", Bill: deref(v.BillNumber), Outlet: v.OutletName, ChargeCode: v.ChargeCode, ChargeCodeName: v.ChargeCodeName, Department: v.Department,
			Description: v.ProductName, OriginalItemID: idPtr(v.OriginalItemID), OriginalDate: v.OriginalDate,
			Net: fx(v.NetAmount, decimals), Service: fx(v.ServiceChargeTotal, decimals), Tax: fx(v.TaxTotal, decimals), Gross: fx(v.Gross, decimals), Reason: v.VoidReason, DoneBy: v.DoneBy, ApprovedBy: v.ApprovedBy}
		rows = append(rows, r)
	}
	if in.Kind != CorrAll {
		kept := rows[:0]
		for _, r := range rows {
			if r.Kind == in.Kind {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].BusinessDate.Equal(rows[j].BusinessDate) {
			return rows[i].BusinessDate.Before(rows[j].BusinessDate)
		}
		return rows[i].At.Before(rows[j].At)
	})
	out := CorrectionReport{From: in.From, To: in.To, Kind: in.Kind, Rows: rows}
	if out.Rows == nil {
		out.Rows = []CorrectionRow{}
	}
	type acc struct {
		key        string
		n          int
		net, gross decimal.Decimal
	}
	group := func(keyOf func(CorrectionRow) (fold, label string)) []CorrectionTotal {
		idx := map[string]*acc{}
		var order []string
		for _, r := range out.Rows {
			fold, label := keyOf(r)
			a := idx[fold]
			if a == nil {
				a = &acc{key: label}
				idx[fold] = a
				order = append(order, fold)
			}
			n, _ := decimal.NewFromString(r.Net)
			g, _ := decimal.NewFromString(r.Gross)
			a.n++
			a.net, a.gross = a.net.Add(n), a.gross.Add(g)
		}
		sort.SliceStable(order, func(i, j int) bool { return idx[order[i]].n > idx[order[j]].n })
		res := make([]CorrectionTotal, 0, len(order))
		for _, k := range order {
			a := idx[k]
			res = append(res, CorrectionTotal{Key: a.key, Count: a.n, Net: fx(a.net, decimals), Gross: fx(a.gross, decimals)})
		}
		return res
	}
	out.ByKind = group(func(r CorrectionRow) (string, string) { return r.Kind, r.Kind })
	out.ByReason = group(func(r CorrectionRow) (string, string) {
		return foldText(r.Reason), strings.Join(strings.Fields(r.Reason), " ")
	})
	out.ByUser = group(func(r CorrectionRow) (string, string) { return r.DoneBy, r.DoneBy })
	out.ByOutlet = group(func(r CorrectionRow) (string, string) { return r.Outlet, r.Outlet })
	taken := decimal.Zero // the revenue taken back: the net of the ledger items that reduce it (a payment void, a refund and a comp carry a zero net)
	for _, r := range out.Rows {
		if n, _ := decimal.NewFromString(r.Net); n.IsNegative() {
			taken = taken.Add(n.Neg())
		}
	}
	red, back := CorrectionSum{}, CorrectionMoney{}
	var rn, rs, rt, rg, ma decimal.Decimal
	for _, r := range out.Rows {
		switch {
		case r.PaymentMethod == "": // a ledger item: it reduced or gave away revenue
			red.Count++
			rn, rs, rt, rg = rn.Add(dec(r.Net)), rs.Add(dec(r.Service)), rt.Add(dec(r.Tax)), rg.Add(dec(r.Gross))
		case r.Kind == CorrVoid || r.Kind == CorrRefund: // money that went back; a complimentary payment is neither
			back.Count++
			ma = ma.Add(dec(r.Gross))
		}
	}
	red.Net, red.Service, red.Tax, red.Gross = fx(rn, decimals), fx(rs, decimals), fx(rt, decimals), fx(rg, decimals)
	back.Amount = fx(ma, decimals)
	out.Reduced, out.MoneyBack = red, back
	net, err := q.CorrectionNetRevenue(ctx, reportsdb.CorrectionNetRevenueParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: in.From, ToDate: in.To})
	if err != nil {
		return CorrectionReport{}, err
	}
	out.RevenueTakenBack, out.NetRevenue = fx(taken, decimals), fx(net, decimals)
	if net.IsPositive() {
		sh := taken.Div(net).Mul(hundred).StringFixed(2)
		out.SharePercent = &sh
	}
	return out, nil
}
