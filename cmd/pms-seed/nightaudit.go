package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"kamarapms/internal/auditpack"
)

// pms-seed nightaudit: demo recipients of the night audit pack (docs/architecture/38-reports-night-audit-pack.md, PR 3) for a DEVELOPMENT database, through the service of the application. Addresses are on
// the reserved domain .example, so nothing leaves a development machine by mistake. Safe to run again: an address that is a recipient is left alone.

type napDemoRecipient struct {
	email, name string
	sections    []string
	inactive    bool
}

var napDemoRecipients = []napDemoRecipient{
	{email: "gm@kamarabali.example", name: "General Manager", sections: []string{auditpack.SecSummary, auditpack.SecFlash}},
	{email: "fom@kamarabali.example", name: "Front office manager", sections: []string{
		auditpack.SecSummary, auditpack.SecFlash, auditpack.SecGuestLedger, auditpack.SecCancellations, auditpack.SecInHouse, auditpack.SecNextDay}},
	{email: "finance@kamarabali.example", name: "Finance", sections: []string{
		auditpack.SecSummary, auditpack.SecFlash, auditpack.SecRevenue, auditpack.SecTax, auditpack.SecPayments, auditpack.SecZReports, auditpack.SecGuestLedger, auditpack.SecDepositLedger, auditpack.SecCorrections}},
	{email: "owner@kamaragroup.example", name: "Pemilik", sections: []string{auditpack.SecSummary, auditpack.SecFlash}},
	{email: "chief.eng@kamarabali.example", name: "Engineering", sections: []string{auditpack.SecOutOfOrder}, inactive: true},
}

func seedNightAudit(e *env, dryRun bool) error {
	_, err := seedNightAuditWith(e.ctx, auditpack.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, nil), e.propertyID, dryRun, os.Stdout)
	return err
}

func seedNightAuditWith(ctx context.Context, svc *auditpack.Service, propertyID int64, dryRun bool, out io.Writer) (int, error) {
	list, err := svc.ListRecipients(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, r := range list.Items {
		have[strings.ToLower(r.Email)] = true
	}
	made := 0
	for _, r := range napDemoRecipients {
		if have[r.email] {
			_, _ = fmt.Fprintf(out, "recipient %s exists\n", r.email)
			continue
		}
		made++
		if dryRun {
			_, _ = fmt.Fprintf(out, "recipient %s: would create\n", r.email)
			continue
		}
		active := !r.inactive
		if _, err := svc.CreateRecipient(ctx, propertyID, auditpack.RecipientInput{Email: r.email, Name: r.name, Sections: r.sections, IsActive: &active}); err != nil {
			return made, fmt.Errorf("recipient %s: %w", r.email, err)
		}
		_, _ = fmt.Fprintf(out, "recipient %s: created\n", r.email)
	}
	return made, nil
}
