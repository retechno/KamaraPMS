package banquet

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The lines of an event and its estimate (36 4.5, 5.1, 5.2, 5.4).

var hundred = decimal.NewFromInt(100)

// editable says whether the lines of an event in this status can still change.
func editable(status string) bool {
	return status == StatusInquiry || status == StatusTentative || status == StatusDefinite
}

func (s *Service) loadLines(ctx context.Context, tenantID, propertyID, eventID int64, lineID, sessionID *int64, activeOnly bool) ([]Line, error) {
	q := s.q(ctx)
	rows, err := q.ListLines(ctx, banquetdb.ListLinesParams{TenantID: tenantID, PropertyID: propertyID, EventID: eventID, ID: lineID, SessionID: sessionID, ActiveOnly: activeOnly})
	if err != nil {
		return nil, err
	}
	comps, err := q.ListLineComponents(ctx, banquetdb.ListLineComponentsParams{TenantID: tenantID, PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	out := make([]Line, len(rows))
	for i, r := range rows {
		out[i] = Line{ID: r.ID, SessionID: r.SessionID, LineNo: int(r.LineNo), ItemID: r.ItemID, PackageID: r.PackageID, Description: r.Description, Basis: r.Basis, Quantity: r.Quantity,
			UnitPrice: r.UnitPrice, PriceMode: r.PriceMode, DiscountPct: r.DiscountPct, DiscountAmount: r.DiscountAmount, DiscountBy: r.DiscountBy, DiscountApprovedBy: r.DiscountApprovedBy,
			ChargeCodeID: r.ChargeCodeID, ChargeCode: r.ChargeCode, FbClass: r.FbClass, PosProductID: r.PosProductID, Components: []LineComponent{}}
		for _, c := range comps {
			if c.LineID == r.ID {
				out[i].Components = append(out[i].Components, LineComponent{ItemID: c.ItemID, Name: c.Name, ChargeCodeID: c.ChargeCodeID, ChargeCode: c.ChargeCode, QtyPerPax: c.QtyPerPax,
					ListPrice: c.ListPrice, SortOrder: int(c.SortOrder), FbClass: c.FbClass, PosProductID: c.PosProductID})
			}
		}
	}
	return out, nil
}

func planLineOf(l Line) PlanLine {
	pl := PlanLine{ID: l.ID, Name: l.Description, IsPackage: l.PackageID != nil, Basis: l.Basis, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Mode: l.PriceMode,
		DiscountPct: l.DiscountPct, DiscountAmount: l.DiscountAmount}
	if l.ChargeCodeID != nil {
		pl.ChargeCodeID = *l.ChargeCodeID
	}
	for _, c := range l.Components {
		pl.Components = append(pl.Components, PlanComponent{ItemID: c.ItemID, Name: c.Name, ChargeCodeID: c.ChargeCodeID, QtyPerPax: c.QtyPerPax, ListPrice: c.ListPrice})
	}
	return pl
}

// guestsOf is the number of guests a session is estimated on: the posted number, else the larger of the guaranteed and the actual number, else the estimated one.
func guestsOf(sn Session, posted *Posting) int {
	if posted != nil {
		return posted.PaxBilled
	}
	if pax, _, _, ok := BilledPax(sn.PaxGuaranteed, sn.PaxActual); ok {
		return pax
	}
	return sn.PaxEstimated
}

func (s *Service) postings(ctx context.Context, tenantID, propertyID, eventID int64) (map[int64]Posting, error) {
	rows, err := s.q(ctx).ListEventPostings(ctx, banquetdb.ListEventPostingsParams{TenantID: tenantID, PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Posting, len(rows))
	for _, r := range rows {
		out[r.SessionID] = Posting{SessionID: r.SessionID, BusinessDate: r.BusinessDate, PaxBilled: int(r.PaxBilled), PaxBasis: r.PaxBasis, Auto: r.Auto, OnGuarantee: r.OnGuarantee, PostedAt: r.PostedAt}
	}
	return out, nil
}

func chargeRequest(propertyID int64, c PlannedCharge) billingconfig.ChargeRequest {
	mode := chargecalc.PriceMode(c.Mode)
	return billingconfig.ChargeRequest{PropertyID: propertyID, ChargeCodeID: c.ChargeCodeID, Quantity: c.Quantity, UnitPrice: c.UnitPrice, PriceMode: &mode, Discount: c.Discount}
}

// estimate runs the plan of every active session through the charge engine and adds it up. It writes nothing.
func (s *Service) estimate(ctx context.Context, tenantID, propertyID, eventID int64, lines []Line, sessions []Session, posted map[int64]Posting) (Estimate, error) {
	decimals, err := s.decimals(ctx, tenantID, propertyID)
	if err != nil {
		return Estimate{}, err
	}
	out := Estimate{Lines: []EstimateLine{}, ByCode: []EstimateCode{}}
	codes := map[int64]*EstimateCode{}
	var order []int64
	for _, sn := range sessions {
		if sn.Status != "ACTIVE" {
			continue
		}
		var mine []PlanLine
		for _, l := range lines {
			if l.SessionID == sn.ID {
				mine = append(mine, planLineOf(l))
			}
		}
		if len(mine) == 0 {
			continue
		}
		var post *Posting
		if p, ok := posted[sn.ID]; ok {
			post = &p
		}
		pax := guestsOf(sn, post)
		per := map[int64]*EstimateLine{}
		for _, c := range PlanSession(mine, pax, decimals) {
			b, err := s.billing.Calculate(ctx, chargeRequest(propertyID, c))
			if err != nil {
				return Estimate{}, err
			}
			el := per[c.LineID]
			if el == nil {
				el = &EstimateLine{LineID: c.LineID, Pax: pax}
				per[c.LineID] = el
			}
			el.Net, el.Service, el.Tax, el.Total = el.Net.Add(b.NetAmount), el.Service.Add(b.ServiceTotal), el.Tax.Add(b.TaxTotal), el.Total.Add(b.TotalAmount)
			ec := codes[c.ChargeCodeID]
			if ec == nil {
				ec = &EstimateCode{ChargeCodeID: c.ChargeCodeID}
				codes[c.ChargeCodeID] = ec
				order = append(order, c.ChargeCodeID)
			}
			ec.Net = ec.Net.Add(b.NetAmount)
		}
		for _, l := range mine {
			if el := per[l.ID]; el != nil {
				out.Lines = append(out.Lines, *el)
				out.Net, out.Service, out.Tax, out.Total = out.Net.Add(el.Net), out.Service.Add(el.Service), out.Tax.Add(el.Tax), out.Total.Add(el.Total)
			}
		}
	}
	names, err := s.chargeCodeNames(ctx, tenantID, propertyID, order)
	if err != nil {
		return Estimate{}, err
	}
	for _, id := range order {
		codes[id].ChargeCode, codes[id].GLAccountCode, codes[id].GLAccountName = names[id].Code, names[id].GlAccountCode, names[id].GlAccountName
		out.ByCode = append(out.ByCode, *codes[id])
	}
	return out, nil
}

func (s *Service) chargeCodeNames(ctx context.Context, tenantID, propertyID int64, ids []int64) (map[int64]banquetdb.ChargeCodeNamesRow, error) {
	out := map[int64]banquetdb.ChargeCodeNamesRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ChargeCodeNames(ctx, banquetdb.ChargeCodeNamesParams{TenantID: tenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// eventEstimate reads the lines, the sessions and the postings of an event and estimates it.
func (s *Service) eventEstimate(ctx context.Context, tenantID, propertyID, eventID int64) ([]Line, Estimate, error) {
	lines, err := s.loadLines(ctx, tenantID, propertyID, eventID, nil, nil, true)
	if err != nil {
		return nil, Estimate{}, err
	}
	sessions, err := s.sessions(ctx, tenantID, propertyID, eventID, nil, "")
	if err != nil {
		return nil, Estimate{}, err
	}
	posted, err := s.postings(ctx, tenantID, propertyID, eventID)
	if err != nil {
		return nil, Estimate{}, err
	}
	est, err := s.estimate(ctx, tenantID, propertyID, eventID, lines, sessions, posted)
	return lines, est, err
}

// ListLines answers the lines of an event with the estimate of the engine (banquet.manage, confirm or read).
func (s *Service) ListLines(ctx context.Context, propertyID, eventID int64) (LinesView, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return LinesView{}, err
	}
	if _, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID}); err != nil {
		return LinesView{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	lines, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return LinesView{}, err
	}
	note, err := s.stockNote(ctx, p.TenantID, propertyID, lines)
	return LinesView{Lines: lines, Estimate: est, Stock: note}, err
}

// Estimate answers the money of an event by the engine.
func (s *Service) Estimate(ctx context.Context, propertyID, eventID int64) (Estimate, error) {
	v, err := s.ListLines(ctx, propertyID, eventID)
	return v.Estimate, err
}

// refreshEstimate stores the contract value of the event and, until a row is paid, recalculates the rows of the deposit schedule that are a percent of it (36 4.7). It runs in the transaction of
// the change that moved the estimate, which holds the event.
func (s *Service) refreshEstimate(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, eventID int64) error {
	_, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	if err := q.SetContractEstimate(ctx, banquetdb.SetContractEstimateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, Estimate: est.Total}); err != nil {
		return err
	}
	rows, err := s.schedule(ctx, propertyID, eventID)
	if err != nil || len(rows) == 0 {
		return err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return err
	}
	fresh := ScheduleAmounts(rows, est.Total, decimals)
	var changed []map[string]any
	for i := range rows {
		if rows[i].Percent != nil && !rows[i].Amount.Equal(fresh[i].Amount) {
			if err := q.SetDepositAmount(ctx, banquetdb.SetDepositAmountParams{PropertyID: propertyID, EventID: eventID, Seq: int32(rows[i].Seq), Amount: fresh[i].Amount}); err != nil { //nolint:gosec // G115: small
				return err
			}
			changed = append(changed, map[string]any{"seq": rows[i].Seq, "old": rows[i].Amount, "new": fresh[i].Amount})
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.deposit_schedule_recalculated", "banquet_event", eventID, "", nil, map[string]any{"rows": changed, "base": est.Total}))
}

// ---------------------------------------------------------------- the lines

func checkLineNumbers(qty, price *decimal.Decimal, pct, amount *decimal.Decimal, decimals int32) []apperr.FieldError {
	var f []apperr.FieldError
	if qty != nil && (!qty.IsPositive() || qty.Round(3).Cmp(*qty) != 0 || qty.GreaterThan(decimal.NewFromInt(9999999))) {
		f = append(f, fieldErr("quantity", "INVALID_VALUE", "above zero, up to three decimals"))
	}
	if price != nil && (price.IsNegative() || price.Round(decimals).Cmp(*price) != 0 || price.GreaterThan(decimal.NewFromInt(999999999999))) {
		f = append(f, fieldErr("unit_price", "INVALID_VALUE", "zero or more, with the decimals of the currency"))
	}
	if pct != nil && amount != nil {
		f = append(f, fieldErr("discount_pct", "INVALID_VALUE", "a discount is a percent or an amount, never both"))
	}
	if pct != nil && (pct.IsNegative() || pct.GreaterThan(hundred) || pct.Round(2).Cmp(*pct) != 0) {
		f = append(f, fieldErr("discount_pct", "INVALID_VALUE", "0 to 100, up to two decimals"))
	}
	if amount != nil && (amount.IsNegative() || amount.Round(decimals).Cmp(*amount) != 0) {
		f = append(f, fieldErr("discount_amount", "INVALID_VALUE", "zero or more, with the decimals of the currency"))
	}
	return f
}

// discountPlan is what a discount would be, before any lock: the base of the line, the percent it comes to and whether the approval of BANQUET_DISCOUNT is needed (above the limit of the
// property; with a limit of zero any discount is).
func discountPlan(base decimal.Decimal, pct, amount *decimal.Decimal, limit decimal.Decimal) (effective decimal.Decimal, needs bool) {
	switch {
	case pct != nil:
		effective = *pct
	case amount != nil && base.IsPositive():
		effective = amount.Mul(hundred).Div(base)
	}
	return effective, effective.IsPositive() && effective.GreaterThan(limit)
}

func (s *Service) callerApproves(ctx context.Context, propertyID int64) bool {
	return s.authz.Require(ctx, propertyID, auth.PermBanquetConfirm) == nil
}

// lineBase is the base a line would discount, for the sessions' guests of the moment.
func lineBase(basis string, isPackage bool, qty, price decimal.Decimal, pax int, decimals int32) decimal.Decimal {
	switch {
	case isPackage:
		return price.Mul(decimal.NewFromInt(int64(pax))).Round(decimals)
	case basis == "PER_PAX":
		return qty.Mul(decimal.NewFromInt(int64(pax))).Mul(price).Round(decimals)
	}
	return qty.Mul(price).Round(decimals)
}

// lockEvent takes the event (level 45) and reads its money header; nothing of the folio is touched.
func (s *Service) lockEvent(ctx context.Context, tenantID, propertyID, eventID int64) (banquetdb.EventMoneyRow, error) {
	if err := db.LockRows(ctx, db.BanquetEvents, db.ForUpdate, propertyID, []int64{eventID}); err != nil {
		return banquetdb.EventMoneyRow{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	hdr, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: tenantID, PropertyID: propertyID, ID: eventID})
	return hdr, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
}

// sessionForLine reads a session the lines may change: of the event, active and not posted.
func (s *Service) sessionForLine(ctx context.Context, tenantID, propertyID, eventID, sessionID int64) (Session, error) {
	sn, err := s.sessionOf(ctx, tenantID, propertyID, eventID, sessionID)
	if err != nil {
		return Session{}, err
	}
	if sn.Status != "ACTIVE" {
		return Session{}, apperr.Conflict("SESSION_CANCELLED", "a cancelled session cannot be changed")
	}
	posted, err := s.q(ctx).SessionHasPosting(ctx, banquetdb.SessionHasPostingParams{PropertyID: propertyID, SessionID: sessionID})
	if err != nil {
		return Session{}, err
	}
	if posted {
		return Session{}, apperr.Conflict("SESSION_POSTED", "the session is posted: correct it with an adjustment on the folio")
	}
	return sn, nil
}

func (s *Service) approvalFor(ctx context.Context, propertyID int64, in *iam.ApprovalInput, base decimal.Decimal) (iam.Approval, error) {
	return s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in, Kind: iam.KindBanquetDiscount, Amount: base, Own: s.callerApproves(ctx, propertyID)})
}

// AddLine sells an item or a package in a session of an event (banquet.manage). The price and the mode are frozen on the line; a package freezes its components. A discount above the limit of the
// property needs the approval of BANQUET_DISCOUNT.
func (s *Service) AddLine(ctx context.Context, propertyID, eventID int64, in LineInput) (LinesView, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return LinesView{}, err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return LinesView{}, err
	}
	f := checkLineNumbers(in.Quantity, in.UnitPrice, in.DiscountPct, in.DiscountAmount, decimals)
	if (in.ItemID == nil) == (in.PackageID == nil) {
		f = append(f, fieldErr("item_id", "INVALID_VALUE", "a line is an item or a package, one of the two"))
	}
	if in.PriceMode != "" && in.PriceMode != PriceExclusive && in.PriceMode != PriceInclusive {
		f = append(f, fieldErr("price_mode", "INVALID_VALUE", "EXCLUSIVE (++) or INCLUSIVE (nett)"))
	}
	if in.Description != nil {
		if n := len([]rune(strings.TrimSpace(*in.Description))); n < 1 || n > 200 {
			f = append(f, fieldErr("description", "INVALID_VALUE", "1 to 200 characters"))
		}
	}
	if len(f) > 0 {
		return LinesView{}, invalid("line", f...)
	}
	// before the transaction: the guests and the base of the discount, to know whether the approval is needed (the approval is verified outside the transaction)
	var approval iam.Approval
	if in.DiscountPct != nil || in.DiscountAmount != nil {
		st, err := s.settings(ctx, propertyID)
		if err != nil {
			return LinesView{}, err
		}
		base, err := s.baseOf(ctx, p.TenantID, propertyID, eventID, in, st, decimals)
		if err != nil {
			return LinesView{}, err
		}
		if _, needs := discountPlan(base, in.DiscountPct, in.DiscountAmount, st.MaxDiscountPctNoApproval); needs {
			if approval, err = s.approvalFor(ctx, propertyID, in.Approval, base); err != nil {
				return LinesView{}, err
			}
		}
	}
	var out LinesView
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		if _, err := s.sessionForLine(ctx, p.TenantID, propertyID, eventID, in.SessionID); err != nil {
			return err
		}
		st, err := s.settings(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		no, err := q.NextLineNo(ctx, banquetdb.NextLineNoParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return err
		}
		mode := in.PriceMode
		if mode == "" {
			mode = st.DefaultPriceMode
		}
		qty := decimal.NewFromInt(1)
		if in.Quantity != nil {
			qty = *in.Quantity
		}
		par := banquetdb.InsertLineParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, SessionID: in.SessionID, LineNo: no, Quantity: qty, PriceMode: mode,
			DiscountPct: in.DiscountPct, DiscountAmount: in.DiscountAmount, ActorID: p.ActorID()}
		if in.DiscountPct != nil || in.DiscountAmount != nil {
			by := p.ActorID()
			par.DiscountBy = by
			if !approval.IsZero() {
				par.DiscountApprovedBy = ptr(approval.UserID())
			}
		}
		var comps []banquetdb.PackageSnapshotRow
		switch {
		case in.ItemID != nil:
			items, err := q.ListItems(ctx, banquetdb.ListItemsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.ItemID})
			if err != nil || len(items) == 0 {
				return orNotFound(err, invalidCode("ITEM_NOT_FOUND", "item_id", "no such item in this property"))
			}
			it := items[0]
			if !it.IsActive {
				return invalidCode("ITEM_NOT_FOUND", "item_id", "the item is not active")
			}
			par.ItemID, par.Basis, par.Description = in.ItemID, it.UnitBasis, it.Name
			par.ChargeCodeID, par.FbClass, par.PosProductID = &it.ChargeCodeID, it.FbClass, it.PosProductID
			par.UnitPrice = it.ListPrice
		default:
			pk, err := q.ListPackages(ctx, banquetdb.ListPackagesParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.PackageID})
			if err != nil || len(pk) == 0 {
				return orNotFound(err, invalidCode("PACKAGE_NOT_FOUND", "package_id", "no such package in this property"))
			}
			if !pk[0].IsActive {
				return invalidCode("PACKAGE_NOT_FOUND", "package_id", "the package is not active")
			}
			if comps, err = q.PackageSnapshot(ctx, banquetdb.PackageSnapshotParams{PropertyID: propertyID, PackageID: *in.PackageID}); err != nil {
				return err
			}
			if len(comps) == 0 {
				return invalidCode("PACKAGE_EMPTY", "package_id", "the package has no component")
			}
			par.PackageID, par.Basis, par.Description, par.UnitPrice = in.PackageID, "PER_PAX", pk[0].Name, pk[0].PricePerPax
			par.Quantity = decimal.NewFromInt(1)
		}
		if in.UnitPrice != nil {
			par.UnitPrice = *in.UnitPrice
		}
		if in.Description != nil {
			par.Description = strings.TrimSpace(*in.Description)
		}
		id, err := q.InsertLine(ctx, par)
		if err != nil {
			return err
		}
		for _, c := range comps {
			if err := q.InsertLineComponent(ctx, banquetdb.InsertLineComponentParams{PropertyID: propertyID, LineID: id, ItemID: c.ItemID, Name: c.Name, ChargeCodeID: c.ChargeCodeID,
				QtyPerPax: c.QtyPerPax, ListPrice: c.ListPrice, FbClass: c.FbClass, PosProductID: c.PosProductID, SortOrder: c.SortOrder}); err != nil {
				return err
			}
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		lines, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		out = LinesView{Lines: lines, Estimate: est}
		data := map[string]any{"event_id": eventID, "session_id": in.SessionID, "description": par.Description, "quantity": par.Quantity, "unit_price": par.UnitPrice, "price_mode": mode,
			"discount_pct": in.DiscountPct, "discount_amount": in.DiscountAmount}
		if !approval.IsZero() {
			data = approval.AuditFields(data)
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.line_added", "banquet_event_line", id, par.Description, nil, data))
	})
	return out, err
}

// baseOf is the base the discount of a new line is a part of, read before the transaction.
func (s *Service) baseOf(ctx context.Context, tenantID, propertyID, eventID int64, in LineInput, st Settings, decimals int32) (decimal.Decimal, error) {
	_ = st
	sn, err := s.sessionOf(ctx, tenantID, propertyID, eventID, in.SessionID)
	if err != nil {
		return decimal.Zero, err
	}
	pax := guestsOf(sn, nil)
	q := s.q(ctx)
	qty := decimal.NewFromInt(1)
	if in.Quantity != nil {
		qty = *in.Quantity
	}
	if in.ItemID != nil {
		items, err := q.ListItems(ctx, banquetdb.ListItemsParams{TenantID: tenantID, PropertyID: propertyID, ID: in.ItemID})
		if err != nil || len(items) == 0 {
			return decimal.Zero, orNotFound(err, invalidCode("ITEM_NOT_FOUND", "item_id", "no such item in this property"))
		}
		price := items[0].ListPrice
		if in.UnitPrice != nil {
			price = *in.UnitPrice
		}
		return lineBase(items[0].UnitBasis, false, qty, price, pax, decimals), nil
	}
	pk, err := q.ListPackages(ctx, banquetdb.ListPackagesParams{TenantID: tenantID, PropertyID: propertyID, ID: in.PackageID})
	if err != nil || len(pk) == 0 {
		return decimal.Zero, orNotFound(err, invalidCode("PACKAGE_NOT_FOUND", "package_id", "no such package in this property"))
	}
	price := pk[0].PricePerPax
	if in.UnitPrice != nil {
		price = *in.UnitPrice
	}
	return lineBase("PER_PAX", true, qty, price, pax, decimals), nil
}

// UpdateLine changes the quantity, the price, the mode or the discount of a line whose session is not posted (banquet.manage).
func (s *Service) UpdateLine(ctx context.Context, propertyID, eventID, lineID int64, in LineUpdate) (LinesView, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return LinesView{}, err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return LinesView{}, err
	}
	f := checkLineNumbers(in.Quantity, in.UnitPrice, in.DiscountPct, in.DiscountAmount, decimals)
	if in.PriceMode != "" && in.PriceMode != PriceExclusive && in.PriceMode != PriceInclusive {
		f = append(f, fieldErr("price_mode", "INVALID_VALUE", "EXCLUSIVE (++) or INCLUSIVE (nett)"))
	}
	if in.ClearDiscount && (in.DiscountPct != nil || in.DiscountAmount != nil) {
		f = append(f, fieldErr("clear_discount", "INVALID_VALUE", "clear the discount or set one"))
	}
	if in.Description != nil {
		if n := len([]rune(strings.TrimSpace(*in.Description))); n < 1 || n > 200 {
			f = append(f, fieldErr("description", "INVALID_VALUE", "1 to 200 characters"))
		}
	}
	if len(f) > 0 {
		return LinesView{}, invalid("line", f...)
	}
	cur, err := s.lineOf(ctx, p.TenantID, propertyID, eventID, lineID)
	if err != nil {
		return LinesView{}, err
	}
	next := mergeLine(cur, in)
	var approval iam.Approval
	if next.DiscountPct != nil || next.DiscountAmount != nil {
		st, err := s.settings(ctx, propertyID)
		if err != nil {
			return LinesView{}, err
		}
		sn, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, cur.SessionID)
		if err != nil {
			return LinesView{}, err
		}
		base := lineBase(next.Basis, next.PackageID != nil, next.Quantity, next.UnitPrice, guestsOf(sn, nil), decimals)
		changed := in.DiscountPct != nil || in.DiscountAmount != nil
		if _, needs := discountPlan(base, next.DiscountPct, next.DiscountAmount, st.MaxDiscountPctNoApproval); needs && changed {
			if approval, err = s.approvalFor(ctx, propertyID, in.Approval, base); err != nil {
				return LinesView{}, err
			}
		}
	}
	var out LinesView
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.lineOf(ctx, p.TenantID, propertyID, eventID, lineID)
		if err != nil {
			return err
		}
		if _, err := s.sessionForLine(ctx, p.TenantID, propertyID, eventID, old.SessionID); err != nil {
			return err
		}
		next := mergeLine(old, in)
		par := banquetdb.UpdateLineParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: lineID, Description: next.Description, Quantity: next.Quantity, UnitPrice: next.UnitPrice,
			PriceMode: next.PriceMode, DiscountPct: next.DiscountPct, DiscountAmount: next.DiscountAmount, DiscountBy: next.DiscountBy, DiscountApprovedBy: next.DiscountApprovedBy, ActorID: p.ActorID()}
		if in.DiscountPct != nil || in.DiscountAmount != nil {
			par.DiscountBy = p.ActorID()
			par.DiscountApprovedBy = nil
			if !approval.IsZero() {
				par.DiscountApprovedBy = ptr(approval.UserID())
			}
		}
		if n, err := s.q(ctx).UpdateLine(ctx, par); err != nil {
			return err
		} else if n == 0 {
			return notFound("LINE_NOT_FOUND", "line")
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		lines, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		out = LinesView{Lines: lines, Estimate: est}
		data := map[string]any{"quantity": next.Quantity, "unit_price": next.UnitPrice, "price_mode": next.PriceMode, "discount_pct": next.DiscountPct, "discount_amount": next.DiscountAmount}
		if !approval.IsZero() {
			data = approval.AuditFields(data)
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.line_updated", "banquet_event_line", lineID, old.Description, old, data))
	})
	return out, err
}

func (s *Service) lineOf(ctx context.Context, tenantID, propertyID, eventID, lineID int64) (Line, error) {
	ls, err := s.loadLines(ctx, tenantID, propertyID, eventID, &lineID, nil, true)
	if err != nil {
		return Line{}, err
	}
	if len(ls) == 0 {
		return Line{}, notFound("LINE_NOT_FOUND", "line")
	}
	return ls[0], nil
}

// mergeLine applies a change to a line: what is left out stays, a new discount replaces the old, and ClearDiscount removes it.
func mergeLine(l Line, in LineUpdate) Line {
	if in.Quantity != nil && l.PackageID == nil {
		l.Quantity = *in.Quantity
	}
	if in.UnitPrice != nil {
		l.UnitPrice = *in.UnitPrice
	}
	if in.PriceMode != "" {
		l.PriceMode = in.PriceMode
	}
	if in.Description != nil {
		l.Description = strings.TrimSpace(*in.Description)
	}
	switch {
	case in.ClearDiscount:
		l.DiscountPct, l.DiscountAmount, l.DiscountBy, l.DiscountApprovedBy = nil, nil, nil, nil
	case in.DiscountPct != nil:
		l.DiscountPct, l.DiscountAmount = in.DiscountPct, nil
	case in.DiscountAmount != nil:
		l.DiscountPct, l.DiscountAmount = nil, in.DiscountAmount
	}
	return l
}

// RemoveLine takes a line off a session that is not posted (banquet.manage).
func (s *Service) RemoveLine(ctx context.Context, propertyID, eventID, lineID int64) (LinesView, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return LinesView{}, err
	}
	var out LinesView
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.lineOf(ctx, p.TenantID, propertyID, eventID, lineID)
		if err != nil {
			return err
		}
		if _, err := s.sessionForLine(ctx, p.TenantID, propertyID, eventID, old.SessionID); err != nil {
			return err
		}
		if n, err := s.q(ctx).DeactivateLine(ctx, banquetdb.DeactivateLineParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: lineID, ActorID: p.ActorID()}); err != nil {
			return err
		} else if n == 0 {
			return notFound("LINE_NOT_FOUND", "line")
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		lines, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		out = LinesView{Lines: lines, Estimate: est}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.line_removed", "banquet_event_line", lineID, old.Description, old, nil))
	})
	return out, err
}
