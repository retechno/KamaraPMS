package folios

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The stay folio hooks used by check-in and reverse check-in. They join the caller's transaction and need no
// permission of their own: the front desk use case was authorized.

// StayFolio is a folio as reported with a stay.
type StayFolio struct {
	ID          int64  `json:"id"`
	FolioNumber string `json:"folio_number"`
	Status      string `json:"status"`
	Balance     string `json:"balance"`
}

// LockUnlinkedFolio locks (L4) the reservation's open folio that has no stay, if there is one, and returns its
// id (0 when there is none). Call it before taking any sequence number.
func (s *Service) LockUnlinkedFolio(ctx context.Context, tenantID, propertyID, reservationID int64) (int64, error) {
	id, err := s.q(ctx).FindUnlinkedOpenFolio(ctx, foliosdb.FindUnlinkedOpenFolioParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: reservationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err := s.lockFolio(ctx, tenantID, propertyID, id); err != nil {
		return 0, err
	}
	return id, nil
}

// AttachStayFolio gives the stay its guest folio: the folio locked by LockUnlinkedFolio (a deposit folio) is
// linked to the stay; without one (lockedID 0) a new folio is created, numbered from the sequence.
func (s *Service) AttachStayFolio(ctx context.Context, p auth.Principal, propertyID, reservationID, stayID, lockedID int64) (StayFolio, error) {
	q := s.q(ctx)
	var f foliosdb.Folio
	var err error
	if lockedID != 0 {
		f, err = q.LinkFolioToStay(ctx, foliosdb.LinkFolioToStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lockedID, StayID: &stayID, ActorID: p.ActorID()})
	} else {
		var number string
		if number, err = s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFolio); err != nil {
			return StayFolio{}, err
		}
		f, err = q.InsertFolio(ctx, foliosdb.InsertFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, ReservationID: reservationID, StayID: &stayID, ActorID: p.ActorID()})
	}
	if err != nil {
		return StayFolio{}, err
	}
	return s.stayFolio(ctx, propertyID, f)
}

func (s *Service) stayFolio(ctx context.Context, propertyID int64, f foliosdb.Folio) (StayFolio, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return StayFolio{}, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, f.ID)
	return StayFolio{ID: f.ID, FolioNumber: f.FolioNumber, Status: f.Status, Balance: fixed(balance, decimals)}, err
}

// DetachStayFolio unlinks the stay's folio again (a reversed check-in). The folio must hold no CHARGE items:
// once a night is charged the check-in cannot be undone. Lock order: the reservation and the stay first.
func (s *Service) DetachStayFolio(ctx context.Context, p auth.Principal, propertyID, stayID int64) (StayFolio, error) {
	q := s.q(ctx)
	f, err := q.GetFolioOfStay(ctx, foliosdb.GetFolioOfStayParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return StayFolio{}, orNotFound(err, errFolioNotFound())
	}
	if _, err := s.lockFolio(ctx, p.TenantID, propertyID, f.ID); err != nil {
		return StayFolio{}, err
	}
	n, err := q.CountChargeItems(ctx, foliosdb.CountChargeItemsParams{PropertyID: propertyID, FolioID: f.ID})
	if err != nil {
		return StayFolio{}, err
	}
	if n > 0 {
		return StayFolio{}, apperr.Conflict("CHECK_IN_HAS_CHARGES", "charges are posted to the stay's folio: the check-in cannot be reversed")
	}
	f, err = q.UnlinkFolioFromStay(ctx, foliosdb.UnlinkFolioFromStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: f.ID, ActorID: p.ActorID()})
	if err != nil {
		return StayFolio{}, err
	}
	return s.stayFolio(ctx, propertyID, f)
}

// StayFolios lists the folios of a stay with their balances.
func (s *Service) StayFolios(ctx context.Context, tenantID, propertyID, stayID int64) ([]StayFolio, error) {
	q := s.q(ctx)
	f, err := q.GetFolioOfStay(ctx, foliosdb.GetFolioOfStayParams{TenantID: tenantID, PropertyID: propertyID, StayID: &stayID})
	if errors.Is(err, pgx.ErrNoRows) {
		return []StayFolio{}, nil
	}
	if err != nil {
		return nil, err
	}
	sf, err := s.stayFolio(ctx, propertyID, f)
	return []StayFolio{sf}, err
}

// ClosedFolio is a folio closed by a check-out.
type ClosedFolio struct {
	ID          int64  `json:"id"`
	FolioNumber string `json:"folio_number"`
	Status      string `json:"status"`
}

// CloseStayFolios closes every OPEN folio of a stay at check-out (folio policy of the MVP: each balance is
// exactly zero). A balance that is not zero is 409 FOLIO_NOT_BALANCED with the balances in the context, and
// nothing is closed. The folios are locked in id order (L4) before their balances are read.
func (s *Service) CloseStayFolios(ctx context.Context, p auth.Principal, propertyID, stayID int64) ([]ClosedFolio, error) {
	q := s.q(ctx)
	open, err := q.ListStayOpenFolios(ctx, foliosdb.ListStayOpenFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(open))
	for i, f := range open {
		ids[i] = f.ID
	}
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil {
		return nil, mapNotFound(err, errFolioNotFound())
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var unbalanced []map[string]any
	for _, f := range open {
		bal, _, err := s.balanceOf(ctx, propertyID, f.ID)
		if err != nil {
			return nil, err
		}
		if !bal.IsZero() {
			unbalanced = append(unbalanced, map[string]any{"folio_id": f.ID, "folio_number": f.FolioNumber, "balance": fixed(bal, decimals)})
		}
	}
	if len(unbalanced) > 0 {
		return nil, apperr.Conflict("FOLIO_NOT_BALANCED", "every folio of the stay must have a zero balance to check out").WithContext("folios", unbalanced)
	}
	now := s.clock.Now()
	out := make([]ClosedFolio, 0, len(open))
	for _, f := range open {
		c, err := q.CloseFolio(ctx, foliosdb.CloseFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: f.ID, Now: now, ActorID: p.ActorID()})
		if err != nil {
			return nil, err
		}
		out = append(out, ClosedFolio{ID: c.ID, FolioNumber: c.FolioNumber, Status: c.Status})
	}
	return out, nil
}
