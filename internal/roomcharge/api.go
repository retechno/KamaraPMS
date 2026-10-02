package roomcharge

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/expected"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// authorize lets a caller through with nightaudit.run or folio.post_charge at the property. A missing property
// is 404 whichever way.
func (s *Service) authorize(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	err = s.authz.Require(ctx, propertyID, auth.PermNightAuditRun)
	if apperr.IsCode(err, "PERMISSION_DENIED") {
		if err2 := s.authz.Require(ctx, propertyID, auth.PermFolioPostCharge); err2 == nil {
			return p, nil
		}
	}
	return p, err
}

// Totals of a preview.
type Totals struct {
	ReadyCount int    `json:"ready_count"`
	ReadyTotal string `json:"ready_total"`
}

// Preview is the room charge preview: what a posting run would post now. It modifies nothing.
type Preview struct {
	BusinessDate civil.Date `json:"business_date"`
	Items        []Result   `json:"items"`
	Totals       Totals     `json:"totals"`
}

// Preview runs the posting service as a dry run (nightaudit.run or folio.post_charge).
func (s *Service) Preview(ctx context.Context, propertyID int64, bd civil.Date, stayIDs []int64) (Preview, error) {
	p, err := s.authorize(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	rep, err := s.Post(ctx, p, propertyID, PostCmd{BusinessDate: bd, StayIDs: stayIDs, Trigger: TriggerManual, DryRun: true})
	if err != nil {
		return Preview{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{BusinessDate: bd, Items: rep.Items}
	if out.Items == nil {
		out.Items = []Result{}
	}
	sum := decimal.Zero
	for _, r := range rep.Items {
		if r.Status == expected.StatusReady {
			out.Totals.ReadyCount++
			t, _ := decimal.NewFromString(r.Total)
			sum = sum.Add(t)
		}
	}
	out.Totals.ReadyTotal = fixed(sum, decimals)
	return out, nil
}

// PostItem is one night of a posting answer.
type PostItem struct {
	StayID      int64      `json:"stay_id"`
	ServiceDate civil.Date `json:"service_date"`
	Status      string     `json:"status"`
	FolioItemID *int64     `json:"folio_item_id,omitempty"`
	Total       string     `json:"total,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// PostResponse is the answer of a manual posting.
type PostResponse struct {
	Results      []PostItem   `json:"results"`
	Revalidation Revalidation `json:"revalidation"`
}

// PostManual posts the missing and due room charges (nightaudit.run or folio.post_charge): the trigger is MANUAL.
// It is idempotent: a second run answers ALREADY_POSTED.
func (s *Service) PostManual(ctx context.Context, propertyID int64, bd civil.Date, stayIDs []int64) (PostResponse, error) {
	p, err := s.authorize(ctx, propertyID)
	if err != nil {
		return PostResponse{}, err
	}
	rep, err := s.Post(ctx, p, propertyID, PostCmd{BusinessDate: bd, StayIDs: stayIDs, Trigger: TriggerManual})
	if err != nil {
		return PostResponse{}, err
	}
	out := PostResponse{Results: make([]PostItem, 0, len(rep.Items)), Revalidation: rep.Revalidation}
	for _, r := range rep.Items {
		it := PostItem{StayID: r.StayID, ServiceDate: r.ServiceDate, Status: r.Status, FolioItemID: r.FolioItemID, Reason: r.Reason}
		if r.Status == StatusPosted {
			it.Total = r.Total
		}
		out.Results = append(out.Results, it)
	}
	return out, nil
}
