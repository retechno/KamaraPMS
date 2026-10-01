package cityledger_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
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
	adminEmail       string
	minibar          int64
	acme             companies.Company
	n                int
}

// setup: a property (IDR, no decimals) and ACME with a credit limit of 1,000,000.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, p.ID).Scan(&f.minibar))
	f.acme = f.company(t, "ACME", "1000000")
	return f
}

func (f *fx) company(t *testing.T, codeStr, limit string) companies.Company {
	t.Helper()
	c, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: codeStr, Name: codeStr + " Ltd", CreditLimit: limit, PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	return c
}

// folio adds an open folio with a balance of amount (a manual charge, no taxes on this charge code).
func (f *fx) folio(t *testing.T, amount string) int64 {
	t.Helper()
	f.n++
	var res, id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, $3, '2026-09-30', 'PHONE', 'DRAFT') RETURNING id`,
		f.tenantID, f.propID, "R-"+string(rune('A'+f.n))).Scan(&res))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		f.tenantID, f.propID, "F"+string(rune('A'+f.n)), res).Scan(&id))
	unit := amount
	_, err := f.Folios.PostCharge(f.admin, f.propID, id, "charge-"+string(rune('A'+f.n)), folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	return id
}

func (f *fx) transfer(folio, company int64, key, amount string) (folios.PaymentResult, error) {
	return f.Folios.Transfer(f.admin, f.propID, folio, key, folios.TransferInput{CompanyID: company, Amount: amount})
}

func (f *fx) receive(company int64, key, amount string) (cityledger.ReceiptResult, error) {
	return f.CityLedger.Receive(f.admin, f.propID, company, key, cityledger.ReceiptInput{Amount: amount, PaymentMethod: "CASH"})
}

func (f *fx) balance(t *testing.T, company int64) cityledger.Account {
	t.Helper()
	a, err := f.CityLedger.GetAccount(f.admin, f.propID, company)
	must(t, err)
	return a
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
}

func TestTransferAndReceive(t *testing.T) {
	f := setup(t)
	folio := f.folio(t, "400000")
	res, err := f.transfer(folio, f.acme.ID, "t1", "300000")
	must(t, err)
	if res.Payment.PaymentMethod != "CITY_LEDGER" || res.Payment.CompanyID == nil || *res.Payment.CompanyID != f.acme.ID || res.FolioBalance != "100000" {
		t.Fatalf("transfer: %+v", res)
	}
	if res.FolioItem.TransactionType != "PAYMENT" || res.FolioItem.Credit != "300000" {
		t.Fatalf("ledger entry: %+v", res.FolioItem)
	}
	acc := f.balance(t, f.acme.ID)
	if acc.Balance != "300000" || acc.Transferred != "300000" || acc.Received != "0" || acc.Available == nil || *acc.Available != "700000" {
		t.Fatalf("account: %+v", acc)
	}
	// the same key replays, another folio with the same key is refused
	again, err := f.transfer(folio, f.acme.ID, "t1", "300000")
	must(t, err)
	if again.Payment.ID != res.Payment.ID || f.Count(t, `SELECT count(*) FROM payments WHERE company_id IS NOT NULL`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	_, err = f.transfer(f.folio(t, "10"), f.acme.ID, "t1", "5")
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")

	r, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	if !strings.HasPrefix(r.Receipt.ReceiptNumber, "CLR") || r.Balance != "200000" || r.Receipt.Status != "POSTED" {
		t.Fatalf("receipt: %+v", r)
	}
	same, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	if same.Receipt.ID != r.Receipt.ID || f.Count(t, `SELECT count(*) FROM city_ledger_receipts`) != 1 {
		t.Fatalf("receipt replay: %+v", same)
	}
	_, err = f.receive(f.acme.ID, "r2", "200001")
	wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE")
	other := f.company(t, "OTHER", "")
	_, err = f.receive(other.ID, "r1", "1")
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
	if f.balance(t, f.acme.ID).Balance != "200000" || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('cityledger.transferred', 'cityledger.receipt_posted')`) != 2 {
		t.Fatal("balance and audit")
	}
}

func TestTransferRules(t *testing.T) {
	f := setup(t)
	folio := f.folio(t, "400000")
	_, err := f.transfer(folio, f.acme.ID, "a", "400001")
	wantCode(t, err, "TRANSFER_EXCEEDS_BALANCE")
	_, err = f.transfer(folio, f.acme.ID+999, "b", "1000")
	wantCode(t, err, "COMPANY_NOT_FOUND")
	_, err = f.transfer(folio, 0, "c", "1000")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.transfer(folio, f.acme.ID, "d", "0")
	wantCode(t, err, "VALIDATION_FAILED")

	// a limit of 0 is "no credit", an inactive company takes nothing
	none := f.company(t, "NONE", "0")
	_, err = f.transfer(folio, none.ID, "e", "1")
	wantCode(t, err, "CREDIT_LIMIT_EXCEEDED")
	inactive := f.company(t, "OLD", "")
	off := false
	_, err = f.Companies.Update(f.admin, f.propID, inactive.ID, companies.Patch{IsActive: &off})
	must(t, err)
	_, err = f.transfer(folio, inactive.ID, "f", "1")
	wantCode(t, err, "COMPANY_INACTIVE")

	// the limit is exact: 1,000,000 fits, one more does not; a company without a limit takes any amount
	big := f.folio(t, "2000000")
	_, err = f.transfer(big, f.acme.ID, "g", "1000001")
	wantCode(t, err, "CREDIT_LIMIT_EXCEEDED")
	_, err = f.transfer(big, f.acme.ID, "h", "1000000")
	must(t, err)
	_, err = f.transfer(folio, f.acme.ID, "i", "1")
	wantCode(t, err, "CREDIT_LIMIT_EXCEEDED")
	free := f.company(t, "FREE", "")
	_, err = f.transfer(big, free.ID, "j", "1000000")
	must(t, err)

	// it is not a payment method: no payment, deposit or refund takes it
	_, err = f.Folios.PostPayment(f.admin, f.propID, folio, "k", folios.PaymentInput{Amount: "1000", PaymentMethod: "CITY_LEDGER"})
	wantCode(t, err, "VALIDATION_FAILED")
	small := f.folio(t, "1000")
	_, err = f.transfer(small, free.ID, "l", "1000")
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM payments WHERE payment_method = 'CITY_LEDGER'`) != 3 {
		t.Fatal("three transfers expected")
	}
}

func TestTransferIsNotRefundableAndVoidKeepsReceiptsCovered(t *testing.T) {
	f := setup(t)
	folio := f.folio(t, "400000")
	tr, err := f.transfer(folio, f.acme.ID, "t1", "300000")
	must(t, err)
	_, err = f.Folios.Refund(f.admin, f.propID, tr.Payment.ID, "rf", folios.RefundInput{Amount: "1000", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_NOT_REFUNDABLE")

	_, err = f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	// 300,000 transferred, 100,000 paid: voiding the transfer would leave the receipt uncovered
	_, err = f.Folios.Void(f.admin, f.propID, tr.Payment.ID, folios.CorrectionInput{Reason: "oops", Approval: f.approval()})
	wantCode(t, err, "COMPANY_BALANCE_SETTLED")

	// void the receipt, then the transfer: the folio owes again and the account is empty
	vr, err := f.CityLedger.VoidReceipt(f.admin, f.propID, mustReceipt(t, f), cityledger.VoidInput{Reason: "wrong amount", Approval: f.approval()})
	must(t, err)
	if vr.Receipt.Status != "VOIDED" || vr.Balance != "300000" {
		t.Fatalf("void receipt: %+v", vr)
	}
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, vr.Receipt.ID, cityledger.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "RECEIPT_ALREADY_VOIDED")
	v, err := f.Folios.Void(f.admin, f.propID, tr.Payment.ID, folios.CorrectionInput{Reason: "oops", Approval: f.approval()})
	must(t, err)
	if v.Payment.Status != "VOIDED" || v.FolioBalance != "400000" {
		t.Fatalf("void: %+v", v)
	}
	if acc := f.balance(t, f.acme.ID); acc.Balance != "0" || acc.Transferred != "0" || acc.Received != "0" {
		t.Fatalf("account after voids: %+v", acc)
	}
}

func mustReceipt(t *testing.T, f *fx) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM city_ledger_receipts WHERE status = 'POSTED' ORDER BY id LIMIT 1`).Scan(&id))
	return id
}

func TestVoidReceiptNeedsAnApprovalAndTheSameBusinessDate(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "400000"), f.acme.ID, "t1", "300000")
	must(t, err)
	r, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r.Receipt.ID, cityledger.VoidInput{Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r.Receipt.ID, cityledger.VoidInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r.Receipt.ID+99, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "RECEIPT_NOT_FOUND")

	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, roomstest.BD, nil, json.RawMessage(`{}`))
		return err
	}))
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r.Receipt.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "CORRECTION_REQUIRES_ADJUSTMENT")
}

func TestReceiptsAreAppendOnly(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "400000"), f.acme.ID, "t1", "300000")
	must(t, err)
	r, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	if err := f.Exec(t, `UPDATE city_ledger_receipts SET amount = 1 WHERE id = $1`, r.Receipt.ID); err == nil {
		t.Fatal("a receipt's amount cannot change")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_receipts WHERE id = $1`, r.Receipt.ID); err == nil {
		t.Fatal("a receipt cannot be deleted")
	}
	if err := f.Exec(t, `TRUNCATE city_ledger_receipts`); err == nil {
		t.Fatal("receipts cannot be truncated")
	}
	if err := f.Exec(t, `UPDATE payments SET company_id = NULL WHERE company_id IS NOT NULL`); err == nil {
		t.Fatal("a transfer cannot lose its company")
	}
}

func TestPermissions(t *testing.T) {
	f := setup(t)
	folio := f.folio(t, "400000")
	teller := f.User(t, f.tenantID, f.propID, auth.PermPaymentPost, auth.PermFolioRead)
	_, err := f.Folios.Transfer(teller, f.propID, folio, "t1", folios.TransferInput{CompanyID: f.acme.ID, Amount: "1000"})
	wantCode(t, err, "PERMISSION_DENIED")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerTransfer)
	_, err = f.Folios.Transfer(clerk, f.propID, folio, "t1", folios.TransferInput{CompanyID: f.acme.ID, Amount: "100000"})
	must(t, err)
	_, err = f.CityLedger.Receive(clerk, f.propID, f.acme.ID, "r1", cityledger.ReceiptInput{Amount: "1", PaymentMethod: "CASH"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.CityLedger.GetAccount(clerk, f.propID, f.acme.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	reader := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	if a, err := f.CityLedger.GetAccount(reader, f.propID, f.acme.ID); err != nil || a.Balance != "100000" {
		t.Fatalf("reader: %+v %v", a, err)
	}
	_, err = f.Companies.Create(reader, f.propID, companies.Input{Code: "X", Name: "X", IsActive: true})
	wantCode(t, err, "PERMISSION_DENIED")
	if list, err := f.Companies.List(reader, f.propID, 0, nil, "", 10); err != nil || len(list) != 1 {
		t.Fatalf("reader lists companies: %v %v", list, err)
	}
	// another tenant's property is not found
	tn2 := f.Tenant(t, "XYZ")
	p2 := f.Property(t, tn2.ID, "OTHER")
	adm2, _ := f.AdminAccount(t, tn2.ID)
	_, err = f.CityLedger.GetAccount(adm2, f.propID, f.acme.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.CityLedger.GetAccount(adm2, p2.ID, f.acme.ID)
	wantCode(t, err, "COMPANY_NOT_FOUND")
}

func TestStatementAndAccountList(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "400000"), f.acme.ID, "t1", "300000")
	must(t, err)
	_, err = f.transfer(f.folio(t, "50000"), f.acme.ID, "t2", "50000")
	must(t, err)
	_, err = f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	r2, err := f.receive(f.acme.ID, "r2", "10000")
	must(t, err)
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r2.Receipt.ID, cityledger.VoidInput{Reason: "mistake", Approval: f.approval()})
	must(t, err)

	st, err := f.CityLedger.Statement(f.admin, f.propID, f.acme.ID, nil, nil)
	must(t, err)
	if st.OpeningBalance != "0" || st.TotalDebit != "350000" || st.TotalCredit != "100000" || st.ClosingBalance != "250000" || len(st.Lines) != 4 {
		t.Fatalf("statement: %+v", st)
	}
	// transfers first, then receipts; a voided receipt is listed but does not move the balance
	kinds := []string{"TRANSFER", "TRANSFER", "RECEIPT", "RECEIPT"}
	balances := []string{"300000", "350000", "250000", "250000"}
	for i, l := range st.Lines {
		if l.Kind != kinds[i] || l.Balance != balances[i] {
			t.Fatalf("line %d: %+v", i, l)
		}
	}
	if st.Lines[3].Status != "VOIDED" || st.Lines[0].FolioNumber == "" || st.Lines[0].ConfirmationNumber == "" {
		t.Fatalf("lines: %+v", st.Lines)
	}
	// a period after the movements opens with the balance and has no lines
	after := civil.MustParseDate("2026-10-05")
	st, err = f.CityLedger.Statement(f.admin, f.propID, f.acme.ID, &after, nil)
	must(t, err)
	if st.OpeningBalance != "250000" || st.ClosingBalance != "250000" || len(st.Lines) != 0 {
		t.Fatalf("later statement: %+v", st)
	}
	before := civil.MustParseDate("2026-09-01")
	st, err = f.CityLedger.Statement(f.admin, f.propID, f.acme.ID, nil, &before)
	must(t, err)
	if st.OpeningBalance != "0" || st.ClosingBalance != "0" || len(st.Lines) != 0 {
		t.Fatalf("earlier statement: %+v", st)
	}

	// accounts: ACME owes, EMPTY does not
	f.company(t, "EMPTY", "")
	all, err := f.CityLedger.Accounts(f.admin, f.propID, 0, false, "", 10)
	must(t, err)
	owing, err := f.CityLedger.Accounts(f.admin, f.propID, 0, true, "", 10)
	must(t, err)
	found, err := f.CityLedger.Accounts(f.admin, f.propID, 0, false, "empt", 10)
	must(t, err)
	if len(all) != 2 || len(owing) != 1 || owing[0].Code != "ACME" || owing[0].Balance != "250000" || len(found) != 1 || found[0].Code != "EMPTY" {
		t.Fatalf("accounts: %+v / %+v / %+v", all, owing, found)
	}
	// aging: everything is from today (0-30)
	ag, err := f.CityLedger.Aging(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if ag.Total != "250000" || ag.Buckets[0].Label != "0-30" || ag.Buckets[0].Amount != "250000" || ag.Buckets[3].Amount != "0" {
		t.Fatalf("aging: %+v", ag)
	}
}

func TestCompanyRules(t *testing.T) {
	f := setup(t)
	_, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "acme", Name: "Again", IsActive: true})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.Companies.Create(f.admin, f.propID, companies.Input{Code: "bad code", Name: "X", IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Companies.Create(f.admin, f.propID, companies.Input{Code: "NEG", Name: "X", CreditLimit: "-1", IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Companies.Create(f.admin, f.propID, companies.Input{Code: "DEC", Name: "X", CreditLimit: "1.5", IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED") // IDR has no decimals
	_, err = f.Companies.Get(f.admin, f.propID, 9999)
	wantCode(t, err, "COMPANY_NOT_FOUND")

	_, err = f.transfer(f.folio(t, "400000"), f.acme.ID, "t1", "100000")
	must(t, err)
	off := false
	_, err = f.Companies.Update(f.admin, f.propID, f.acme.ID, companies.Patch{IsActive: &off})
	wantCode(t, err, "COMPANY_HAS_BALANCE")
	_, err = f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	got, err := f.Companies.Update(f.admin, f.propID, f.acme.ID, companies.Patch{IsActive: &off})
	must(t, err)
	if got.IsActive {
		t.Fatal("deactivated")
	}
	// lowering the limit below the balance is allowed; clearing it removes the limit
	empty := ""
	got, err = f.Companies.Update(f.admin, f.propID, f.acme.ID, companies.Patch{CreditLimit: &empty})
	must(t, err)
	if got.CreditLimit != nil {
		t.Fatalf("limit: %v", *got.CreditLimit)
	}
}

func TestCashierReportCountsReceiptsNotTransfers(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "400000"), f.acme.ID, "t1", "300000")
	must(t, err)
	_, err = f.CityLedger.Receive(f.admin, f.propID, f.acme.ID, "r1", cityledger.ReceiptInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	rep, err := f.Reports.Cashier(f.admin, f.propID, roomstest.BD, roomstest.BD)
	must(t, err)
	var cash, ledger string
	for _, m := range rep.ByMethod {
		switch m.Method {
		case "CASH":
			cash = m.Net
		case "CITY_LEDGER":
			ledger = m.Net
		}
	}
	if cash != "100000" || ledger != "300000" || rep.Net != "100000" {
		t.Fatalf("cashier: %+v", rep)
	}
}

// Two transfers that each fit the credit limit alone but not together: the company row lock lets exactly one in.
func TestCreditLimitHoldsUnderRace(t *testing.T) {
	f := setup(t)
	small := f.company(t, "SMALL", "150000")
	const n = 6
	folioIDs := make([]int64, n)
	for i := range folioIDs {
		folioIDs[i] = f.folio(t, "100000")
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.transfer(folioIDs[i], small.ID, "race-"+string(rune('a'+i)), "100000")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "CREDIT_LIMIT_EXCEEDED")
		}
	}
	if ok != 1 || f.balance(t, small.ID).Balance != "100000" {
		t.Fatalf("%d transfers fit a limit of 150000 (balance %s)", ok, f.balance(t, small.ID).Balance)
	}
}

// Receipts together cannot exceed the balance, however many arrive at once.
func TestReceiptsCannotExceedTheBalanceUnderRace(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "100000"), f.acme.ID, "t1", "100000")
	must(t, err)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.receive(f.acme.ID, "rr-"+string(rune('a'+i)), "60000")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE")
		}
	}
	if ok != 1 || f.balance(t, f.acme.ID).Balance != "40000" {
		t.Fatalf("%d receipts of 60000 fit 100000 (balance %s)", ok, f.balance(t, f.acme.ID).Balance)
	}
}

// The same Idempotency-Key sent twice at once records one receipt.
func TestReceiptKeyRace(t *testing.T) {
	f := setup(t)
	_, err := f.transfer(f.folio(t, "100000"), f.acme.ID, "t1", "100000")
	must(t, err)
	var wg sync.WaitGroup
	ids := make([]int64, 4)
	errs := make([]error, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r cityledger.ReceiptResult
			r, errs[i] = f.receive(f.acme.ID, "same", "10000")
			ids[i] = r.Receipt.ID
		}()
	}
	wg.Wait()
	for i := range ids {
		must(t, errs[i])
		if ids[i] != ids[0] {
			t.Fatalf("ids %v", ids)
		}
	}
	if f.Count(t, `SELECT count(*) FROM city_ledger_receipts`) != 1 {
		t.Fatal("one receipt")
	}
}
