package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/entitlement"
	"kamarapms/internal/subscription"
)

const subscriptionUsage = `
subscriptions (docs/architecture/26-subscriptions.md):
  pms-admin list-plans
  pms-admin create-plan -code CODE -name NAME [-modules HOUSEKEEPING,ACCOUNTING] [-price 1500000] [-currency IDR]
                        [-period-months 1|3|6|12] [-max-properties N] [-max-rooms N] [-max-users N] [-trial-days N] [-default] [-inactive]
  pms-admin update-plan -code CODE [any flag of create-plan; a limit of "unlimited" removes it]
  pms-admin show-subscription -tenant CODE
  pms-admin subscribe -tenant CODE [-plan CODE] [-paid-through YYYY-MM-DD|none] [-grace-days N] [-status TRIAL|ACTIVE]
  pms-admin set-limits -tenant CODE [-max-properties N|plan] [-max-rooms N|plan] [-max-users N|plan]
  pms-admin cancel-subscription -tenant CODE
  pms-admin issue-invoice -tenant CODE [-plan CODE] [-periods N] [-amount X] [-due YYYY-MM-DD] [-notes TEXT]
  pms-admin pay-invoice -number SUB-2026-00001 -paid-on YYYY-MM-DD [-reference REF]
  pms-admin void-invoice -number SUB-2026-00001 -reason TEXT
  pms-admin list-invoices -tenant CODE`

var subscriptionCommands = map[string]bool{
	"list-plans": true, "create-plan": true, "update-plan": true, "show-subscription": true, "subscribe": true,
	"set-limits": true, "cancel-subscription": true, "issue-invoice": true, "pay-invoice": true, "void-invoice": true,
	"list-invoices": true,
}

// subFlags are the flags of every subscription command (one set, as for the tenant commands).
type subFlags struct {
	code, name, modules, price, currency, periodMonths, maxProperties, maxRooms, maxUsers, trialDays *string
	isDefault, inactive                                                                              *bool
	tenant, plan, paidThrough, graceDays, status                                                     *string
	periods, amount, due, notes, number, paidOn, reference, reason                                   *string
	set                                                                                              map[string]bool
}

func parseSubFlags(cmd string, args []string) (*subFlags, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	f := &subFlags{
		code: fs.String("code", "", "plan code"), name: fs.String("name", "", "plan name"),
		modules:       fs.String("modules", "", "optional modules, comma separated: HOUSEKEEPING, ACCOUNTING, INVENTORY, BANQUET, POS (none: CORE only)"),
		price:         fs.String("price", "0", "price per billing period"),
		currency:      fs.String("currency", "IDR", "ISO 4217 currency of the price"),
		periodMonths:  fs.String("period-months", "1", "billing period in months: 1, 3, 6 or 12"),
		maxProperties: fs.String("max-properties", "", "active properties: a number, unlimited, or plan (set-limits)"),
		maxRooms:      fs.String("max-rooms", "", "active rooms: a number, unlimited, or plan (set-limits)"),
		maxUsers:      fs.String("max-users", "", "active users: a number, unlimited, or plan (set-limits)"),
		trialDays:     fs.String("trial-days", "0", "trial length of a new tenant on this plan"),
		isDefault:     fs.Bool("default", false, "the plan of new tenants"),
		inactive:      fs.Bool("inactive", false, "retired: no longer given to tenants"),
		tenant:        fs.String("tenant", "", "tenant code"), plan: fs.String("plan", "", "plan code"),
		paidThrough: fs.String("paid-through", "", "last paid day (YYYY-MM-DD) or none"),
		graceDays:   fs.String("grace-days", "", "days of grace after the paid period (0 to 90)"),
		status:      fs.String("status", "", "TRIAL or ACTIVE"),
		periods:     fs.String("periods", "1", "billing periods on the invoice"),
		amount:      fs.String("amount", "", "invoice amount instead of periods x price"),
		due:         fs.String("due", "", "due date (YYYY-MM-DD)"), notes: fs.String("notes", "", "invoice notes"),
		number:    fs.String("number", "", "invoice number"),
		paidOn:    fs.String("paid-on", "", "payment date (YYYY-MM-DD)"),
		reference: fs.String("reference", "", "payment reference (bank transfer, ...)"),
		reason:    fs.String("reason", "", "why the invoice is voided"),
		set:       map[string]bool{},
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	return f, nil
}

func runSubscription(cmd string, args []string) error {
	f, err := parseSubFlags(cmd, args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	txm, closeDB, err := connect(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	svc := subscription.NewService(txm, clock.System{}, audit.NewWriter(clock.System{}))

	switch cmd {
	case "list-plans":
		plans, err := svc.ListPlans(ctx)
		if err != nil {
			return err
		}
		printPlans(plans)
	case "create-plan":
		in, err := planInput(f, subscription.PlanInput{Price: decimal.Zero, CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true})
		if err != nil {
			return err
		}
		p, err := svc.CreatePlan(ctx, in)
		if err != nil {
			return err
		}
		printPlans([]subscription.Plan{p})
	case "update-plan":
		cur, err := svc.PlanByCode(ctx, *f.code)
		if err != nil {
			return err
		}
		in, err := planInput(f, subscription.PlanInput{Code: cur.Code, Name: cur.Name, Modules: cur.Modules, Limits: cur.Limits, Price: cur.Price,
			CurrencyCode: cur.CurrencyCode, BillingPeriodMonths: cur.BillingPeriodMonths, TrialDays: cur.TrialDays, IsActive: cur.IsActive, IsDefault: cur.IsDefault})
		if err != nil {
			return err
		}
		p, err := svc.UpdatePlan(ctx, in)
		if err != nil {
			return err
		}
		printPlans([]subscription.Plan{p})
	case "show-subscription":
		return show(ctx, svc, *f.tenant)
	case "subscribe", "set-limits", "cancel-subscription":
		in, err := changeInput(cmd, f)
		if err != nil {
			return err
		}
		if _, err := svc.Change(ctx, *f.tenant, in); err != nil {
			return err
		}
		return show(ctx, svc, *f.tenant)
	case "issue-invoice":
		in := subscription.IssueInput{TenantCode: *f.tenant, PlanCode: *f.plan, Notes: *f.notes}
		if in.Periods, err = strconv.Atoi(*f.periods); err != nil {
			return fmt.Errorf("-periods: %w", err)
		}
		if *f.amount != "" {
			a, err := decimal.NewFromString(*f.amount)
			if err != nil {
				return fmt.Errorf("-amount: %w", err)
			}
			in.Amount = &a
		}
		if *f.due != "" {
			d, err := civil.ParseDate(*f.due)
			if err != nil {
				return fmt.Errorf("-due: %w", err)
			}
			in.DueOn = &d
		}
		inv, err := svc.IssueInvoice(ctx, in)
		if err != nil {
			return err
		}
		printInvoices([]subscription.Invoice{inv})
	case "pay-invoice":
		d, err := civil.ParseDate(*f.paidOn)
		if err != nil {
			return fmt.Errorf("-paid-on: %w", err)
		}
		inv, sub, err := svc.PayInvoice(ctx, *f.number, d, *f.reference)
		if err != nil {
			return err
		}
		printInvoices([]subscription.Invoice{inv})
		fmt.Printf("subscription: %s, paid through %s\n", sub.Status, dateOr(sub.PaidThrough, "no end"))
	case "void-invoice":
		inv, err := svc.VoidInvoice(ctx, *f.number, *f.reason)
		if err != nil {
			return err
		}
		printInvoices([]subscription.Invoice{inv})
	case "list-invoices":
		inv, err := svc.TenantInvoices(ctx, *f.tenant)
		if err != nil {
			return err
		}
		printInvoices(inv)
	}
	return nil
}

// planInput applies the flags that were given to a starting plan.
func planInput(f *subFlags, in subscription.PlanInput) (subscription.PlanInput, error) {
	var err error
	if f.set["code"] || in.Code == "" {
		in.Code = *f.code
	}
	if f.set["name"] {
		in.Name = *f.name
	}
	if f.set["modules"] {
		in.Modules = nil
		for m := range strings.SplitSeq(*f.modules, ",") {
			if m = strings.TrimSpace(m); m != "" {
				in.Modules = append(in.Modules, subscription.Module(strings.ToUpper(m)))
			}
		}
	}
	if f.set["price"] {
		if in.Price, err = decimal.NewFromString(*f.price); err != nil {
			return in, fmt.Errorf("-price: %w", err)
		}
	}
	if f.set["currency"] {
		in.CurrencyCode = *f.currency
	}
	if f.set["period-months"] {
		if in.BillingPeriodMonths, err = strconv.Atoi(*f.periodMonths); err != nil {
			return in, fmt.Errorf("-period-months: %w", err)
		}
	}
	if f.set["trial-days"] {
		if in.TrialDays, err = strconv.Atoi(*f.trialDays); err != nil {
			return in, fmt.Errorf("-trial-days: %w", err)
		}
	}
	for _, l := range []struct {
		flag string
		dst  **int
	}{{"max-properties", &in.Limits.MaxProperties}, {"max-rooms", &in.Limits.MaxRooms}, {"max-users", &in.Limits.MaxUsers}} {
		if !f.set[l.flag] {
			continue
		}
		v := map[string]*string{"max-properties": f.maxProperties, "max-rooms": f.maxRooms, "max-users": f.maxUsers}[l.flag]
		if strings.EqualFold(*v, "unlimited") {
			*l.dst = nil
			continue
		}
		n, err := strconv.Atoi(*v)
		if err != nil {
			return in, fmt.Errorf("-%s: a number or unlimited", l.flag)
		}
		*l.dst = &n
	}
	if f.set["default"] {
		in.IsDefault = *f.isDefault
	}
	if f.set["inactive"] {
		in.IsActive = !*f.inactive
	}
	return in, nil
}

// changeInput turns the flags of subscribe, set-limits and cancel-subscription into a change.
func changeInput(cmd string, f *subFlags) (subscription.ChangeInput, error) {
	var in subscription.ChangeInput
	switch cmd {
	case "cancel-subscription":
		st := subscription.StatusCancelled
		in.Status = &st
		return in, nil
	case "subscribe":
		if f.set["plan"] {
			in.PlanCode = f.plan
		}
		if f.set["paid-through"] {
			if strings.EqualFold(*f.paidThrough, "none") {
				in.NoEnd = true
			} else {
				d, err := civil.ParseDate(*f.paidThrough)
				if err != nil {
					return in, fmt.Errorf("-paid-through: %w", err)
				}
				in.PaidThrough = &d
			}
		}
		if f.set["grace-days"] {
			n, err := strconv.Atoi(*f.graceDays)
			if err != nil {
				return in, fmt.Errorf("-grace-days: %w", err)
			}
			in.GraceDays = &n
		}
		st := subscription.StatusActive
		if f.set["status"] {
			st = strings.ToUpper(*f.status)
		}
		in.Status = &st
	case "set-limits":
		for _, l := range []struct {
			flag string
			v    *string
			r    entitlement.Resource
			dst  **int
		}{{"max-properties", f.maxProperties, entitlement.Properties, &in.Limits.MaxProperties},
			{"max-rooms", f.maxRooms, entitlement.Rooms, &in.Limits.MaxRooms},
			{"max-users", f.maxUsers, entitlement.Users, &in.Limits.MaxUsers}} {
			if !f.set[l.flag] {
				continue
			}
			if strings.EqualFold(*l.v, "plan") {
				in.ClearLimits = append(in.ClearLimits, l.r)
				continue
			}
			n, err := strconv.Atoi(*l.v)
			if err != nil {
				return in, fmt.Errorf("-%s: a number or plan", l.flag)
			}
			*l.dst = &n
		}
	}
	return in, nil
}

func show(ctx context.Context, svc *subscription.Service, tenant string) error {
	v, err := svc.Show(ctx, tenant)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func printPlans(plans []subscription.Plan) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tNAME\tMODULES\tPROPERTIES\tROOMS\tUSERS\tPRICE\tMONTHS\tTRIAL\tACTIVE\tDEFAULT")
	for _, p := range plans {
		ms := make([]string, 0, len(p.Modules))
		for _, m := range p.Modules {
			ms = append(ms, string(m))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s %s\t%d\t%d\t%t\t%t\n", p.Code, p.Name, strings.Join(append([]string{"CORE"}, ms...), ","),
			limit(p.Limits.MaxProperties), limit(p.Limits.MaxRooms), limit(p.Limits.MaxUsers), p.Price, p.CurrencyCode,
			p.BillingPeriodMonths, p.TrialDays, p.IsActive, p.IsDefault)
	}
	_ = w.Flush()
}

func printInvoices(inv []subscription.Invoice) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NUMBER\tPLAN\tPERIOD\tAMOUNT\tSTATUS\tDUE\tPAID ON")
	for _, i := range inv {
		fmt.Fprintf(w, "%s\t%s\t%s to %s\t%s %s\t%s\t%s\t%s\n", i.Number, i.PlanCode, i.PeriodStart, i.PeriodEnd, i.Amount, i.CurrencyCode,
			i.Status, i.DueOn, dateOr(i.PaidOn, "-"))
	}
	_ = w.Flush()
}

func limit(n *int) string {
	if n == nil {
		return "unlimited"
	}
	return strconv.Itoa(*n)
}

func dateOr(d *civil.Date, none string) string {
	if d == nil {
		return none
	}
	return d.String()
}
