package main

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/departments"
	"kamarapms/internal/inventory"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/auth"
)

// The purchasing part of `pms-seed inventory` (docs/architecture/35-inventory.md, PR 2): a supplier, a purchase request that waits at level 2 of its approval chain, and a purchase request that was
// approved, ordered and received in part (which leaves a draft bill to match). It is made once: the client uuids are fixed, and a property that holds them already is left alone.
//
// The chain needs three people: the requester, the head of the department (level 1) and someone else for level 2. The seed uses the first three administrators of the tenant, so that a development
// database that has them gets the demo and one that has fewer says so and goes on. The head of the department FB is set to the second one when it has none.
//
// The invoice of the draft bill is not made: its price difference is what the person who matches it types in (mockup 21).

const (
	demoSupplierCode  = "SUMBER"
	demoRunningUUID   = "5eed0000-0000-4000-8001-000000000001"
	demoReceivedUUID  = "5eed0000-0000-4000-8001-000000000002"
	demoOrderUUID     = "5eed0000-0000-4000-8001-000000000003"
	demoReceiptUUID   = "5eed0000-0000-4000-8001-000000000004"
	demoRequestsNote  = "Kebutuhan mingguan (demo)"
	demoDepartment    = "FB"
	demoWarehouseCode = "UTAMA"
)

type demoLine struct{ item, unit, qty, price string }

// The lines of the two requests: all items and units are those of invItems. The totals stay under the threshold of the third level of the default chain (5,000,000).
var (
	demoRunningLines  = []demoLine{{"BERAS", "karung", "4", "434000"}, {"MGRNG", "l", "20", "23500"}}
	demoReceivedLines = []demoLine{{"AIRMIN", "karton", "20", "60000"}, {"SIRUP", "karton", "5", "588000"}}
	// the quantity of the first line of the second request that comes in the first delivery (a part of the 20 cartons)
	demoReceivedQty = "12"
)

func (e *env) as(userID int64) context.Context {
	return auth.WithPrincipal(e.ctx, auth.Principal{TenantID: e.tenant.ID, UserID: userID, IsTenantAdmin: true})
}

func seedPurchasing(e *env, svc *inventory.Service, deptSvc *departments.Service, unit, item, warehouse, deptID map[string]int64) error {
	ctx, pid := e.ctx, e.propertyID

	// the people: the first three administrators of the tenant
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
	if len(admins) < 3 {
		fmt.Printf("purchasing: skipped, the demo of the approval chain needs three administrators in the tenant (found %d); create them with pms-admin create-admin\n", len(admins))
		return nil
	}
	l2, head, requester := admins[0], admins[1], admins[2]

	var made int
	if err := e.txm.DB(ctx).QueryRow(ctx, `SELECT count(*) FROM inv_requests WHERE property_id = $1 AND client_uuid IN ($2, $3)`, pid, demoRunningUUID, demoReceivedUUID).Scan(&made); err != nil {
		return err
	}
	if made > 0 {
		fmt.Println("purchasing: the demo requests exist")
		return nil
	}

	// the supplier
	paySvc := payables.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, nil, nil, nil)
	var supplierID int64
	err = e.txm.DB(ctx).QueryRow(ctx, `SELECT id FROM suppliers WHERE property_id = $1 AND code = $2`, pid, demoSupplierCode).Scan(&supplierID)
	if err != nil {
		terms := 14
		s, err := paySvc.CreateSupplier(ctx, pid, payables.SupplierInput{Code: demoSupplierCode, Name: "CV Sumber Segar", ContactName: "Bu Ratna", Phone: "0361 555 0101", City: "Denpasar", PaymentTermsDays: &terms})
		if err != nil {
			return fmt.Errorf("supplier: %w", err)
		}
		supplierID = s.ID
	}
	fmt.Printf("supplier %s: %d\n", demoSupplierCode, supplierID)

	// the head of the department
	dep := deptID[demoDepartment]
	if dep == 0 {
		fmt.Printf("purchasing: skipped, the department %s does not exist\n", demoDepartment)
		return nil
	}
	if d, err := deptSvc.Get(ctx, pid, dep); err == nil && d.HeadUserID == nil {
		if _, err := deptSvc.Update(ctx, pid, dep, departments.Patch{HeadUserID: &head}); err != nil {
			return fmt.Errorf("head of the department: %w", err)
		}
	}

	whID := warehouse[demoWarehouseCode]
	build := func(uuid string, lines []demoLine) (inventory.Request, error) {
		note := demoRequestsNote
		in := inventory.RequestInput{ClientUUID: uuid, DepartmentID: &dep, WarehouseID: &whID, Note: &note}
		for _, l := range lines {
			qty, err := decimal.NewFromString(l.qty)
			if err != nil {
				return inventory.Request{}, err
			}
			price, err := decimal.NewFromString(l.price)
			if err != nil {
				return inventory.Request{}, err
			}
			sid := supplierID
			in.Lines = append(in.Lines, inventory.RequestLineInput{ItemID: item[l.item], UnitID: unit[l.unit], Qty: qty, EstUnitPrice: &price, SupplierID: &sid})
		}
		r, err := svc.CreateRequest(e.as(requester), pid, in)
		if err != nil {
			return inventory.Request{}, err
		}
		return svc.SubmitRequest(e.as(requester), pid, r.ID)
	}

	// 1. the request that waits at level 2: the head approved level 1
	running, err := build(demoRunningUUID, demoRunningLines)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if _, err := svc.ApproveRequest(e.as(head), pid, running.ID, inventory.ApproveInput{}); err != nil {
		return fmt.Errorf("request %s, level 1: %w", running.Number, err)
	}
	fmt.Printf("request %s: waits at level 2\n", running.Number)

	// 2. the request that was approved at both levels, ordered and received in part
	done, err := build(demoReceivedUUID, demoReceivedLines)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if _, err := svc.ApproveRequest(e.as(head), pid, done.ID, inventory.ApproveInput{}); err != nil {
		return fmt.Errorf("request %s, level 1: %w", done.Number, err)
	}
	if _, err := svc.ApproveRequest(e.as(l2), pid, done.ID, inventory.ApproveInput{}); err != nil {
		return fmt.Errorf("request %s, level 2: %w", done.Number, err)
	}
	var orderLines []inventory.POFromRequestLine
	for i, l := range demoReceivedLines {
		price, err := decimal.NewFromString(l.price)
		if err != nil {
			return err
		}
		orderLines = append(orderLines, inventory.POFromRequestLine{LineNo: i + 1, UnitPrice: price})
	}
	orders, err := svc.CreateOrdersFromRequest(e.as(requester), pid, done.ID, inventory.POFromRequestInput{
		WarehouseID: &whID, Send: true, Orders: []inventory.POFromRequestOrder{{ClientUUID: demoOrderUUID, SupplierID: supplierID, Lines: orderLines}},
	})
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	if len(orders) != 1 {
		return fmt.Errorf("orders: %d, want 1", len(orders))
	}
	qty, err := decimal.NewFromString(demoReceivedQty)
	if err != nil {
		return err
	}
	first := 1
	note := "Pengiriman pertama (demo)"
	rec, err := svc.PostReceipt(e.as(requester), pid, inventory.ReceiptInput{
		ClientUUID: demoReceiptUUID, SupplierID: supplierID, POID: &orders[0].ID, WarehouseID: whID, Note: &note,
		Lines: []inventory.ReceiptLineInput{{POLineNo: &first, ItemID: item[demoReceivedLines[0].item], UnitID: unit[demoReceivedLines[0].unit], Qty: qty}},
	})
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	fmt.Printf("request %s -> order %s -> receipt %s (partly): a draft bill waits for the invoice\n", done.Number, orders[0].Number, rec.Number)
	return nil
}
