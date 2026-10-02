package folios

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// RoomPoster is the capability to post room revenue: the only way to post a charge code of type ROOM as a
// CHARGE. Manual charges refuse ROOM codes (docs/architecture/03-financial-engines.md step 10 rule 3), so the
// single room-posting path is whoever holds this value. The app wiring hands it to the room charge posting
// service and to nothing else.
type RoomPoster struct{ s *Service }

// RoomPoster returns the room posting capability. Call it only from the app's wiring.
func (s *Service) RoomPoster() *RoomPoster { return &RoomPoster{s: s} }

// RoomNightCmd is one room night to post: the amount and the charge code come from the stay's nightly snapshot.
type RoomNightCmd struct {
	FolioID      int64
	ChargeCodeID int64
	StayRoomID   int64
	UnitPrice    decimal.Decimal
	PriceMode    chargecalc.PriceMode
	ServiceDate  civil.Date
	Description  string
}

// RoomNight is what a posted room night amounts to.
type RoomNight struct {
	ItemID   int64
	Net      decimal.Decimal
	Service  decimal.Decimal
	Tax      decimal.Decimal
	Rounding decimal.Decimal
	Total    decimal.Decimal
}

// LockFolios takes the row locks (L4, ascending id) of the folios about to be posted to. The caller has
// locked the business day and the stays first.
func (r *RoomPoster) LockFolios(ctx context.Context, propertyID int64, ids []int64) error {
	return db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids)
}

// Post posts one room night to a locked, open folio: quantity 1 at the nightly amount in the snapshot's price
// mode, the breakdown coming from the charge calculation service, stamped with the stay segment of the night
// and the ROOM_POSTING source. It runs in the caller's transaction.
func (r *RoomPoster) Post(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, cmd RoomNightCmd) (RoomNight, error) {
	s := r.s
	folio, err := s.lockFolio(ctx, p.TenantID, propertyID, cmd.FolioID)
	if err != nil {
		return RoomNight{}, err
	}
	if err := requireOpen(folio); err != nil {
		return RoomNight{}, err
	}
	price, mode, seg := cmd.UnitPrice, cmd.PriceMode, cmd.StayRoomID
	item, err := s.posting(p, propertyID, bd, folio).postCharge(ctx, chargeCmd{
		chargeCodeID: cmd.ChargeCodeID, quantity: decimal.NewFromInt(1), unitPrice: &price, priceMode: &mode, serviceDate: cmd.ServiceDate,
		description: cmd.Description, room: &seg,
	})
	if err != nil {
		return RoomNight{}, err
	}
	return RoomNight{ItemID: item.ID, Net: item.NetAmount, Service: item.ServiceChargeTotal, Tax: item.TaxTotal, Rounding: item.RoundingAdjustment, Total: item.Debit.Sub(item.Credit)}, nil
}
