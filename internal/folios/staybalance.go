package folios

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios/foliosdb"
)

// StayBalance is what a stay owes. Amount is the sum of the balances of its folios (the guest folio and the company folios), each of which is given in Folios with its own balance;
// Status is SETTLED (zero), OUTSTANDING (above zero) or CREDIT (below zero) for that sum, or NO_FOLIO when the stay has no folio (Amount is then empty: no folio is not a zero balance).
// The in-house list and the night audit report it with this one type and this one computation.
type StayBalance struct {
	Amount string      `json:"amount"`
	Status string      `json:"status"`
	Folios []StayFolio `json:"folios"`
}

// NewStayBalance is the balance of a stay from its folios, each with its own balance already fixed at the currency decimals.
func NewStayBalance(fs []StayFolio, decimals int32) (StayBalance, error) {
	if len(fs) == 0 {
		return StayBalance{Status: "NO_FOLIO", Folios: []StayFolio{}}, nil
	}
	total := decimal.Zero
	for _, f := range fs {
		b, err := decimal.NewFromString(f.Balance)
		if err != nil {
			return StayBalance{}, err
		}
		total = total.Add(b)
	}
	return StayBalance{Amount: total.StringFixed(decimals), Status: balanceStatus(total), Folios: fs}, nil
}

func balanceStatus(d decimal.Decimal) string {
	switch d.Sign() {
	case 1:
		return "OUTSTANDING"
	case -1:
		return "CREDIT"
	}
	return "SETTLED"
}

// StayBalances reads the balances of many stays with two queries, whatever their number: the folios of all the stays, then the ledger totals of all those folios. A stay appears in the map
// even without a folio (NO_FOLIO). A stay of another tenant or property has no folio under this scope, so it is NO_FOLIO as well.
func (s *Service) StayBalances(ctx context.Context, tenantID, propertyID int64, stayIDs []int64) (map[int64]StayBalance, error) {
	out := make(map[int64]StayBalance, len(stayIDs))
	if len(stayIDs) == 0 {
		return out, nil
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	rows, err := q.ListFoliosOfStays(ctx, foliosdb.ListFoliosOfStaysParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	folioIDs := make([]int64, len(rows))
	for i, f := range rows {
		folioIDs[i] = f.ID
	}
	sums, err := q.FolioTotalsOf(ctx, foliosdb.FolioTotalsOfParams{PropertyID: propertyID, FolioIds: folioIDs})
	if err != nil {
		return nil, err
	}
	balanceOf := make(map[int64]decimal.Decimal, len(sums))
	for _, t := range sums {
		balanceOf[t.FolioID] = t.Debit.Sub(t.Credit)
	}
	byStay := make(map[int64][]StayFolio, len(stayIDs))
	for _, f := range rows {
		if f.StayID == nil {
			continue
		}
		byStay[*f.StayID] = append(byStay[*f.StayID], StayFolio{
			ID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, BillToCompanyID: f.BillToCompanyID, Status: f.Status,
			Balance: fixed(balanceOf[f.ID], decimals),
		})
	}
	for _, id := range stayIDs {
		b, err := NewStayBalance(byStay[id], decimals)
		if err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, nil
}
