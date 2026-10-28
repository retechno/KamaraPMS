package assets

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/civil"
)

// AuditDifference is a line of a stock-take that is not "found", with the book value at risk and what became of its proposal.
type AuditDifference struct {
	AuditID              int64           `json:"audit_id"`
	AuditNumber          string          `json:"audit_number"`
	AuditStatus          string          `json:"audit_status"`
	StartedAt            time.Time       `json:"started_at"`
	ClosedAt             *time.Time      `json:"closed_at"`
	AssetID              int64           `json:"asset_id"`
	AssetNumber          string          `json:"asset_number"`
	AssetName            string          `json:"asset_name"`
	CategoryCode         string          `json:"category_code"`
	Result               string          `json:"result"`
	ExpectedLocationName *string         `json:"expected_location_name"`
	FoundLocationName    *string         `json:"found_location_name"`
	Note                 *string         `json:"note"`
	CarryingValue        decimal.Decimal `json:"carrying_value"`
	AssetStatus          string          `json:"asset_status"`
	ProposalID           *int64          `json:"proposal_id"`
	ProposalKind         *string         `json:"proposal_kind"`
	ProposalStatus       *string         `json:"proposal_status"`
}

// AuditDifferences is the report of the differences of the stock-takes (asset.read): the filters narrow to one stock-take, one result, or the dates the stock-takes were started.
func (s *Service) AuditDifferences(ctx context.Context, propertyID int64, auditID *int64, result string, from, to *civil.Date) ([]AuditDifference, error) {
	p, err := s.requireRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var res *string
	if result = strings.ToUpper(trimmed(result)); result != "" {
		switch result {
		case ScanMoved, ScanUnexpected, ScanDamaged, ScanMissing:
			res = &result
		default:
			return nil, invalid("filter", fieldErr("result", "INVALID_VALUE", "MOVED, UNEXPECTED, DAMAGED or MISSING"))
		}
	}
	rows, err := s.q(ctx).AuditDifferences(ctx, assetsdb.AuditDifferencesParams{TenantID: p.TenantID, PropertyID: propertyID, AuditID: auditID, Result: res, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]AuditDifference, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditDifference{AuditID: r.AuditID, AuditNumber: r.AuditNumber, AuditStatus: r.AuditStatus, StartedAt: r.StartedAt, ClosedAt: r.ClosedAt, AssetID: r.AssetID,
			AssetNumber: r.AssetNumber, AssetName: r.AssetName, CategoryCode: r.CategoryCode, Result: r.Result, ExpectedLocationName: r.ExpectedLocationName,
			FoundLocationName: r.FoundLocationName, Note: r.Note, CarryingValue: r.CarryingValue, AssetStatus: r.AssetStatus, ProposalID: r.ProposalID, ProposalKind: r.ProposalKind,
			ProposalStatus: r.ProposalStatus})
	}
	return out, nil
}

// AssetRepair is a maintenance request that names the asset: no money, a request has none (Q17).
type AssetRepair struct {
	ID             int64      `json:"id"`
	RequestNumber  string     `json:"request_number"`
	BusinessDate   civil.Date `json:"business_date"`
	Category       string     `json:"category"`
	Priority       string     `json:"priority"`
	Status         string     `json:"status"`
	Description    string     `json:"description"`
	ResolutionNote *string    `json:"resolution_note"`
	ReportedAt     time.Time  `json:"reported_at"`
	ClosedAt       *time.Time `json:"closed_at"`
	RoomNumber     *string    `json:"room_number"`
	Location       *string    `json:"location"`
}

// AssetRepairs is the Repairs tab of the asset page (asset.read): the requests that name the asset, the latest first.
func (s *Service) AssetRepairs(ctx context.Context, propertyID, assetID int64) ([]AssetRepair, error) {
	p, err := s.requireRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := s.q(ctx).GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID}); err != nil {
		return nil, orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
	}
	rows, err := s.q(ctx).ListMaintenanceOfAsset(ctx, assetsdb.ListMaintenanceOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: &assetID})
	if err != nil {
		return nil, err
	}
	out := make([]AssetRepair, 0, len(rows))
	for _, r := range rows {
		out = append(out, AssetRepair{ID: r.ID, RequestNumber: r.RequestNumber, BusinessDate: r.BusinessDate, Category: r.Category, Priority: r.Priority, Status: r.Status,
			Description: r.Description, ResolutionNote: r.ResolutionNote, ReportedAt: r.ReportedAt, ClosedAt: r.ClosedAt, RoomNumber: r.RoomNumber, Location: r.Location})
	}
	return out, nil
}
