package accounting_test

import (
	"context"
	"encoding/csv"
	"os"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	return &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID)}
}

func (f *fx) accounts(t *testing.T, filter accounting.AccountFilter) []accounting.Account {
	t.Helper()
	list, err := f.Accounting.Accounts(f.admin, f.propID, filter)
	must(t, err)
	return list
}

func (f *fx) byCode(t *testing.T, code string) accounting.Account {
	t.Helper()
	for _, a := range f.accounts(t, accounting.AccountFilter{}) {
		if a.Code == code {
			return a
		}
	}
	t.Fatalf("no account %s", code)
	return accounting.Account{}
}

func ptr[T any](v T) *T { return &v }

func TestANewPropertyGetsTheStandardChart(t *testing.T) {
	f := setup(t)
	all := f.accounts(t, accounting.AccountFilter{})
	if len(all) < 150 {
		t.Fatalf("%d accounts", len(all))
	}
	byCode := map[string]accounting.Account{}
	for _, a := range all {
		byCode[a.Code] = a
		if a.IsPostable && a.StatementGroup == "" {
			t.Fatalf("a postable account needs a group: %+v", a)
		}
		if a.ParentID == nil && a.Code != "1000" && a.Code != "2000" && a.Code != "3000" && a.Code != "4000" && a.Code != "5000" && a.Code != "6000" && a.Code != "7000" {
			t.Fatalf("only the seven top headers have no parent: %+v", a)
		}
	}
	for code, want := range map[string]struct{ name, typ, side, parent string }{
		"1210": {"Guest ledger (in-house guests)", "ASSET", "DEBIT", "1200"},
		"1220": {"City ledger - accounts receivable", "ASSET", "DEBIT", "1200"},
		"1240": {"Allowance for doubtful accounts", "ASSET", "CREDIT", "1200"},
		"2310": {"Advance deposits", "LIABILITY", "CREDIT", "2300"},
		"4110": {"Room revenue - transient", "REVENUE", "CREDIT", "4100"},
		"4160": {"Rooms - allowances and rebates", "REVENUE", "DEBIT", "4100"},
		"5160": {"Commissions (travel agents and OTAs)", "EXPENSE", "DEBIT", "5100"},
		"6510": {"Electricity", "EXPENSE", "DEBIT", "6500"},
		"7630": {"Interest income", "EXPENSE", "CREDIT", "7600"},
	} {
		a, ok := byCode[code]
		if !ok || a.Name != want.name || a.AccountType != want.typ || a.NormalSide != want.side || a.ParentCode != want.parent || !a.IsPostable || !a.IsActive {
			t.Fatalf("%s: %+v", code, a)
		}
	}
	if h := byCode["4100"]; h.IsPostable || h.StatementGroup != "REV_ROOMS" || !h.InUse {
		t.Fatalf("a header groups and has children: %+v", h)
	}
	// the system accounts and the standard charge codes
	m, err := f.Accounting.AccountMap(f.admin, f.propID)
	must(t, err)
	got := map[string]string{}
	for _, e := range m {
		got[e.Key] = e.AccountCode
	}
	want := map[string]string{"CASH": "1110", "CARD": "1150", "BANK_TRANSFER": "1130", "OTHER_PAYMENT": "1160", "CITY_LEDGER": "1220", "GUEST_LEDGER": "1210",
		"ADVANCE_DEPOSITS": "2310", "TAX_PAYABLE": "2410", "SERVICE_PAYABLE": "2430", "SUSPENSE": "2990"}
	if len(m) != 10 || len(got) != 10 {
		t.Fatalf("map: %+v", m)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s -> %s, want %s", k, got[k], v)
		}
	}
	if m[0].Key != "CASH" || m[len(m)-1].Key != "SUSPENSE" || m[0].Meaning == "" {
		t.Fatalf("map order: %+v", m)
	}
	var room, rest string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT gl_account_code FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, f.propID).Scan(&room))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT gl_account_code FROM charge_codes WHERE property_id = $1 AND code = 'RESTAURANT'`, f.propID).Scan(&rest))
	if room != "4110" || rest != "4210" {
		t.Fatalf("charge codes: %s %s", room, rest)
	}
	rep, err := f.Accounting.UnmappedCodes(f.admin, f.propID)
	if err != nil || rep.Checked != 10 || len(rep.Issues) != 0 {
		t.Fatalf("every standard charge code is mapped: %+v %v", rep, err)
	}
	// filters
	if n := len(f.accounts(t, accounting.AccountFilter{AccountType: "REVENUE", Postable: ptr(true)})); n < 25 {
		t.Fatalf("revenue accounts: %d", n)
	}
	if n := len(f.accounts(t, accounting.AccountFilter{Query: "electric"})); n != 1 {
		t.Fatalf("search: %d", n)
	}
	if n := len(f.accounts(t, accounting.AccountFilter{StatementGroup: "UND_UTIL"})); n != 5 {
		t.Fatalf("group: %d", n)
	}
	_, err = f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{AccountType: "MAGIC"})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestEachPropertyHasItsOwnChart(t *testing.T) {
	f := setup(t)
	other := f.Property(t, f.tenantID, "UBUD")
	a, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4190", Name: "Only in Bali", AccountType: "REVENUE", StatementGroup: "REV_ROOMS"})
	must(t, err)
	list, err := f.Accounting.Accounts(f.admin, other.ID, accounting.AccountFilter{Query: "4190"})
	if err != nil || len(list) != 0 {
		t.Fatalf("another property does not see it: %+v %v", list, err)
	}
	if _, err := f.Accounting.Account(f.admin, other.ID, a.ID); err == nil {
		t.Fatal("an account of another property is not found")
	} else {
		wantCode(t, err, "ACCOUNT_NOT_FOUND")
	}
	// the same code can exist in both
	if _, err := f.Accounting.CreateAccount(f.admin, other.ID, accounting.AccountInput{Code: "4190", Name: "Also in Ubud", AccountType: "REVENUE", StatementGroup: "REV_ROOMS"}); err != nil {
		t.Fatal(err)
	}
	// and another tenant cannot reach either
	tn2 := f.Tenant(t, "XYZ")
	_, err = f.Accounting.Accounts(roomstest.Admin(tn2.ID), f.propID, accounting.AccountFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestAccountRules(t *testing.T) {
	f := setup(t)
	a, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: " 4170 ", Name: " Late check-out ", AccountType: "revenue", StatementGroup: "rev_rooms", Description: "fees"})
	must(t, err)
	if a.Code != "4170" || a.Name != "Late check-out" || a.AccountType != "REVENUE" || a.NormalSide != "CREDIT" || !a.IsPostable || !a.IsActive || a.StatementGroup != "REV_ROOMS" || a.InUse {
		t.Fatalf("created: %+v", a)
	}
	// rules at creation
	for name, in := range map[string]accounting.AccountInput{
		"bad code":              {Code: "bad code", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_ROOMS"},
		"no name":               {Code: "4171", Name: " ", AccountType: "REVENUE", StatementGroup: "REV_ROOMS"},
		"bad type":              {Code: "4171", Name: "x", AccountType: "MAGIC"},
		"no group":              {Code: "4171", Name: "x", AccountType: "REVENUE"},
		"group of another type": {Code: "4171", Name: "x", AccountType: "REVENUE", StatementGroup: "CASH"},
		"bad side":              {Code: "4171", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_ROOMS", NormalSide: "UP"},
	} {
		_, err := f.Accounting.CreateAccount(f.admin, f.propID, in)
		if err == nil {
			t.Fatalf("%s must be refused", name)
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	_, err = f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4170", Name: "Again", AccountType: "REVENUE", StatementGroup: "REV_ROOMS"})
	wantCode(t, err, "CODE_TAKEN")
	// a header needs no group; the parent must be a header of the same type
	hdr, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4600", Name: "Events revenue", AccountType: "REVENUE", IsPostable: ptr(false)})
	must(t, err)
	post := f.byCode(t, "4110")
	_, err = f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4610", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_MISC", ParentID: &post.ID})
	wantCode(t, err, "VALIDATION_FAILED") // a postable parent
	cash := f.byCode(t, "1100")
	_, err = f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4610", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_MISC", ParentID: &cash.ID})
	wantCode(t, err, "VALIDATION_FAILED") // another type
	child, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4610", Name: "Weddings", AccountType: "REVENUE", StatementGroup: "REV_MISC", ParentID: &hdr.ID})
	must(t, err)
	if child.ParentCode != "4600" {
		t.Fatalf("child: %+v", child)
	}
	// updates: the name and the group change; the header with a child cannot take postings; no loops
	up, err := f.Accounting.UpdateAccount(f.admin, f.propID, a.ID, accounting.AccountPatch{Name: ptr("Late check-out fee"), StatementGroup: ptr("REV_MISC"), IsActive: ptr(false)})
	if err != nil || up.Name != "Late check-out fee" || up.StatementGroup != "REV_MISC" || up.IsActive {
		t.Fatalf("update: %+v %v", up, err)
	}
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, hdr.ID, accounting.AccountPatch{IsPostable: ptr(true), StatementGroup: ptr("REV_MISC")})
	wantCode(t, err, "ACCOUNT_HAS_CHILDREN")
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, a.ID, accounting.AccountPatch{StatementGroup: ptr("CASH")})
	wantCode(t, err, "VALIDATION_FAILED")
	hdr2, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4700", Name: "Other events", AccountType: "REVENUE", IsPostable: ptr(false), ParentID: &hdr.ID})
	must(t, err)
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, hdr.ID, accounting.AccountPatch{ParentID: &hdr2.ID})
	wantCode(t, err, "VALIDATION_FAILED") // 4600 under its own child
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, hdr.ID, accounting.AccountPatch{ParentID: &hdr.ID})
	wantCode(t, err, "VALIDATION_FAILED")
	none, err := f.Accounting.UpdateAccount(f.admin, f.propID, hdr2.ID, accounting.AccountPatch{ParentID: ptr(int64(0))})
	if err != nil || none.ParentID != nil {
		t.Fatalf("parent removed: %+v %v", none, err)
	}
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, 99999, accounting.AccountPatch{})
	wantCode(t, err, "ACCOUNT_NOT_FOUND")

	// deleting: not while used
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, a.ID, accounting.AccountPatch{IsActive: ptr(true)})
	must(t, err)
	if err := f.Accounting.DeleteAccount(f.admin, f.propID, hdr.ID); err == nil {
		t.Fatal("an account with children cannot be deleted")
	} else {
		wantCode(t, err, "ACCOUNT_IN_USE")
	}
	if err := f.Accounting.DeleteAccount(f.admin, f.propID, f.byCode(t, "1210").ID); err == nil {
		t.Fatal("an account the system posts to cannot be deleted")
	} else {
		wantCode(t, err, "ACCOUNT_IN_USE")
	}
	if err := f.Exec(t, `UPDATE charge_codes SET gl_account_code = '4170' WHERE property_id = $1 AND code = 'OTHER'`, f.propID); err != nil {
		t.Fatal(err)
	}
	if err := f.Accounting.DeleteAccount(f.admin, f.propID, a.ID); err == nil {
		t.Fatal("an account a charge code points at cannot be deleted")
	} else {
		wantCode(t, err, "ACCOUNT_IN_USE")
	}
	if err := f.Exec(t, `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'OTHER'`, f.propID); err != nil {
		t.Fatal(err)
	}
	must(t, f.Accounting.DeleteAccount(f.admin, f.propID, a.ID))
	must(t, f.Accounting.DeleteAccount(f.admin, f.propID, child.ID))
	_, err = f.Accounting.Account(f.admin, f.propID, a.ID)
	wantCode(t, err, "ACCOUNT_NOT_FOUND")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'accounting.account_%'`) != 9 {
		t.Fatalf("every change is audited: %d", f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'accounting.account_%'`))
	}
}

func TestSystemAccountsCannotBeSwitchedOff(t *testing.T) {
	f := setup(t)
	guest := f.byCode(t, "1210")
	_, err := f.Accounting.UpdateAccount(f.admin, f.propID, guest.ID, accounting.AccountPatch{IsActive: ptr(false)})
	wantCode(t, err, "ACCOUNT_IN_USE")
	_, err = f.Accounting.UpdateAccount(f.admin, f.propID, guest.ID, accounting.AccountPatch{IsPostable: ptr(false)})
	wantCode(t, err, "ACCOUNT_IN_USE")
	// once the key points elsewhere, the account can go
	other, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "1215", Name: "Guest ledger - rooms", AccountType: "ASSET", StatementGroup: "RECEIVABLES"})
	must(t, err)
	_, err = f.Accounting.SetAccountMap(f.admin, f.propID, []accounting.MapInput{{Key: "GUEST_LEDGER", AccountID: other.ID}})
	must(t, err)
	if _, err := f.Accounting.UpdateAccount(f.admin, f.propID, guest.ID, accounting.AccountPatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}
}

func TestAccountMap(t *testing.T) {
	f := setup(t)
	bank2, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "1135", Name: "Bank - USD account", AccountType: "ASSET", StatementGroup: "CASH"})
	must(t, err)
	m, err := f.Accounting.SetAccountMap(f.admin, f.propID, []accounting.MapInput{{Key: "BANK_TRANSFER", AccountID: bank2.ID}})
	if err != nil || len(m) != 10 {
		t.Fatalf("set: %+v %v", m, err)
	}
	for _, e := range m {
		if e.Key == "BANK_TRANSFER" && e.AccountCode != "1135" {
			t.Fatalf("bank: %+v", e)
		}
	}
	// the rules: right type, an account that takes postings, active, known key, each key once, all or nothing
	rev := f.byCode(t, "4110")
	hdr := f.byCode(t, "1100")
	liab := f.byCode(t, "2310")
	off, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "1136", Name: "Closed bank", AccountType: "ASSET", StatementGroup: "CASH", IsActive: ptr(false)})
	must(t, err)
	for name, in := range map[string][]accounting.MapInput{
		"revenue for cash":         {{Key: "CASH", AccountID: rev.ID}},
		"a header":                 {{Key: "CASH", AccountID: hdr.ID}},
		"an asset for the deposit": {{Key: "ADVANCE_DEPOSITS", AccountID: f.byCode(t, "1110").ID}},
		"a liability for the bank": {{Key: "BANK_TRANSFER", AccountID: liab.ID}},
		"an inactive account":      {{Key: "CASH", AccountID: off.ID}},
		"an unknown key":           {{Key: "PETTY", AccountID: bank2.ID}},
		"a key twice":              {{Key: "CASH", AccountID: bank2.ID}, {Key: "CASH", AccountID: bank2.ID}},
		"an unknown account":       {{Key: "CASH", AccountID: 99999}},
		"nothing":                  nil,
	} {
		_, err := f.Accounting.SetAccountMap(f.admin, f.propID, in)
		if err == nil {
			t.Fatalf("%s must be refused", name)
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	// suspense may be any type
	if _, err := f.Accounting.SetAccountMap(f.admin, f.propID, []accounting.MapInput{{Key: "SUSPENSE", AccountID: f.byCode(t, "1230").ID}}); err != nil {
		t.Fatal(err)
	}
	// all or nothing: the good entry is not kept when another is bad
	_, err = f.Accounting.SetAccountMap(f.admin, f.propID, []accounting.MapInput{{Key: "CASH", AccountID: bank2.ID}, {Key: "CARD", AccountID: rev.ID}})
	wantCode(t, err, "VALIDATION_FAILED")
	cur, err := f.Accounting.AccountMap(f.admin, f.propID)
	must(t, err)
	for _, e := range cur {
		if e.Key == "CASH" && e.AccountCode != "1110" {
			t.Fatalf("nothing changed: %+v", e)
		}
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.map_changed'`) != 2 {
		t.Fatal("changes of the map are audited")
	}
}

func TestUnmappedCodesAreFoundAndSaySeWhereTheyGo(t *testing.T) {
	f := setup(t)
	tax, err := f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", GLAccountCode: "9999", IsActive: true})
	must(t, err)
	svc, err := f.Billing.CreateServiceCharge(f.admin, f.propID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", GLAccountCode: "1100", IsActive: true})
	must(t, err)
	_, err = f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "OLD", Name: "Old tax", Rate: "5", IsActive: false})
	must(t, err)
	ok, err := f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "PB1", Name: "Hotel tax", Rate: "10", GLAccountCode: "2410", IsActive: true})
	must(t, err)
	_ = ok
	if err := f.Exec(t, `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'LAUNDRY'`, f.propID); err != nil {
		t.Fatal(err)
	}
	if err := f.Exec(t, `UPDATE charge_codes SET gl_account_code = '2310' WHERE property_id = $1 AND code = 'OTHER'`, f.propID); err != nil { // a liability for revenue
		t.Fatal(err)
	}
	rep, err := f.Accounting.UnmappedCodes(f.admin, f.propID)
	must(t, err)
	got := map[string]accounting.CodeIssue{}
	for _, i := range rep.Issues {
		got[i.Kind+"/"+i.Code] = i
	}
	// the inactive tax is not checked; the mapped tax is fine
	if rep.Checked != 13 || len(got) != 4 {
		t.Fatalf("report: %+v", rep)
	}
	for key, want := range map[string]struct{ problem, posted string }{
		"CHARGE_CODE/LAUNDRY": {"NO_CODE", "2990"},
		"CHARGE_CODE/OTHER":   {"WRONG_TYPE", "2990"},
		"TAX/VAT":             {"UNKNOWN_ACCOUNT", "2410"},
		"SERVICE_CHARGE/SVC":  {"HEADER_ACCOUNT", "2430"},
	} {
		i, found := got[key]
		if !found || i.Problem != want.problem || !strings.HasPrefix(i.PostedTo, want.posted) {
			t.Fatalf("%s: %+v", key, i)
		}
	}
	_, _ = tax, svc
	// fixing a code takes it off the list
	if err := f.Exec(t, `UPDATE taxes SET gl_account_code = '2420' WHERE property_id = $1 AND code = 'VAT'`, f.propID); err != nil {
		t.Fatal(err)
	}
	rep, err = f.Accounting.UnmappedCodes(f.admin, f.propID)
	if err != nil || len(rep.Issues) != 3 {
		t.Fatalf("after the fix: %+v %v", rep, err)
	}
}

func TestChartCSVRoundTripAndAtomicImport(t *testing.T) {
	f := setup(t)
	rows, err := f.Accounting.ExportCSV(f.admin, f.propID)
	must(t, err)
	if len(rows) != len(f.accounts(t, accounting.AccountFilter{}))+1 || strings.Join(rows[0], ",") != "code,name,type,parent_code,postable,group,active,description" {
		t.Fatalf("export: %d rows, header %v", len(rows), rows[0])
	}
	csvOf := func(rows [][]string) string {
		var b strings.Builder
		w := csv.NewWriter(&b)
		must(t, w.WriteAll(rows))
		return b.String()
	}
	// importing the export changes nothing but counts every row as updated
	res, err := f.Accounting.ImportCSV(f.admin, f.propID, csvOf(rows), false)
	if err != nil || res.Created != 0 || res.Updated != len(rows)-1 {
		t.Fatalf("round trip: %+v %v", res, err)
	}
	// new accounts with the parent after the child, an update, and a dry run first
	file := "code,name,type,parent_code,postable,group,active\n" +
		"4650,Spa packages,REVENUE,4620,yes,REV_OOD,yes\n" +
		"4620,Spa revenue,REVENUE,4000,no,,yes\n" +
		"4110,Room revenue - walk-in,REVENUE,4100,yes,REV_ROOMS,yes\n"
	dry, err := f.Accounting.ImportCSV(f.admin, f.propID, file, true)
	if err != nil || !dry.DryRun || dry.Created != 2 || dry.Updated != 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if f.Count(t, `SELECT count(*) FROM gl_accounts WHERE code IN ('4650', '4620')`) != 0 {
		t.Fatal("a dry run keeps nothing")
	}
	res, err = f.Accounting.ImportCSV(f.admin, f.propID, file, false)
	if err != nil || res.Created != 2 || res.Updated != 1 {
		t.Fatalf("import: %+v %v", res, err)
	}
	if a := f.byCode(t, "4650"); a.ParentCode != "4620" || a.StatementGroup != "REV_OOD" {
		t.Fatalf("child: %+v", a)
	}
	if f.byCode(t, "4110").Name != "Room revenue - walk-in" {
		t.Fatal("update by code")
	}
	// a file with any mistake changes nothing
	before := len(f.accounts(t, accounting.AccountFilter{}))
	for name, bad := range map[string]string{
		"unknown parent":     "code,name,type,parent_code,postable,group,active\n4660,Fine,REVENUE,,yes,REV_MISC,yes\n4670,Bad,REVENUE,NOPE,yes,REV_MISC,yes\n",
		"type change":        "code,name,type,parent_code,postable,group,active\n4660,Fine,REVENUE,,yes,REV_MISC,yes\n4110,Now an asset,ASSET,,yes,CASH,yes\n",
		"duplicate code":     "code,name,type,parent_code,postable,group,active\n4660,One,REVENUE,,yes,REV_MISC,yes\n4660,Two,REVENUE,,yes,REV_MISC,yes\n",
		"postable parent":    "code,name,type,parent_code,postable,group,active\n4660,Child,REVENUE,4210,yes,REV_MISC,yes\n",
		"parent type":        "code,name,type,parent_code,postable,group,active\n4660,Child,REVENUE,1100,yes,REV_MISC,yes\n",
		"a loop":             "code,name,type,parent_code,postable,group,active\n4660,A,REVENUE,4670,no,,yes\n4670,B,REVENUE,4660,no,,yes\n",
		"no group":           "code,name,type,parent_code,postable,group,active\n4660,No group,REVENUE,,yes,,yes\n",
		"bad yes/no":         "code,name,type,parent_code,postable,group,active\n4660,A,REVENUE,,maybe,REV_MISC,yes\n",
		"missing column":     "code,name\n4660,A\n",
		"empty":              "",
		"system account off": "code,name,type,parent_code,postable,group,active\n1210,Guest ledger,ASSET,1200,yes,RECEIVABLES,no\n",
	} {
		_, err := f.Accounting.ImportCSV(f.admin, f.propID, bad, false)
		if err == nil {
			t.Fatalf("%s must be refused", name)
		}
		if after := len(f.accounts(t, accounting.AccountFilter{})); after != before {
			t.Fatalf("%s changed the chart: %d accounts instead of %d", name, after, before)
		}
	}
	_, err = f.Accounting.ImportCSV(f.admin, f.propID, "code,name,type,parent_code,postable,group,active\n4660,A,REVENUE,4670,no,,yes\n4670,B,REVENUE,4660,no,,yes\n", false)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Accounting.ImportCSV(f.admin, f.propID, "code,name,type,parent_code,postable,group,active\n1210,Guest ledger,ASSET,1200,yes,RECEIVABLES,no\n", false)
	wantCode(t, err, "ACCOUNT_IN_USE")
	// a spreadsheet's byte order mark and the account list in any column order
	res, err = f.Accounting.ImportCSV(f.admin, f.propID, "\ufeffname,type,code,group\nMeeting catering,REVENUE,4245,REV_FB\n", false)
	if err != nil || res.Created != 1 {
		t.Fatalf("bom and column order: %+v %v", res, err)
	}
}

func TestAccountingPermissionsAndTheSettingsLock(t *testing.T) {
	f := setup(t)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermAccountingView)
	if list, err := f.Accounting.Accounts(viewer, f.propID, accounting.AccountFilter{}); err != nil || len(list) == 0 {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := f.Accounting.AccountMap(viewer, f.propID); err != nil {
		t.Fatal(err)
	}
	_, err := f.Accounting.CreateAccount(viewer, f.propID, accounting.AccountInput{Code: "4999", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_MISC"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Accounting.SetAccountMap(viewer, f.propID, []accounting.MapInput{{Key: "CASH", AccountID: 1}})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Accounting.ImportCSV(viewer, f.propID, "code,name,type\n", false)
	wantCode(t, err, "PERMISSION_DENIED")
	if err := f.Accounting.DeleteAccount(viewer, f.propID, f.byCode(t, "4110").ID); err == nil {
		t.Fatal("a viewer cannot delete")
	} else {
		wantCode(t, err, "PERMISSION_DENIED")
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Accounting.Accounts(nobody, f.propID, accounting.AccountFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Accounting.UnmappedCodes(nobody, f.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	manager := f.User(t, f.tenantID, f.propID, auth.PermAccountingView, auth.PermAccountingManage)
	if _, err := f.Accounting.CreateAccount(manager, f.propID, accounting.AccountInput{Code: "4999", Name: "x", AccountType: "REVENUE", StatementGroup: "REV_MISC"}); err != nil {
		t.Fatal(err)
	}
}

// Two people creating the same account, or putting two accounts under each other, at once.
func TestConcurrentChartEdits(t *testing.T) {
	f := setup(t)
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4888", Name: "Same", AccountType: "REVENUE", StatementGroup: "REV_MISC"})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "CODE_TAKEN")
		}
	}
	if ok != 1 {
		t.Fatalf("%d creations won", ok)
	}
	a, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4801", Name: "A", AccountType: "REVENUE", IsPostable: ptr(false)})
	must(t, err)
	b, err := f.Accounting.CreateAccount(f.admin, f.propID, accounting.AccountInput{Code: "4802", Name: "B", AccountType: "REVENUE", IsPostable: ptr(false)})
	must(t, err)
	var wg2 sync.WaitGroup
	loop := make([]error, 2)
	wg2.Add(2)
	go func() {
		defer wg2.Done()
		_, loop[0] = f.Accounting.UpdateAccount(f.admin, f.propID, a.ID, accounting.AccountPatch{ParentID: &b.ID})
	}()
	go func() {
		defer wg2.Done()
		_, loop[1] = f.Accounting.UpdateAccount(f.admin, f.propID, b.ID, accounting.AccountPatch{ParentID: &a.ID})
	}()
	wg2.Wait()
	if (loop[0] == nil) == (loop[1] == nil) {
		t.Fatalf("exactly one of two opposite moves wins: %v %v", loop[0], loop[1])
	}
	if f.Count(t, `SELECT count(*) FROM gl_accounts x JOIN gl_accounts y ON y.id = x.parent_id AND y.parent_id = x.id`) != 0 {
		t.Fatal("no loop in the chart")
	}
}

func TestAccountTablesGuardTheirRows(t *testing.T) {
	f := setup(t)
	for name, sql := range map[string]string{
		"a code of the right form":      `UPDATE gl_accounts SET code = 'bad code' WHERE code = '4110' AND property_id = $1`,
		"a known type":                  `UPDATE gl_accounts SET account_type = 'MAGIC' WHERE code = '4110' AND property_id = $1`,
		"a known side":                  `UPDATE gl_accounts SET normal_side = 'UP' WHERE code = '4110' AND property_id = $1`,
		"a known group":                 `UPDATE gl_accounts SET statement_group = 'NOPE' WHERE code = '4110' AND property_id = $1`,
		"not its own parent":            `UPDATE gl_accounts SET parent_id = id WHERE code = '4110' AND property_id = $1`,
		"a known system key":            `UPDATE gl_account_map SET map_key = 'PETTY' WHERE map_key = 'CASH' AND property_id = $1`,
		"a month of the year":           `UPDATE accounting_settings SET fiscal_year_start_month = 13 WHERE property_id = $1`,
		"an account that exists in map": `UPDATE gl_account_map SET account_id = 999999 WHERE map_key = 'CASH' AND property_id = $1`,
		"delete an account in the map":  `DELETE FROM gl_accounts WHERE code = '1110' AND property_id = $1`,
		"delete a parent with children": `DELETE FROM gl_accounts WHERE code = '4100' AND property_id = $1`,
	} {
		if err := f.Exec(t, sql, f.propID); err == nil {
			t.Fatalf("%s", name)
		}
	}
}
