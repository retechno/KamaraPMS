package folios_test

import (
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
)

func str(s string) *string { return &s }

// mapAccounts sets the GL account codes of MINIBAR, SVC and VAT (the codes the accounting module will own).
func (f *fx) mapAccounts(t *testing.T, revenue, service, tax string) {
	t.Helper()
	_, err := f.Billing.UpdateChargeCode(f.admin, f.propID, f.minibar, billingconfig.ChargeCodePatch{GLAccountCode: str(revenue)})
	must(t, err)
	var svcID, taxID int64
	must(t, f.Pool.QueryRow(t.Context(), `SELECT id FROM service_charges WHERE property_id = $1 AND code = 'SVC'`, f.propID).Scan(&svcID))
	must(t, f.Pool.QueryRow(t.Context(), `SELECT id FROM taxes WHERE property_id = $1 AND code = 'VAT'`, f.propID).Scan(&taxID))
	_, err = f.Billing.UpdateServiceCharge(f.admin, f.propID, svcID, billingconfig.ServiceChargePatch{GLAccountCode: str(service)})
	must(t, err)
	_, err = f.Billing.UpdateTax(f.admin, f.propID, taxID, billingconfig.TaxPatch{GLAccountCode: str(tax)})
	must(t, err)
}

func accounts(it folios.Item) (revenue string, service string, tax string) {
	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	for _, c := range it.Components {
		if c.ComponentType == "TAX" {
			tax = deref(c.GLAccountCode)
		} else {
			service = deref(c.GLAccountCode)
		}
	}
	return deref(it.RevenueAccountCode), service, tax
}

func TestPostedItemsCarryTheAccountCodesInForceWhenPosted(t *testing.T) {
	f := setup(t)
	// unmapped: nothing to carry
	before := f.charge(t, "c0").Item
	if r, s, x := accounts(before); r != "<nil>" || s != "<nil>" || x != "<nil>" {
		t.Fatalf("unmapped: %s %s %s", r, s, x)
	}
	f.mapAccounts(t, "4-1300", "2-2100", "2.1.05")
	mapped := f.charge(t, "c1").Item
	if r, s, x := accounts(mapped); r != "4-1300" || s != "2-2100" || x != "2.1.05" {
		t.Fatalf("mapped: %s %s %s", r, s, x)
	}
	// a later remapping never rewrites history
	f.mapAccounts(t, "4-9999", "2-9999", "2.9.99")
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	if r, s, x := accounts(fo.Items[1]); r != "4-1300" || s != "2-2100" || x != "2.1.05" {
		t.Fatalf("history changed: %s %s %s", r, s, x)
	}
	// new postings use the new mapping, a reversal copies the account of what it reverses
	fresh := f.charge(t, "c2").Item
	if r, _, _ := accounts(fresh); r != "4-9999" {
		t.Fatalf("new mapping: %s", r)
	}
	rev, err := f.Folios.Reverse(f.admin, f.propID, mapped.ID, folios.CorrectionInput{Reason: "twice", Approval: f.approval()})
	must(t, err)
	if r, s, x := accounts(rev.Item); r != "4-1300" || s != "2-2100" || x != "2.1.05" {
		t.Fatalf("reversal must mirror the original: %s %s %s", r, s, x)
	}
	// reversing the unmapped item after the mapping exists keeps it unmapped
	rev0, err := f.Folios.Reverse(f.admin, f.propID, before.ID, folios.CorrectionInput{Reason: "twice", Approval: f.approval()})
	must(t, err)
	if r, s, x := accounts(rev0.Item); r != "<nil>" || s != "<nil>" || x != "<nil>" {
		t.Fatalf("an unmapped item is reversed unmapped: %s %s %s", r, s, x)
	}
	// payments are not revenue
	pay := f.payment(t, "p1", "1000")
	if pay.FolioItem.RevenueAccountCode != nil {
		t.Fatalf("payment item: %+v", pay.FolioItem)
	}
	// accounting reads by code: per account totals reconcile with the ledger
	var net string
	must(t, f.Pool.QueryRow(t.Context(), `SELECT COALESCE(sum(net_amount), 0)::text FROM folio_items WHERE property_id = $1 AND revenue_account_code = '4-1300'`, f.propID).Scan(&net))
	if net != "0.000" {
		t.Fatalf("the charge and its reversal net to zero on the account: %s", net)
	}
}
