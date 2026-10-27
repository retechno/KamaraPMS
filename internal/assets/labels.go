package assets

import (
	"context"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
)

// Label is what one QR label prints: the code in the QR (only the code, never a value or a URL, Q12) and the words around it.
type Label struct {
	Code     string // "FA:<label_code>" for an asset, "FL:<label_code>" for a place
	Title    string // the number of the asset, or the code of the place
	Name     string
	Subtitle string // the place of an asset, or the kind of a place
}

// LabelFilter picks the labels: Places true prints the labels of places, otherwise those of assets. IDs names assets (or places); LocationID and CategoryID narrow assets.
type LabelFilter struct {
	Places     bool
	IDs        []int64
	LocationID *int64
	CategoryID *int64
}

const maxLabels = 500

// Labels answers the labels to print (any asset permission): the active assets, or the active places.
func (s *Service) Labels(ctx context.Context, propertyID int64, f LabelFilter) ([]Label, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	if len(f.IDs) > maxLabels {
		return nil, apperr.Invalid("too many labels", fieldErr("ids", "TOO_MANY", "500 labels at most"))
	}
	if f.IDs == nil {
		f.IDs = []int64{} // an empty array, not NULL: cardinality(NULL) is not 0
	}
	var out []Label
	if f.Places {
		active := true
		locs, err := s.q(ctx).ListLocations(ctx, assetsdb.ListLocationsParams{TenantID: p.TenantID, PropertyID: propertyID, Active: &active})
		if err != nil {
			return nil, err
		}
		want := map[int64]bool{}
		for _, id := range f.IDs {
			want[id] = true
		}
		for _, l := range locs {
			if len(want) > 0 && !want[l.ID] {
				continue
			}
			out = append(out, Label{Code: "FL:" + l.LabelCode, Title: l.Code, Name: l.Name, Subtitle: l.Kind})
		}
	} else {
		rows, err := s.q(ctx).ListAssetsForLabels(ctx, assetsdb.ListAssetsForLabelsParams{TenantID: p.TenantID, PropertyID: propertyID, LocationID: f.LocationID, CategoryID: f.CategoryID, Ids: f.IDs})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			l := Label{Code: "FA:" + r.LabelCode, Title: r.Number, Name: r.Name, Subtitle: r.CategoryName}
			if r.LocationName != nil {
				l.Subtitle = *r.LocationName
			}
			out = append(out, l)
		}
	}
	if len(out) > maxLabels {
		out = out[:maxLabels]
	}
	if len(out) == 0 {
		return nil, apperr.NotFound("NO_LABELS", "there is nothing to print a label for")
	}
	return out, nil
}
