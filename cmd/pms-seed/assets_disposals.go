package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The demo of the disposals (docs/architecture/37-fixed-assets.md, PR 3): the pieces of a batch of operating equipment that were spoiled (a partial write-off, posted at once while the approval rule
// of the property is still FREE) and the sale of an old server that waits for an approver. The sale waits because the seed then sets the rule of ASSET_DISPOSAL to DISTINCT: a sale made by the
// person who asks is not enough. A tenant administrator is accepted as their own approver whatever the rule says, so the request is made by a clerk of the demo (a user without the administrator
// right who holds asset.dispose at the property, with a random password that nobody knows: nobody signs in as them). The rule stays set, so the screen of the disposals shows what a property that
// wants a second person looks like, and the tenant administrator approves or rejects the request.
const (
	demoDisposalBatchAsset   = "Linen kamar (batch)"
	demoDisposalBatchUnits   = 50
	demoDisposalBatchNote    = "Linen rusak karena gudang kebanjiran (demo pelepasan)"
	demoDisposalBatchKey     = "pms-seed-assets-disposal-batch"
	demoDisposalPendingAsset = "Server reservasi"
	demoDisposalPendingPrice = 5_000_000
	demoDisposalPendingNote  = "Server lama dijual ke vendor (demo pelepasan)"
	demoDisposalPendingKey   = "pms-seed-assets-disposal-pending"
	demoClerkEmail           = "staf.aset@demo.test"
	demoClerkRole            = "Staf aset (demo)"
)

// disposalRows is how many disposals the demo makes: the partial write-off of the batch and the sale that waits for approval.
const disposalRows = 2

// seedDisposals makes the two disposals of the demo, on the business date. Safe to run again: each is found again by its asset (an asset that was disposed of or has a request is left alone).
func seedDisposals(ctx context.Context, svc *assets.Service, acct *accounting.Service, d *seedDeps, pid int64, businessDate civil.Date, dryRun bool, out io.Writer) (int, error) {
	if d == nil || d.rules == nil || d.days == nil || d.iam == nil {
		return 0, nil
	}
	list, err := svc.ListAssets(ctx, pid, assets.AssetFilter{Limit: 100})
	if err != nil {
		return 0, err
	}
	byName := map[string]assets.Asset{}
	for _, a := range list.Data {
		byName[a.Name] = a
	}
	batch, hasBatch := byName[demoDisposalBatchAsset]
	server, hasServer := byName[demoDisposalPendingAsset]
	if !hasBatch || !hasServer {
		// the register of the demo is not there yet (a dry run before the import): say what the real run does
		if dryRun {
			_, _ = fmt.Fprintf(out, "disposals: %d (%d pieces of %q written off, the sale of %q waiting for approval)\n", disposalRows, demoDisposalBatchUnits, demoDisposalBatchAsset, demoDisposalPendingAsset)
			return disposalRows, nil
		}
		_, _ = fmt.Fprintln(out, "disposals: skipped, the demo assets are not in the register")
		return 0, nil
	}
	todo := 0
	batchDone, err := hasDisposalOrRequest(ctx, svc, pid, batch.ID)
	if err != nil {
		return 0, err
	}
	serverDone, err := hasDisposalOrRequest(ctx, svc, pid, server.ID)
	if err != nil {
		return 0, err
	}
	if !batchDone {
		todo++
	}
	if !serverDone {
		todo++
	}
	if todo == 0 {
		_, _ = fmt.Fprintln(out, "disposals: the demo disposals exist")
		return 0, nil
	}
	_, _ = fmt.Fprintf(out, "disposals: %d to make, dated %s\n", todo, businessDate)
	if dryRun {
		return todo, nil
	}
	if cfg, err := svc.GetSettings(ctx, pid); err != nil {
		return 0, err
	} else if cfg.RegisterFrom == nil || cfg.RegisterFrom.After(businessDate) {
		_, _ = fmt.Fprintln(out, "disposals: skipped, the register starts after the business date")
		return 0, nil
	}
	made := 0
	if !batchDone {
		units := int32(demoDisposalBatchUnits)
		res, err := svc.Dispose(ctx, pid, batch.ID, assets.DisposeInput{Kind: assets.DisposalWriteOff, ReasonCode: assets.ReasonDamaged, Units: &units, Note: demoDisposalBatchNote, IdempotencyKey: demoDisposalBatchKey})
		if err != nil {
			_, _ = fmt.Fprintf(out, "disposals: skipped, %v\n", err)
			return 0, nil
		}
		_, _ = fmt.Fprintf(out, "disposals: %d pieces of the batch written off (%s)\n", units, res.Status)
		made++
	}
	if !serverDone {
		prop, err := d.days.GetProperty(ctx, pid)
		if err != nil {
			return made, err
		}
		if err := d.rules.Set(ctx, pid, prop.CurrencyDecimals, []iam.ApprovalRuleInput{{Kind: string(iam.KindAssetDisposal), Rule: iam.RuleDistinct}}); err != nil {
			return made, fmt.Errorf("approval rule: %w", err)
		}
		accs, err := acct.Accounts(ctx, pid, accounting.AccountFilter{Query: "1130"})
		if err != nil {
			return made, err
		}
		var bank int64
		for _, a := range accs {
			if a.Code == "1130" {
				bank = a.ID
			}
		}
		if bank == 0 {
			_, _ = fmt.Fprintln(out, "disposals: the sale is skipped, the account 1130 is missing")
			return made, nil
		}
		clerk, err := clerkContext(ctx, d, pid)
		if err != nil {
			return made, fmt.Errorf("clerk: %w", err)
		}
		price := decimal.NewFromInt(demoDisposalPendingPrice)
		res, err := svc.Dispose(clerk, pid, server.ID, assets.DisposeInput{Kind: assets.DisposalSale, ReasonCode: assets.ReasonSold, Proceeds: &price, ProceedsAccountID: &bank, Note: demoDisposalPendingNote, IdempotencyKey: demoDisposalPendingKey})
		if err != nil {
			_, _ = fmt.Fprintf(out, "disposals: the sale is skipped, %v\n", err)
			return made, nil
		}
		_, _ = fmt.Fprintf(out, "disposals: the sale of %s is %s\n", server.Number, res.Status)
		made++
	}
	return made, nil
}

// hasDisposalOrRequest says whether the asset was disposed of (even if reversed) or has a request, decided or not.
func hasDisposalOrRequest(ctx context.Context, svc *assets.Service, pid, assetID int64) (bool, error) {
	ds, err := svc.ListDisposals(ctx, pid, &assetID)
	if err != nil {
		return false, err
	}
	if len(ds) > 0 {
		return true, nil
	}
	rs, err := svc.ListDisposalRequests(ctx, pid, "")
	if err != nil {
		return false, err
	}
	for _, r := range rs {
		if r.AssetID == assetID {
			return true, nil
		}
	}
	return false, nil
}

// clerkContext is the context of the clerk of the demo, made on the first call: a role with asset.dispose and asset.read, a user with that role at the property and a random password.
func clerkContext(ctx context.Context, d *seedDeps, pid int64) (context.Context, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	users, err := d.iam.ListUsers(ctx, 0, 500)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Email == demoClerkEmail {
			return auth.WithPrincipal(ctx, auth.Principal{TenantID: p.TenantID, UserID: u.ID}), nil
		}
	}
	var roleID int64
	roles, err := d.iam.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		if r.Name == demoClerkRole {
			roleID = r.ID
		}
	}
	if roleID == 0 {
		name, desc := demoClerkRole, "Mengajukan pelepasan aset; persetujuan oleh orang lain (data demo)"
		perms := []auth.Permission{auth.PermAssetDispose, auth.PermAssetRead}
		r, err := d.iam.CreateRole(ctx, iam.RoleInput{Name: &name, Description: &desc, Permissions: &perms})
		if err != nil {
			return nil, err
		}
		roleID = r.ID
	}
	secret := hex.EncodeToString(randomSecret()) // 64 characters, never shown
	u, err := d.iam.CreateUser(ctx, iam.CreateUserInput{Email: demoClerkEmail, FullName: "Staf Aset (demo)", Password: secret, Grants: []iam.GrantInput{{PropertyID: pid, RoleID: roleID}}})
	if err != nil {
		return nil, err
	}
	return auth.WithPrincipal(ctx, auth.Principal{TenantID: p.TenantID, UserID: u.ID}), nil
}
