package auditpack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
)

// What the e-mail worker (internal/notifications) reads to compose a message of a pack. It runs as the tenant, without a request, so nothing here asks for a permission of a user: the row it works on
// was queued by someone who had one.

// MailView is a pack as one message needs it.
type MailView struct {
	Date civil.Date
	// RecipientActive is false when the address is no longer a recipient of the property or was deactivated: the message is dropped (SKIPPED).
	RecipientActive bool
	// OK are the sections of the pack that hold data, in the order of the catalog.
	OK []string
	// Headlines are the figures for the body of the message, empty when the message does not carry them.
	Headlines []string
}

// MailView reads the pack of a queued message and the present state of its recipient. wantFigures says whether the message carries the headline figures (the recipient has the summary or the flash).
func (s *Service) MailView(ctx context.Context, propertyID, packID int64, email string, wantFigures bool) (MailView, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return MailView{}, err
	}
	q := s.q(ctx)
	date, err := q.GetPackDate(ctx, auditpackdb.GetPackDateParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: packID})
	if errors.Is(err, pgx.ErrNoRows) {
		return MailView{}, packNotFound(civil.Date{})
	}
	if err != nil {
		return MailView{}, err
	}
	out := MailView{Date: date}
	r, err := q.GetRecipientByEmail(ctx, auditpackdb.GetRecipientByEmailParams{TenantID: p.TenantID, PropertyID: propertyID, Email: email})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return MailView{}, err
	default:
		out.RecipientActive = r.IsActive
	}
	rows, err := q.ListPackSections(ctx, auditpackdb.ListPackSectionsParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: packID})
	if err != nil {
		return MailView{}, err
	}
	byCode := map[string]auditpackdb.ListPackSectionsRow{}
	for _, row := range rows {
		byCode[row.Section] = row
	}
	for _, def := range Catalog {
		if row, ok := byCode[def.Code]; ok && row.Status == StatusOK {
			out.OK = append(out.OK, def.Code)
		}
	}
	if wantFigures {
		if row, ok := byCode[SecFlash]; ok && row.Status == StatusOK {
			var f reports.DailyFlash
			if err := json.Unmarshal(row.Data, &f); err == nil {
				out.Headlines = headlines(f)
			}
		}
	}
	return out, nil
}

// headlines are the figures a manager reads first: the rooms sold, the occupancy, the average rate, the revenue and the money taken, for the day.
func headlines(f reports.DailyFlash) []string {
	var out []string
	for _, l := range f.Rooms {
		switch l.Key {
		case "rooms_sold", "occupancy_pct", "arr":
			out = append(out, fmt.Sprintf("%s: %s", l.Label, l.Today))
		}
	}
	out = append(out,
		fmt.Sprintf("%s: %s", f.Revenue.TotalNet.Label, f.Revenue.TotalNet.Today),
		fmt.Sprintf("%s: %s", f.Revenue.GTR.Label, f.Revenue.GTR.Today),
		fmt.Sprintf("%s: %s", f.Payments.GTP.Label, f.Payments.GTP.Today))
	return out
}
