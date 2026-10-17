package main

import (
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/departments"
	"kamarapms/internal/inventory"
)

// The movements part of `pms-seed inventory` (docs/architecture/35-inventory.md, PR 3): a store requisition that waits at level 2, one transfer, one issue of the housekeeping, one waste, the default plan of
// the automatic amenities and one count that is open in a small warehouse (the bar). Each document has a fixed client uuid, so the seed is made once and a second run leaves it alone.
//
// The requisition needs the same three administrators as the purchasing (the requester, the head of the department, level 2); without them it is skipped and the rest goes on.

const (
	demoSRUUID       = "5eed0000-0000-4000-8002-000000000001"
	demoTransferUUID = "5eed0000-0000-4000-8002-000000000002"
	demoIssueUUID    = "5eed0000-0000-4000-8002-000000000003"
	demoWasteUUID    = "5eed0000-0000-4000-8002-000000000004"
	demoCountUUID    = "5eed0000-0000-4000-8002-000000000005"
	// the expense account of the amenities: the issue account of the supplies
	demoAmenityAccount = "5150"
)

// The lines of the demo documents: the items and units are those of invItems, in a unit the item has (the price is not used).
var (
	demoSRLines       = []demoLine{{"BERAS", "kg", "30", ""}, {"TELUR", "pcs", "120", ""}, {"GULA", "kg", "10", ""}}
	demoTransferLines = []demoLine{{"SIRUP", "botol", "6", ""}, {"SODA", "botol", "24", ""}}
	demoIssueLines    = []demoLine{{"DETERJEN", "pcs", "1", ""}, {"PEMBERSIH", "pcs", "1", ""}}
	demoWasteLines    = []demoLine{{"AYAM", "kg", "0.5", ""}}
	// the plan of the amenities: per room, in the stock unit of the item
	demoAmenityLines = []demoLine{{"SAMPO", "pcs", "2", ""}, {"SABUN", "pcs", "2", ""}, {"SIKAT", "pcs", "1", ""}, {"PASTA", "pcs", "1", ""}, {"TEHKMR", "pcs", "2", ""}, {"KOPIKMR", "pcs", "2", ""}}
	// the items of the open count of the bar
	demoCountItems = []string{"TEHCL", "KOPI", "SODA"}
)

func seedMovements(e *env, svc *inventory.Service, deptSvc *departments.Service, unit, item, warehouse, deptID map[string]int64) error {
	ctx, pid := e.ctx, e.propertyID
	exists := func(table, uuid string) (bool, error) {
		var n int
		err := e.txm.DB(ctx).QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE property_id = $1 AND client_uuid = $2`, pid, uuid).Scan(&n) //nolint:gosec // G202: the table is a constant of this file
		return n > 0, err
	}
	lines := func(ls []demoLine) ([]inventory.TransferLineInput, error) {
		var out []inventory.TransferLineInput
		for _, l := range ls {
			q, err := decimal.NewFromString(l.qty)
			if err != nil {
				return nil, err
			}
			uid := unit[l.unit]
			out = append(out, inventory.TransferLineInput{ItemID: item[l.item], UnitID: &uid, Qty: q})
		}
		return out, nil
	}

	// 1. the requisition of the kitchen to the main store, approved by the head of the department and waiting at level 2
	if ok, err := exists("inv_requisitions", demoSRUUID); err != nil {
		return err
	} else if ok {
		fmt.Println("requisition: the demo exists")
	} else if err := seedRequisition(e, svc, deptSvc, warehouse, deptID, lines); err != nil {
		return err
	}

	// 2. a free transfer of the main store to the bar
	if ok, err := exists("inv_transfers", demoTransferUUID); err != nil {
		return err
	} else if ok {
		fmt.Println("transfer: the demo exists")
	} else {
		ls, err := lines(demoTransferLines)
		if err != nil {
			return err
		}
		note := "Isi ulang bar (demo)"
		t, err := svc.PostTransfer(ctx, pid, inventory.TransferInput{ClientUUID: demoTransferUUID, FromWarehouseID: warehouse["UTAMA"], ToWarehouseID: warehouse["BAR"], Note: &note, Lines: ls})
		if err != nil {
			return fmt.Errorf("transfer: %w", err)
		}
		fmt.Printf("transfer %s: main store -> bar\n", t.Number)
	}

	// 3. the use of the housekeeping and a waste of the kitchen
	issue := func(uuid, purpose, wh string, dept *int64, reason, note string, ls []demoLine) error {
		if ok, err := exists("inv_issues", uuid); err != nil {
			return err
		} else if ok {
			fmt.Printf("issue %s: the demo exists\n", purpose)
			return nil
		}
		in := inventory.IssueInput{ClientUUID: uuid, WarehouseID: warehouse[wh], Purpose: purpose, DepartmentID: dept, Note: &note}
		if reason != "" {
			in.ReasonCode = &reason
		}
		for _, l := range ls {
			q, err := decimal.NewFromString(l.qty)
			if err != nil {
				return err
			}
			uid := unit[l.unit]
			in.Lines = append(in.Lines, inventory.IssueLineInput{ItemID: item[l.item], UnitID: &uid, Qty: q})
		}
		d, err := svc.PostIssue(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("issue %s: %w", purpose, err)
		}
		fmt.Printf("issue %s %s\n", purpose, d.Number)
		return nil
	}
	rooms := deptID["ROOMS"]
	if rooms == 0 {
		fmt.Println("movements: the housekeeping issue is skipped, the department ROOMS does not exist")
	} else if err := issue(demoIssueUUID, inventory.PurposeUse, "HK", &rooms, "", "Pembersih kamar mandi (demo)", demoIssueLines); err != nil {
		return err
	}
	if err := issue(demoWasteUUID, inventory.PurposeWaste, "DAPUR", nil, "SPOILAGE", "Ayam rusak di chiller (demo)", demoWasteLines); err != nil {
		return err
	}

	// 4. the plan of the amenities: once, as the first version
	plan, err := svc.GetAmenityPlan(ctx, pid)
	if err != nil {
		return err
	}
	switch {
	case plan.VersionNo > 0:
		fmt.Println("amenities: the plan exists")
	case rooms == 0:
		fmt.Println("amenities: skipped, the department ROOMS does not exist")
	default:
		hk := warehouse["HK"]
		var account int64
		if err := e.txm.DB(ctx).QueryRow(ctx, `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = $2`, pid, demoAmenityAccount).Scan(&account); err != nil {
			return fmt.Errorf("amenities: the account %s: %w", demoAmenityAccount, err)
		}
		in := inventory.AmenityPlanInput{IsEnabled: true, WarehouseID: &hk, DepartmentID: &rooms, ExpenseAccountID: &account}
		for _, l := range demoAmenityLines {
			q, err := decimal.NewFromString(l.qty)
			if err != nil {
				return err
			}
			in.Lines = append(in.Lines, inventory.AmenityPlanLineInput{ItemID: item[l.item], UnitID: unit[l.unit], Qty: q, Basis: inventory.BasisRoom})
		}
		p, err := svc.SaveAmenityPlan(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("amenities: %w", err)
		}
		fmt.Printf("amenities: plan version %d, %d lines per room\n", p.VersionNo, len(p.Lines))
	}

	// 5. a count of three items of the bar, open (the bar is frozen until it is approved or cancelled)
	if ok, err := exists("inv_counts", demoCountUUID); err != nil {
		return err
	} else if ok {
		fmt.Println("count: the demo exists")
		return nil
	}
	in := inventory.CountInput{ClientUUID: demoCountUUID, WarehouseID: warehouse["BAR"], Scope: inventory.ScopeList}
	for _, c := range demoCountItems {
		in.ItemIDs = append(in.ItemIDs, item[c])
	}
	note := "Opname mingguan bar (demo)"
	in.Note = &note
	// a count is started by a person (it keeps who): the first administrator
	var starter int64
	if err := e.txm.DB(ctx).QueryRow(ctx, `SELECT id FROM users WHERE tenant_id = $1 AND is_tenant_admin AND is_active ORDER BY id LIMIT 1`, e.tenant.ID).Scan(&starter); err != nil {
		fmt.Println("count: skipped, the tenant has no administrator")
		return nil
	}
	c, err := svc.StartCount(e.as(starter), pid, in)
	if err != nil {
		return fmt.Errorf("count: %w", err)
	}
	fmt.Printf("count %s: the bar is frozen until it is approved\n", c.Number)
	return nil
}

func seedRequisition(e *env, svc *inventory.Service, deptSvc *departments.Service, warehouse, deptID map[string]int64, lines func([]demoLine) ([]inventory.TransferLineInput, error)) error {
	ctx, pid := e.ctx, e.propertyID
	rows, err := e.txm.DB(ctx).Query(ctx, `SELECT id FROM users WHERE tenant_id = $1 AND is_tenant_admin AND is_active ORDER BY id LIMIT 3`, e.tenant.ID)
	if err != nil {
		return err
	}
	var admins []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		admins = append(admins, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	dep := deptID[demoDepartment]
	if len(admins) < 3 || dep == 0 {
		fmt.Printf("requisition: skipped, it needs three administrators in the tenant (found %d) and the department %s\n", len(admins), demoDepartment)
		return nil
	}
	head, requester := admins[1], admins[2]
	if d, err := deptSvc.Get(ctx, pid, dep); err == nil && d.HeadUserID == nil {
		if _, err := deptSvc.Update(ctx, pid, dep, departments.Patch{HeadUserID: &head}); err != nil {
			return fmt.Errorf("head of the department: %w", err)
		}
	}
	ls, err := lines(demoSRLines)
	if err != nil {
		return err
	}
	note := "Permintaan dapur (demo)"
	in := inventory.RequisitionInput{ClientUUID: demoSRUUID, ToWarehouseID: warehouse["DAPUR"], FromWarehouseID: warehouse["UTAMA"], DepartmentID: &dep, Note: &note}
	for _, l := range ls {
		in.Lines = append(in.Lines, inventory.RequisitionLineInput{ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty})
	}
	r, err := svc.CreateRequisition(e.as(requester), pid, in)
	if err != nil {
		return fmt.Errorf("requisition: %w", err)
	}
	if r, err = svc.SubmitRequisition(e.as(requester), pid, r.ID); err != nil {
		return fmt.Errorf("requisition: %w", err)
	}
	if _, err = svc.ApproveRequisition(e.as(head), pid, r.ID, inventory.ApproveInput{}); err != nil {
		return fmt.Errorf("requisition %s, level 1: %w", r.Number, err)
	}
	fmt.Printf("requisition %s: waits at level 2\n", r.Number)
	return nil
}
