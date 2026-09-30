package rates

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/rates/ratesdb"
)

func errRatePlanNotFound() *apperr.Error {
	return apperr.NotFound("RATE_PLAN_NOT_FOUND", "the rate plan does not exist in this property")
}

func errRoomTypeNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property")
}

func errChargeCodeNotFound() *apperr.Error {
	return apperr.NotFound("CHARGE_CODE_NOT_FOUND", "the charge code does not exist in this property")
}

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func toRatePlan(p ratesdb.RatePlan, codeName, priceMode string) RatePlan {
	return RatePlan{
		ID: p.ID, Code: p.Code, Name: p.Name, Description: deref(p.Description), MealPlan: p.MealPlan,
		CancellationPolicy: deref(p.CancellationPolicy), IsRefundable: p.IsRefundable, RoomChargeCodeID: p.RoomChargeCodeID,
		RoomChargeCode: codeName, PriceMode: priceMode, IsActive: p.IsActive, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}
