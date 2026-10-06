package rates

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/rates/ratesdb"
	"kamarapms/internal/tenancy"
)

// The kinds of a bed supplement (docs/architecture/16-bed-variants.md): an amount in the currency of the property, or a percentage of the nightly price.
const (
	BedAdjustAmount  = "AMOUNT"
	BedAdjustPercent = "PERCENT"
)

// BedAdjustment is the supplement of one bed type of one room type on a rate plan, from a date. A row is never changed: a new figure is a new row from a later date.
type BedAdjustment struct {
	ID            int64      `json:"id"`
	RatePlanID    int64      `json:"rate_plan_id"`
	RoomTypeID    int64      `json:"room_type_id"`
	RoomTypeCode  string     `json:"room_type_code,omitempty"`
	BedTypeID     int64      `json:"bed_type_id"`
	BedTypeCode   string     `json:"bed_type_code,omitempty"`
	BedTypeName   string     `json:"bed_type_name,omitempty"`
	AdjustKind    string     `json:"adjust_kind"`
	Amount        string     `json:"amount"`
	EffectiveFrom civil.Date `json:"effective_from"`
	// InForce marks the row that prices the nights of today: the latest row that has started on the business date.
	InForce   bool      `json:"in_force"`
	CreatedAt time.Time `json:"created_at"`
}

// BedAdjustmentInput is a new supplement.
type BedAdjustmentInput struct {
	RoomTypeID    int64
	BedTypeID     int64
	AdjustKind    string
	Amount        string
	EffectiveFrom civil.Date
}

// ParseBedAdjustment checks the kind and reads the amount: a negative one is a discount. An amount has at most the decimals of the currency, a percentage at most three, and a percentage is between -100 and 1000.
func ParseBedAdjustment(kind, amount string, currencyDecimals int32) (decimal.Decimal, *apperr.FieldError) {
	bad := func(field, code, msg string) (decimal.Decimal, *apperr.FieldError) {
		f := fieldErr(field, code, msg)
		return decimal.Zero, &f
	}
	if kind != BedAdjustAmount && kind != BedAdjustPercent {
		return bad("adjust_kind", "INVALID_VALUE", "AMOUNT or PERCENT")
	}
	v, err := decimal.NewFromString(amount)
	if err != nil {
		return bad("amount", "INVALID_AMOUNT", "a number, negative for a discount")
	}
	places := currencyDecimals
	if kind == BedAdjustPercent {
		places = 3
		if v.LessThan(decimal.NewFromInt(-100)) || v.GreaterThan(decimal.NewFromInt(1000)) {
			return bad("amount", "INVALID_VALUE", "a percentage is between -100 and 1000")
		}
	}
	if !v.Equal(v.Round(places)) {
		return bad("amount", "INVALID_AMOUNT", "too many decimals")
	}
	if v.Abs().GreaterThanOrEqual(decimal.New(1, 12)) {
		return bad("amount", "INVALID_AMOUNT", "too large")
	}
	return v, nil
}

// ListBedAdjustments lists every supplement of a rate plan, newest first per room type and bed type.
func (s *Service) ListBedAdjustments(ctx context.Context, propertyID, ratePlanID int64) ([]BedAdjustment, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	if _, err := q.GetRatePlan(ctx, ratesdb.GetRatePlanParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ratePlanID}); err != nil {
		return nil, orNotFound(err, errRatePlanNotFound())
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListBedAdjustments(ctx, ratesdb.ListBedAdjustmentsParams{TenantID: p.TenantID, PropertyID: propertyID, RatePlanID: ratePlanID})
	if err != nil {
		return nil, err
	}
	out := make([]BedAdjustment, len(rows))
	type key struct{ roomType, bedType int64 }
	seen := map[key]bool{} // rows come newest first per type and bed: the first one that has started is in force
	for i, r := range rows {
		k := key{r.RoomTypeID, r.BedTypeID}
		inForce := !seen[k] && !r.EffectiveFrom.After(day.BusinessDate)
		if inForce {
			seen[k] = true
		}
		out[i] = BedAdjustment{
			ID: r.ID, RatePlanID: r.RatePlanID, RoomTypeID: r.RoomTypeID, RoomTypeCode: r.RoomTypeCode, BedTypeID: r.BedTypeID, BedTypeCode: r.BedTypeCode,
			BedTypeName: r.BedTypeName, AdjustKind: r.AdjustKind, Amount: r.Amount.String(), EffectiveFrom: r.EffectiveFrom, InForce: inForce, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

// AddBedAdjustment records a supplement from a date (rate.manage). It never changes a row, and a night that is booked already keeps the price it was given.
func (s *Service) AddBedAdjustment(ctx context.Context, propertyID, ratePlanID int64, in BedAdjustmentInput) (BedAdjustment, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return BedAdjustment{}, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return BedAdjustment{}, err
	}
	var fields []apperr.FieldError
	amount, ferr := ParseBedAdjustment(in.AdjustKind, in.Amount, decimals)
	if ferr != nil {
		fields = append(fields, *ferr)
	}
	if in.RoomTypeID < 1 {
		fields = append(fields, fieldErr("room_type_id", "REQUIRED", ""))
	}
	if in.BedTypeID < 1 {
		fields = append(fields, fieldErr("bed_type_id", "REQUIRED", ""))
	}
	if in.EffectiveFrom.IsZero() {
		fields = append(fields, fieldErr("effective_from", "REQUIRED", ""))
	}
	if len(fields) > 0 {
		return BedAdjustment{}, apperr.Invalid("the supplement is invalid", fields...)
	}

	var out BedAdjustment
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.RoomTypes, db.ForShare, propertyID, []int64{in.RoomTypeID}); err != nil {
			if apperr.IsCode(err, "NOT_FOUND") {
				return errRoomTypeNotFound()
			}
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetRatePlanForShare(ctx, ratesdb.GetRatePlanForShareParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ratePlanID}); err != nil {
			return orNotFound(err, errRatePlanNotFound())
		}
		row, err := q.InsertBedAdjustment(ctx, ratesdb.InsertBedAdjustmentParams{
			TenantID: p.TenantID, PropertyID: propertyID, RatePlanID: ratePlanID, RoomTypeID: in.RoomTypeID, BedTypeID: in.BedTypeID,
			AdjustKind: in.AdjustKind, Amount: amount, EffectiveFrom: in.EffectiveFrom, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = BedAdjustment{
			ID: row.ID, RatePlanID: ratePlanID, RoomTypeID: row.RoomTypeID, BedTypeID: row.BedTypeID, AdjustKind: row.AdjustKind,
			Amount: row.Amount.String(), EffectiveFrom: row.EffectiveFrom, CreatedAt: row.CreatedAt,
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "rate_plan.bed_adjustment_added", "rate_plan", ratePlanID, nil, out))
	})
	return out, err
}

type bedAdjustmentRequest struct {
	RoomTypeID    int64      `json:"room_type_id"`
	BedTypeID     int64      `json:"bed_type_id"`
	AdjustKind    string     `json:"adjust_kind"`
	Amount        string     `json:"amount"`
	EffectiveFrom civil.Date `json:"effective_from"`
}

func (h *Handler) registerBedAdjustments(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/rate-plans/{id}/bed-adjustments", httpx.HandlerFunc(h.listBedAdjustments))
	mux.Handle("POST "+p+"/rate-plans/{id}/bed-adjustments", httpx.HandlerFunc(h.addBedAdjustment))
}

func planPath(r *http.Request) (propertyID, planID int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	planID, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if perr != nil || planID < 1 {
		return 0, 0, errRatePlanNotFound()
	}
	return propertyID, planID, nil
}

func (h *Handler) listBedAdjustments(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := planPath(r)
	if err != nil {
		return err
	}
	rows, err := h.svc.ListBedAdjustments(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) addBedAdjustment(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := planPath(r)
	if err != nil {
		return err
	}
	var req bedAdjustmentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	row, err := h.svc.AddBedAdjustment(r.Context(), pid, id, BedAdjustmentInput(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, row)
}
