package folios

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The stay folio hooks used by check-in and reverse check-in. They join the caller's transaction and need no
// permission of their own: the front desk use case was authorized.

// StayFolio is a folio as reported with a stay.
type StayFolio struct {
	ID          int64  `json:"id"`
	FolioNumber string `json:"folio_number"`
	// FolioType is GUEST, or COMPANY for a folio billed to a company (BillToCompanyID).
	FolioType       string `json:"folio_type"`
	BillToCompanyID *int64 `json:"bill_to_company_id"`
	Status          string `json:"status"`
	Balance         string `json:"balance"`
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
// linked to the stay; without one (lockedID 0) a new folio is created, numbered from the sequence. It also opens the company folios the instructions of the line name.
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
	// the company folios the line's billing instructions need are opened now, with the guest folio, before any posting run takes its locks
	if _, err := s.ensureCompanyFolios(ctx, p, propertyID, reservationID, stayID); err != nil {
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
	return StayFolio{ID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, BillToCompanyID: f.BillToCompanyID, Status: f.Status, Balance: fixed(balance, decimals)}, err
}

// DetachStayFolio ends the folios of a stay whose check-in is reversed (audit F-06). A stay can be cancelled only through it, and a cancelled stay has no OPEN folio (migration 00064).
//
//   - Every folio of the stay is locked first, FOR UPDATE in ascending id (L4), and only then are the charges and the balances read: a payment or a charge that is posted at the same
//     moment either wins, and the reversal sees it and refuses, or loses, and finds the folio closed (FOLIO_CLOSED).
//   - No folio of the stay may hold a CHARGE item, on any folio, reversed or not: once a night is charged the check-in cannot be undone (409 CHECK_IN_HAS_CHARGES).
//   - An OPEN company folio must have a zero balance (409 CHECK_IN_HAS_PAYMENTS, with the folios and their balances). Nothing is voided, refunded or moved for the caller: the money is
//     dealt with by the payment rules that exist (a void on the day, or a refund), and the check-in is reversed after that.
//   - The guest folio is unlinked from the stay and is again the deposit folio of the reservation; a payment on it stays there (a deposit). It is the only folio that can be unlinked,
//     because a reservation has one open folio without a stay.
//   - An OPEN company folio with a zero balance is CLOSED, in this transaction, and stays linked to the cancelled stay as history. A company folio that is CLOSED already is left alone,
//     never reopened and never closed again.
//
// bd is the business date of the audit entries. Lock order: the reservation and the stay first.
func (s *Service) DetachStayFolio(ctx context.Context, p auth.Principal, propertyID, stayID int64, bd civil.Date) (StayFolio, []ClosedFolio, error) {
	q := s.q(ctx)
	listed, err := q.ListStayFolios(ctx, foliosdb.ListStayFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return StayFolio{}, nil, err
	}
	if len(listed) == 0 {
		return StayFolio{}, nil, errFolioNotFound()
	}
	ids := make([]int64, len(listed))
	for i, f := range listed {
		ids[i] = f.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil { // L4, ascending, all of them before anything is read
		return StayFolio{}, nil, mapNotFound(err, errFolioNotFound())
	}
	folios, err := q.ListStayFolios(ctx, foliosdb.ListStayFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: &stayID}) // the state after the locks
	if err != nil {
		return StayFolio{}, nil, err
	}
	n, err := q.CountStayChargeItems(ctx, foliosdb.CountStayChargeItemsParams{PropertyID: propertyID, StayID: &stayID}) // on any folio of the stay
	if err != nil {
		return StayFolio{}, nil, err
	}
	if n > 0 {
		return StayFolio{}, nil, apperr.Conflict("CHECK_IN_HAS_CHARGES", "charges are posted to a folio of the stay: the check-in cannot be reversed")
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return StayFolio{}, nil, err
	}
	var guest *foliosdb.Folio
	var toClose []foliosdb.Folio
	var held []map[string]any
	for i := range folios {
		f := folios[i]
		switch {
		case f.FolioType == "GUEST":
			guest = &folios[i]
		case f.Status != "OPEN":
			// a company folio that is closed already is history
		default:
			bal, _, err := s.balanceOf(ctx, propertyID, f.ID)
			if err != nil {
				return StayFolio{}, nil, err
			}
			if !bal.IsZero() {
				held = append(held, map[string]any{"folio_id": f.ID, "folio_number": f.FolioNumber, "folio_type": f.FolioType, "balance": fixed(bal, decimals)})
				continue
			}
			toClose = append(toClose, f)
		}
	}
	if len(held) > 0 {
		return StayFolio{}, nil, apperr.Conflict("CHECK_IN_HAS_PAYMENTS", "a company folio of the stay holds a payment: void or refund it first, then reverse the check-in").WithContext("folios", held)
	}
	if guest == nil {
		return StayFolio{}, nil, errFolioNotFound()
	}
	unlinked, err := q.UnlinkFolioFromStay(ctx, foliosdb.UnlinkFolioFromStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: guest.ID, ActorID: p.ActorID()})
	if err != nil {
		return StayFolio{}, nil, err
	}
	now := s.clock.Now()
	closed := make([]ClosedFolio, 0, len(toClose))
	for _, f := range toClose {
		c, err := q.CloseFolio(ctx, foliosdb.CloseFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: f.ID, Now: now, ActorID: p.ActorID()})
		if err != nil {
			return StayFolio{}, nil, err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "folio.closed", "folio", c.ID,
			map[string]any{"status": f.Status}, map[string]any{"status": c.Status, "reason": "check-in reversed", "stay_id": stayID, "folio_type": c.FolioType})); err != nil {
			return StayFolio{}, nil, err
		}
		closed = append(closed, ClosedFolio{ID: c.ID, FolioNumber: c.FolioNumber, Status: c.Status})
	}
	sf, err := s.stayFolio(ctx, propertyID, unlinked)
	return sf, closed, err
}

// StayFolios lists the folios of a stay with their balances: the guest folio first, then those billed to a company.
func (s *Service) StayFolios(ctx context.Context, tenantID, propertyID, stayID int64) ([]StayFolio, error) {
	rows, err := s.q(ctx).ListStayFolios(ctx, foliosdb.ListStayFoliosParams{TenantID: tenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return nil, err
	}
	out := make([]StayFolio, 0, len(rows))
	for _, f := range rows {
		sf, err := s.stayFolio(ctx, propertyID, f)
		if err != nil {
			return nil, err
		}
		out = append(out, sf)
	}
	return out, nil
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
