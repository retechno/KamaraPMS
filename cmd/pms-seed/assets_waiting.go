package main

import (
	"context"
	"fmt"
	"io"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// seedDeps are the services that the demo of the waiting list (docs/architecture/37-fixed-assets.md, PR 2) needs besides the register: the payables for the bill, the transaction manager and the business day
// for the journals that the cashier and the bank would post, and the departments for the assets, and the rules of approval for the disposals.
type seedDeps struct {
	pay   *payables.Service
	txm   *db.TxManager
	days  *tenancy.Service
	depts *departments.Service
	rules *iam.Rules   // the approval rule of the disposals, which the demo of the disposals sets
	iam   *iam.Service // the clerk of the demo, who asks for the disposal that waits for approval
}

const (
	demoWaitingSupplier = "DEMO-WL-SUP"
	demoWaitingInvoice  = "DEMO-WL-001"
	demoWaitingCashier  = "DEMO-WL-CASHIER"
	demoWaitingBank     = "DEMO-WL-BANK"
	demoWaitingManual   = "DEMO-WL-MANUAL"
)

// waitingRows is how many waiting lines the demo makes: two bill lines and one line each of the cashier, the bank and a manual journal.
const waitingRows = 5

// departmentCodes are the codes of the departments of the property that the demo assets may name.
func departmentCodes(ctx context.Context, d *seedDeps, pid int64) (map[string]bool, error) {
	have := map[string]bool{}
	if d == nil || d.depts == nil {
		return have, nil
	}
	list, err := d.depts.List(ctx, pid, departments.Filter{})
	if err != nil {
		return nil, err
	}
	for _, x := range list {
		have[x.Code] = true
	}
	return have, nil
}

// seedWaiting books the cost of assets that nobody registered yet, one per way a cost reaches the ledger: a supplier bill (two lines), a cashier journal, a bank journal and a manual journal. They are dated the
// business date, which is never before the start of the register. Safe to run again: each document is found again by its reference.
func seedWaiting(ctx context.Context, svc *assets.Service, acct *accounting.Service, d *seedDeps, pid int64, businessDate civil.Date, dryRun bool, out io.Writer) (int, error) {
	if d == nil || d.pay == nil {
		return 0, nil
	}
	if bills, err := d.pay.Bills(ctx, pid, payables.BillFilter{Q: demoWaitingInvoice, Limit: 5}); err == nil && len(bills) > 0 {
		_, _ = fmt.Fprintln(out, "waiting list: the demo documents exist")
		return 0, nil
	}
	_, _ = fmt.Fprintf(out, "waiting list: %d lines (bill, cashier, bank, manual), dated %s\n", waitingRows, businessDate)
	if dryRun {
		return waitingRows, nil
	}
	if cfg, err := svc.GetSettings(ctx, pid); err != nil {
		return 0, err
	} else if cfg.RegisterFrom == nil || cfg.RegisterFrom.After(businessDate) {
		// the register starts after today (the books began too late for an opening balance of an earlier month end): a cost booked today would not be the register's to wait for
		_, _ = fmt.Fprintln(out, "waiting list: skipped, the register starts after the business date")
		return 0, nil
	}
	cats, err := svc.ListCategories(ctx, pid)
	if err != nil {
		return 0, err
	}
	cost := map[string]int64{}
	for _, c := range cats {
		cost[c.Code] = c.CostAccountID
	}
	byCode := map[string]int64{}
	for _, code := range []string{"1110", "1130"} {
		list, err := acct.Accounts(ctx, pid, accounting.AccountFilter{Query: code})
		if err != nil {
			return 0, err
		}
		for _, a := range list {
			if a.Code == code {
				byCode[code] = a.ID
			}
		}
	}
	if cost["FFE"] == 0 || cost["IT"] == 0 || byCode["1110"] == 0 || byCode["1130"] == 0 {
		_, _ = fmt.Fprintln(out, "waiting list: skipped, the categories FFE and IT or the accounts 1110 and 1130 are missing")
		return 0, nil
	}
	dec := decimal.NewFromInt

	sup, err := d.pay.CreateSupplier(ctx, pid, payables.SupplierInput{Code: demoWaitingSupplier, Name: "CV Mebel Jaya", City: "Denpasar"})
	if err != nil {
		// the supplier may exist from an earlier run that stopped before the bill
		list, lerr := d.pay.Suppliers(ctx, pid, payables.SupplierFilter{Q: demoWaitingSupplier})
		if lerr != nil || len(list) == 0 {
			return 0, fmt.Errorf("supplier: %w", err)
		}
		sup = list[0]
	}
	if _, err := d.pay.PostBill(ctx, pid, payables.BillInput{SupplierID: sup.ID, SupplierInvoiceNumber: demoWaitingInvoice, BillDate: businessDate, Description: "Kursi dan printer (demo daftar tunggu)", Lines: []payables.BillLineInput{
		{AccountID: cost["FFE"], Description: "Kursi restoran 12 unit", Amount: dec(18_000_000)},
		{AccountID: cost["IT"], Description: "Printer kantor", Amount: dec(4_500_000)},
	}}, "pms-seed-assets-waiting-bill"); err != nil {
		return 0, fmt.Errorf("bill: %w", err)
	}

	for _, j := range []struct {
		typ, ref, desc string
		debit          int64
		credit         int64
		amount         int64
	}{
		{accounting.JournalCashier, demoWaitingCashier, "Mesin kopi lobi dibayar dari laci kasir (demo daftar tunggu)", cost["FFE"], byCode["1110"], 3_200_000},
		{accounting.JournalBank, demoWaitingBank, "Transfer bank untuk televisi ruang rapat (demo daftar tunggu)", cost["FFE"], byCode["1130"], 8_900_000},
	} {
		if err := postSystem(ctx, acct, d, pid, accounting.SystemJournal{Type: j.typ, Date: businessDate, Description: j.desc, Reference: j.ref, Lines: []accounting.SystemLine{
			{AccountID: j.debit, Debit: dec(j.amount), Description: j.desc},
			{AccountID: j.credit, Credit: dec(j.amount), Description: j.desc},
		}}); err != nil {
			return 0, fmt.Errorf("%s journal: %w", j.typ, err)
		}
	}
	if _, err := acct.PostManual(ctx, pid, accounting.ManualInput{Date: businessDate, Description: "Switch jaringan kantor (demo daftar tunggu)", Reference: demoWaitingManual, Lines: []accounting.LineInput{
		{AccountID: cost["IT"], Debit: dec(12_750_000)},
		{AccountID: byCode["1130"], Credit: dec(12_750_000)},
	}}, "pms-seed-assets-waiting-manual"); err != nil {
		return 0, fmt.Errorf("manual journal: %w", err)
	}
	_, _ = fmt.Fprintln(out, "waiting list: documents booked")
	return waitingRows, nil
}

// postSystem posts a journal the way a module does: in its own transaction, on the open business day, through the poster of the accounting.
func postSystem(ctx context.Context, acct *accounting.Service, d *seedDeps, pid int64, j accounting.SystemJournal) error {
	return d.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := d.days.RequireOpenBusinessDay(ctx, pid, db.ForShare, nil); err != nil {
			return err
		}
		po, err := acct.BeginPosting(ctx, pid)
		if err != nil {
			return err
		}
		_, _, err = po.Post(ctx, j)
		return err
	})
}
