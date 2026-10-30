package pos_test

import (
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/pos"
	"kamarapms/internal/reports"
	"kamarapms/internal/rooms/roomstest"
)

// The reports of the night audit pack (docs/architecture/38-reports-night-audit-pack.md) over the sales of an outlet. The day (rates of the fixture: service 10%, PB1 10% on net plus service):
//
//	bill 1, 2 guests: nasi x2 + tea x2 = 130,000, a discount of 10% before the send (10,000 + 3,000), net 117,000, paid by card
//	bill 2, 3 guests: steak + tea sent (165,000), the steak voided "wrong order" with an approval: a reversal of net 150,000, service 15,000, tax 16,500 (gross 181,500); the tea is paid
//	bill 3, 4 guests: steak x10, net 1,500,000, paid
//	bill 4, 1 guest: nasi x2 sent (net 100,000) and given away as a comp (a complimentary payment of 121,000)
//	bill 5: a steak added to a draft and deleted before the send: nothing reaches the ledger
//	spa:     one massage of 200,000 net, on a charge code mapped to account 4310 (group C)
//
// The restaurant sells 117,000 + 15,000 + 1,500,000 + 100,000 = 1,732,000 net over 2 + 3 + 4 + 1 = 10 covers (173,200 a cover); taken back: 13,000 of discount and 150,000 of void = 163,000, which is
// 8.44% of the net revenue 1,732,000 + 200,000 = 1,932,000. The comp is a payment and carries no net.
func TestTheReportsOverTheSalesOfAnOutlet(t *testing.T) {
	b := newBillFx(t)
	b.shift(t)
	open := func(guests int) pos.Bill {
		b.seq++
		bill, err := b.svc.OpenBill(b.cashier, b.propID, b.resto.ID, pos.OpenBillInput{ClientUUID: uid(b.seq), OrderType: "TAKEAWAY", GuestCount: &guests})
		must(t, err)
		return bill
	}
	card := func(bill pos.Bill, key, amount string) {
		b.pay(t, b.cashier, bill, key, pos.PayLine{Method: "CARD", Amount: dec(amount)})
	}

	one := open(2)
	one = b.add(t, b.cashier, one, b.nasi, "2")
	one = b.add(t, b.cashier, one, b.tea, "2")
	one, err := b.svc.ApplyDiscount(b.cashier, b.propID, one.ID, pos.DiscountInput{Scope: "BILL", Percent: ptr(dec("10")), Reason: "regular", Approval: b.superIn})
	must(t, err)
	one = b.send(t, b.cashier, one)
	eq(t, "bill 1", one.Totals.Sold, "141570")
	card(one, "nap-key-0001", "141570")

	two := open(3)
	two = b.add(t, b.cashier, two, b.steak, "1")
	two = b.add(t, b.cashier, two, b.tea, "1")
	two = b.send(t, b.cashier, two)
	two, err = b.svc.VoidLine(b.cashier, b.propID, two.ID, two.Lines[0].ID, pos.VoidLineInput{Reason: "wrong order", Approval: b.superIn})
	must(t, err)
	eq(t, "bill 2", two.Totals.Sold, "18150")
	card(two, "nap-key-0002", "18150")

	three := open(4)
	three = b.add(t, b.cashier, three, b.steak, "10")
	three = b.send(t, b.cashier, three)
	eq(t, "bill 3", three.Totals.Sold, "1815000")
	card(three, "nap-key-0003", "1815000")

	comp := open(1)
	comp = b.add(t, b.cashier, comp, b.nasi, "2")
	comp = b.send(t, b.cashier, comp)
	comper := b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermPOSComp)
	_, err = b.svc.CompBill(comper, b.propID, comp.ID, pos.CompInput{Reason: "owner's guest", Name: "Pak Wayan", Approval: b.superIn})
	must(t, err)

	five := open(1)
	five = b.add(t, b.cashier, five, b.steak, "1")
	_, err = b.svc.DeleteLine(b.cashier, b.propID, five.ID, five.Lines[0].ID)
	must(t, err)

	// the spa
	_, err = b.Billing.UpdateChargeCode(b.admin, b.propID, b.chargeCode(t, "LAUNDRY"), billingconfig.ChargeCodePatch{GLAccountCode: ptr("4310")})
	must(t, err)
	spa := b.outlet(t, b.k, "S")
	massage, err := b.svc.CreateProduct(b.admin, b.propID, pos.ProductInput{Code: "MASSAGE", Name: "Massage", CategoryID: b.k.cat.ID, ChargeCodeID: b.chargeCode(t, "LAUNDRY"),
		Prices: []pos.ProductPriceIn{{PriceListID: b.k.priceList.ID, Price: dec("200000"), IsSold: true}}})
	must(t, err)
	b.seq++
	sb, err := b.svc.OpenBill(b.cashier, b.propID, spa.ID, pos.OpenBillInput{ClientUUID: uid(b.seq), OrderType: "TAKEAWAY"})
	must(t, err)
	b.seq++
	sb, err = b.svc.AddLine(b.cashier, b.propID, sb.ID, pos.AddLineInput{ClientUUID: uid(5000 + b.seq), ProductID: massage.ID, Quantity: dec("1")})
	must(t, err)
	b.send(t, b.cashier, sb)

	// corrections: the draft line is nowhere, the void once, the discount of each line
	day := roomstest.BD
	r, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: reports.CorrAll})
	must(t, err)
	got := map[string][]reports.CorrectionRow{}
	for _, x := range r.Rows {
		got[x.Kind] = append(got[x.Kind], x)
	}
	if len(r.Rows) != 4 || len(got[reports.CorrDiscount]) != 2 || len(got[reports.CorrPosVoid]) != 1 || len(got[reports.CorrComp]) != 1 {
		t.Fatalf("rows: %+v", r.Rows)
	}
	v := got[reports.CorrPosVoid][0]
	if v.Net != "-150000" || v.Service != "-15000" || v.Tax != "-16500" || v.Gross != "-181500" || v.Reason != "wrong order" || v.Outlet != "Restoran" || v.Bill == "" || v.ApprovedBy == "" || v.DoneBy == "" {
		t.Fatalf("void: %+v", v)
	}
	cp := got[reports.CorrComp][0]
	if cp.Gross != "121000" || cp.Net != "0" || cp.ChargeCode != "" || cp.PaymentMethod != "COMP" || cp.Outlet != "Restoran" || cp.DoneBy == "" {
		t.Fatalf("comp: %+v", cp)
	}
	nets := map[string]bool{}
	for _, x := range got[reports.CorrDiscount] {
		nets[x.Net] = true
		if x.Reason != "regular" || x.Outlet != "Restoran" || x.ApprovedBy == "" {
			t.Fatalf("discount: %+v", x)
		}
	}
	if !nets["-10000"] || !nets["-3000"] {
		t.Fatalf("discounts: %+v", got[reports.CorrDiscount])
	}
	if r.RevenueTakenBack != "163000" || r.NetRevenue != "1932000" || r.SharePercent == nil || *r.SharePercent != "8.44" {
		t.Fatalf("share: taken %s of %s = %v", r.RevenueTakenBack, r.NetRevenue, r.SharePercent)
	}
	only, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: reports.CorrPosVoid})
	must(t, err)
	if len(only.Rows) != 1 {
		t.Fatalf("filter: %+v", only.Rows)
	}
	spaOnly, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: reports.CorrAll, OutletID: &spa.ID})
	must(t, err)
	if len(spaOnly.Rows) != 0 {
		t.Fatalf("the spa gave nothing back: %+v", spaOnly.Rows)
	}

	// the daily flash: restaurant in B, spa in C, covers 2 + 3 + 4 + 1 = 10 of the four closed bills
	fl, err := b.Reports.DailyFlash(b.admin, b.propID, day)
	must(t, err)
	var blocks = map[string]reports.FlashBlock{}
	for _, g := range fl.Revenue.Groups {
		for _, bk := range g.Blocks {
			blocks[g.Code+"/"+bk.Title] = bk
		}
	}
	if bk, ok := blocks["B/Restoran"]; !ok || bk.Total.Today != "1732000" || bk.Kind != "OUTLET" {
		t.Fatalf("restaurant block: %+v of %+v", bk, blocks)
	}
	if bk, ok := blocks["C/S outlet"]; !ok || bk.Total.Today != "200000" {
		t.Fatalf("spa block (other operated departments are group C): %+v of %+v", bk, blocks)
	}
	if fl.Revenue.TotalNet.Today != "1932000" {
		t.Fatalf("total net %s", fl.Revenue.TotalNet.Today)
	}
	rev, err := b.Reports.Revenue(b.admin, b.propID, day, day)
	must(t, err)
	if rev.Totals.Net != fl.Revenue.TotalNet.Today { // A + B + C is the income statement
		t.Fatalf("income statement %s vs %s", rev.Totals.Net, fl.Revenue.TotalNet.Today)
	}
	var covers, billsMissing, perCover string
	for _, c := range fl.Covers {
		switch c.Label {
		case "Restoran: covers":
			covers = c.Today
		case "Restoran: bills without a guest count":
			billsMissing = c.Today
		case "Restoran: average per cover":
			perCover = c.Today
		}
	}
	if covers != "10" || billsMissing != "0" || perCover != "173200" {
		t.Fatalf("covers %q missing %q per cover %q: %+v", covers, billsMissing, perCover, fl.Covers)
	}
}

// A complimentary bill of the outlet under each of the two treatments of the outlet (34-pos.md): the correction report shows it as one COMP row, never none and never two. The bill: nasi goreng x2,
// net 100,000, service 10,000, tax 11,000, gross 121,000.
//
//	PAYMENT_COMP: the charge stays and a complimentary payment of 121,000 settles it. The row is the payment (net 0); the revenue taken back is 0 of the net revenue 100,000 (0.00%).
//	DISCOUNT_100: the charge is cancelled by an adjustment of -100,000 net (-10,000 service, -11,000 tax). The row is that adjustment, as COMP and not as an ADJUSTMENT or a DISCOUNT; the revenue
//	              taken back is 100,000 and the net revenue left is 0, so the share has no base.
func TestAComplimentaryBillIsOneRowOfTheCorrectionsInBothTreatments(t *testing.T) {
	for _, treatment := range []string{"PAYMENT_COMP", "DISCOUNT_100"} {
		t.Run(treatment, func(t *testing.T) {
			b := newBillFx(t)
			_, err := b.svc.UpdateOutlet(b.admin, b.propID, b.resto.ID, pos.OutletInput{
				Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: b.dept, RuleProfileID: b.k.profile.ID, PriceListID: b.k.priceList.ID,
				Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: treatment, Version: b.resto.Version,
			})
			must(t, err)
			bill := b.open(t, b.cashier, nil)
			bill = b.add(t, b.cashier, bill, b.nasi, "2")
			bill = b.send(t, b.cashier, bill)
			comper := b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermPOSComp)
			_, err = b.svc.CompBill(comper, b.propID, bill.ID, pos.CompInput{Reason: "owner's guest", Name: "Pak Wayan", Approval: b.superIn})
			must(t, err)

			day := roomstest.BD
			r, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: reports.CorrAll})
			must(t, err)
			if len(r.Rows) != 1 {
				t.Fatalf("a comp is one row, got %d: %+v", len(r.Rows), r.Rows)
			}
			row := r.Rows[0]
			if row.Kind != reports.CorrComp || row.Outlet != "Restoran" || row.DoneBy == "" {
				t.Fatalf("the row is a comp of the restaurant: %+v", row)
			}
			switch treatment {
			case "PAYMENT_COMP":
				if row.Net != "0" || row.Service != "0" || row.Tax != "0" || row.Gross != "121000" || row.ChargeCode != "" || row.PaymentMethod != "COMP" {
					t.Fatalf("the comp payment: %+v", row)
				}
				if r.RevenueTakenBack != "0" || r.NetRevenue != "100000" || r.SharePercent == nil || *r.SharePercent != "0.00" {
					t.Fatalf("a comp payment takes back no revenue: %s of %s = %v", r.RevenueTakenBack, r.NetRevenue, r.SharePercent)
				}
			default:
				if row.Net != "-100000" || row.Service != "-10000" || row.Tax != "-11000" || row.Gross != "-121000" || row.Reason != "Complimentary: owner's guest" {
					t.Fatalf("the 100%% discount: %+v", row)
				}
				if r.RevenueTakenBack != "100000" || r.NetRevenue != "0" || r.SharePercent != nil {
					t.Fatalf("a 100%% discount takes back all the revenue: %s of %s = %v", r.RevenueTakenBack, r.NetRevenue, r.SharePercent)
				}
			}
			// the same event is in no other kind
			for _, kind := range []string{reports.CorrDiscount, reports.CorrAdjust, reports.CorrAllowance, reports.CorrVoid, reports.CorrRefund, reports.CorrPosVoid} {
				other, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: kind})
				must(t, err)
				if len(other.Rows) != 0 {
					t.Fatalf("the comp is also listed as %s: %+v", kind, other.Rows)
				}
			}
			only, err := b.Reports.Corrections(b.admin, b.propID, reports.CorrectionParams{From: day, To: day, Kind: reports.CorrComp})
			must(t, err)
			if len(only.Rows) != 1 {
				t.Fatalf("the comp filter: %+v", only.Rows)
			}
		})
	}
}
