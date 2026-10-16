package pos_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/pos"
	"kamarapms/internal/rooms/roomstest"
)

// The print agent, the documents and the drawer (docs/architecture/34-pos.md 10.3 and 20), on a real database.

type printFx struct {
	*kitchenFx
	recv  pos.Printer // the receipt printer of the outlet; its drawer opens
	dev   pos.DeviceIdentity
	token string
}

// enrol makes a device through the same two steps an agent takes: a code of the property, then the enrolment with it.
func (p *printFx) enrol(t *testing.T, name string) (pos.DeviceIdentity, string) {
	t.Helper()
	e, err := p.svc.CreateEnrollment(p.admin, p.propID)
	must(t, err)
	d, err := p.svc.Enroll(context.Background(), e.Code, name, "0.1.0")
	must(t, err)
	id, err := p.svc.AuthenticateDevice(context.Background(), d.Token)
	must(t, err)
	return id, d.Token
}

func (p *printFx) assign(t *testing.T, pr pos.Printer, dev int64) {
	t.Helper()
	_, err := p.svc.UpdatePrinter(p.admin, p.propID, pr.ID, pos.PrinterInput{Name: pr.Name, Kind: pr.Kind, ConnectionInfo: "127.0.0.1:9100", PaperWidthMM: pr.PaperWidthMM,
		Codepage: pr.Codepage, OpensDrawer: pr.OpensDrawer, DeviceID: &dev})
	must(t, err)
}

func newPrintFx(t *testing.T) *printFx {
	t.Helper()
	k := newKitchenFx(t)
	p := &printFx{kitchenFx: k}
	var err error
	p.recv, err = k.svc.CreatePrinter(k.admin, k.propID, pos.PrinterInput{Name: "Receipt", Kind: "RECEIPT", PaperWidthMM: 80, Codepage: "CP437", OpensDrawer: true})
	must(t, err)
	// the receipt printer is the outlet's; the cashier has an open shift for its drawer
	o, err := k.svc.GetOutlet(k.admin, k.propID, k.resto.ID)
	must(t, err)
	_, err = k.svc.UpdateOutlet(k.admin, k.propID, k.resto.ID, pos.OutletInput{Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: k.dept,
		RuleProfileID: k.k.profile.ID, PriceListID: k.k.priceList.ID, Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "PAYMENT_COMP",
		RoundingUnit: 100, AllowCompanyCharge: true, ReceiptPrinterID: &p.recv.ID, Version: o.Version})
	must(t, err)
	k.shift(t)
	p.dev, p.token = p.enrol(t, "Kasir Lobby")
	p.assign(t, p.recv, p.dev.ID)
	p.assign(t, k.printer, p.dev.ID)
	return p
}

func (p *printFx) claim(t *testing.T) *pos.ClaimedJob {
	t.Helper()
	j, err := p.svc.NextJob(context.Background(), p.dev, 0)
	must(t, err)
	return j
}

func (p *printFx) jobs(t *testing.T, where string, args ...any) int {
	t.Helper()
	return p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE property_id = `+itoa(p.propID)+` AND `+where, args...)
}

func printed(t *testing.T, p *printFx, j *pos.ClaimedJob) {
	t.Helper()
	_, err := p.svc.ReportResult(context.Background(), p.dev, j.ID, pos.JobResult{Status: "PRINTED", Attempt: j.Attempt, PrinterStatus: "ONLINE"})
	must(t, err)
}

func TestAnEnrolmentCodeWorksOnceAndATokenIsOnlyAHash(t *testing.T) {
	p := newPrintFx(t)
	e, err := p.svc.CreateEnrollment(p.admin, p.propID)
	must(t, err)
	if len(e.Code) != 9 || e.Seconds != 900 {
		t.Fatalf("code: %+v", e)
	}
	// who may not manage cannot make one
	_, err = p.svc.CreateEnrollment(p.waiterC, p.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	d, err := p.svc.Enroll(context.Background(), strings.ToLower(e.Code), "Dapur", "0.1.0")
	must(t, err)
	if !strings.HasPrefix(d.Token, "kpd_") || d.PollSeconds != 25 || d.PropertyID != p.propID {
		t.Fatalf("enrolled: %+v", d)
	}
	// the token is in no column: only its SHA-256 and a prefix to recognise it
	if p.Count(t, `SELECT count(*) FROM pos_devices WHERE token_hash = $1 OR token_prefix = $1`, d.Token) != 0 {
		t.Fatal("the token is stored")
	}
	// one code, one device; an unknown code and an old one are the same answer
	_, err = p.svc.Enroll(context.Background(), e.Code, "Again", "")
	wantCode(t, err, "ENROLLMENT_CODE_INVALID")
	_, err = p.svc.Enroll(context.Background(), "ZZZZ-ZZZZ", "X", "")
	wantCode(t, err, "ENROLLMENT_CODE_INVALID")
	old, err := p.svc.CreateEnrollment(p.admin, p.propID)
	must(t, err)
	p.Clock.Advance(16 * time.Minute)
	_, err = p.svc.Enroll(context.Background(), old.Code, "Late", "")
	wantCode(t, err, "ENROLLMENT_CODE_INVALID")
	// the list says who is there
	l, err := p.svc.ListDevices(p.admin, p.propID)
	must(t, err)
	if len(l.Data) != 2 || l.PendingEnrollments != 0 {
		t.Fatalf("devices: %+v", l)
	}
	// rotate: the old token stops, the new one works; the printers stay
	rot, err := p.svc.RotateDeviceToken(p.admin, p.propID, p.dev.ID)
	must(t, err)
	_, err = p.svc.AuthenticateDevice(context.Background(), p.token)
	wantCode(t, err, "DEVICE_TOKEN_INVALID")
	if _, err := p.svc.AuthenticateDevice(context.Background(), rot.Token); err != nil {
		t.Fatal(err)
	}
	// revoke: the agent is told 401 with a code it can act on, and cannot claim
	if _, err := p.svc.RevokeDevice(p.admin, p.propID, p.dev.ID); err != nil {
		t.Fatal(err)
	}
	_, err = p.svc.AuthenticateDevice(context.Background(), rot.Token)
	wantCode(t, err, "DEVICE_REVOKED")
	_, err = p.svc.RevokeDevice(p.admin, p.propID, p.dev.ID)
	wantCode(t, err, "DEVICE_REVOKED")
	_, err = p.svc.RotateDeviceToken(p.admin, p.propID, p.dev.ID)
	wantCode(t, err, "DEVICE_REVOKED")
	// a user's token is not a device's
	_, err = p.svc.AuthenticateDevice(context.Background(), "eyJhbGciOi.user.token")
	wantCode(t, err, "DEVICE_TOKEN_INVALID")
}

func TestJobsOfAPrinterComeInOrderAndALeaseThatRunsOutReturns(t *testing.T) {
	p := newPrintFx(t)
	p.sent(t)
	p.sent(t) // two tickets for the grill, two jobs for the one kitchen printer
	if n := p.jobs(t, `status = 'QUEUED'`); n != 2 {
		t.Fatalf("queued: %d", n)
	}
	first := p.claim(t)
	if first == nil || first.Kind != "TICKET" || first.Attempt != 1 || len(first.Payload) == 0 || first.ConnectionInfo != "127.0.0.1:9100" || first.PrinterID != p.printer.ID {
		t.Fatalf("claim: %+v", first)
	}
	if !bytes.Contains(first.Payload, []byte("Sate ayam")) || !bytes.Contains(first.Payload, []byte("RESTORAN")) {
		t.Fatalf("the ticket is not on the paper: %q", first.Payload)
	}
	// the printer is busy with the first: the second waits for it
	if p.claim(t) != nil {
		t.Fatal("jobs of one printer are in order: the second waits")
	}
	// the lease of 60 seconds runs out: the job returns to the queue with its attempt counted, and it is still the first
	p.Clock.Advance(61 * time.Second)
	again := p.claim(t)
	if again == nil || again.ID != first.ID || again.Attempt != 2 {
		t.Fatalf("after the lease: %+v", again)
	}
	// a report for the attempt that lost its lease is not accepted
	_, err := p.svc.ReportResult(context.Background(), p.dev, first.ID, pos.JobResult{Status: "PRINTED", Attempt: 1})
	wantCode(t, err, "JOB_LEASE_LOST")
	printed(t, p, again)
	second := p.claim(t)
	if second == nil || second.ID == first.ID {
		t.Fatalf("the next job: %+v", second)
	}
	printed(t, p, second)
	if p.claim(t) != nil {
		t.Fatal("nothing is left")
	}
	// the printer reported its status with the result
	l, err := p.svc.ListPrinters(p.admin, p.propID)
	must(t, err)
	for _, pr := range l {
		if pr.ID == p.printer.ID && (pr.State != "ONLINE" || pr.DeviceName == nil || *pr.DeviceName != "Kasir Lobby") {
			t.Fatalf("printer: %+v", pr)
		}
	}
	// silent for 90 seconds it reads as offline
	p.Clock.Advance(91 * time.Second)
	l, _ = p.svc.ListPrinters(p.admin, p.propID)
	for _, pr := range l {
		if pr.ID == p.printer.ID && pr.State != "OFFLINE" {
			t.Fatalf("printer after silence: %+v", pr)
		}
	}
}

func TestTwoPollersNeverTakeTheSameJob(t *testing.T) {
	p := newPrintFx(t)
	for range 4 {
		p.sent(t)
	}
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				j, err := p.svc.NextJob(context.Background(), p.dev, 0)
				if err != nil {
					t.Error(err)
					return
				}
				if j != nil {
					mu.Lock()
					got[j.ID]++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	for id, n := range got {
		if n != 1 {
			t.Fatalf("job %s was handed out %d times", id, n)
		}
	}
	// the printer holds one job at a time: exactly one of the polls got one
	if len(got) != 1 {
		t.Fatalf("one printer, one job at a time: %v", got)
	}
}

func TestAFailedJobRetriesThenFailsForGoodAndAReprintIsANewJobWithACopyMark(t *testing.T) {
	p := newPrintFx(t)
	p.sent(t)
	ctx := context.Background()
	j := p.claim(t)
	a, err := p.svc.ReportResult(ctx, p.dev, j.ID, pos.JobResult{Status: "FAILED", ErrorCode: "CONNECT_REFUSED", Attempt: 1, PrinterStatus: "OFFLINE"})
	must(t, err)
	if a.Status != "QUEUED" || a.Final {
		t.Fatalf("first failure: %+v", a)
	}
	// the same report again changes nothing
	b, err := p.svc.ReportResult(ctx, p.dev, j.ID, pos.JobResult{Status: "FAILED", ErrorCode: "CONNECT_REFUSED", Attempt: 1})
	must(t, err)
	if b.Status != "QUEUED" || b.Attempts != 1 {
		t.Fatalf("repeat: %+v", b)
	}
	if p.claim(t) != nil {
		t.Fatal("the job waits for its backoff of 10 seconds")
	}
	p.Clock.Advance(11 * time.Second)
	j2 := p.claim(t)
	if j2 == nil || j2.Attempt != 2 {
		t.Fatalf("second attempt: %+v", j2)
	}
	_, err = p.svc.ReportResult(ctx, p.dev, j2.ID, pos.JobResult{Status: "FAILED", ErrorCode: "CONNECT_REFUSED", Attempt: 2})
	must(t, err)
	p.Clock.Advance(30 * time.Second)
	if p.claim(t) != nil {
		t.Fatal("the second backoff is 60 seconds")
	}
	p.Clock.Advance(31 * time.Second)
	j3 := p.claim(t)
	if j3 == nil || j3.Attempt != 3 {
		t.Fatalf("third attempt: %+v", j3)
	}
	end, err := p.svc.ReportResult(ctx, p.dev, j3.ID, pos.JobResult{Status: "FAILED", ErrorCode: "PAPER_OUT", Attempt: 3, PrinterStatus: "PAPER_OUT"})
	must(t, err)
	if end.Status != "FAILED" || !end.Final {
		t.Fatalf("for good: %+v", end)
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.print_failed'`) != 1 {
		t.Fatal("a job that failed for good is audited")
	}
	if p.claim(t) != nil {
		t.Fatal("a failed job is not claimed again")
	}
	// the cashier sees it with its reprint
	failed, err := p.svc.FailedJobs(p.cashier, p.propID, p.resto.ID)
	must(t, err)
	if len(failed) != 1 || failed[0].ErrorCode != "PAPER_OUT" || failed[0].Kind != "TICKET" || failed[0].Attempts != 3 {
		t.Fatalf("failed: %+v", failed)
	}
	// a reprint needs a reason, and a waiter may not
	_, err = p.svc.ReprintJob(p.cashier, p.propID, failed[0].ID, pos.ReprintInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.svc.ReprintJob(p.waiterC, p.propID, failed[0].ID, pos.ReprintInput{Reason: "paper"})
	wantCode(t, err, "PERMISSION_DENIED")
	rp, err := p.svc.ReprintJob(p.cashier, p.propID, failed[0].ID, pos.ReprintInput{Reason: "paper was refilled"})
	must(t, err)
	if !rp.Created || rp.CopyNo != 2 {
		t.Fatalf("reprint: %+v", rp)
	}
	// the original is as it was; the new job points to it
	if p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE id = $1 AND status = 'FAILED' AND error_code = 'PAPER_OUT'`, failed[0].ID) != 1 {
		t.Fatal("the original is never changed")
	}
	if p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE id = $1 AND reprint_of = $2 AND reprint_reason = 'paper was refilled' AND reprint_by IS NOT NULL`, rp.ID, failed[0].ID) != 1 {
		t.Fatal("a reprint points to its original with its reason")
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.print_reprinted'`) != 1 {
		t.Fatal("a reprint is audited")
	}
	again := p.claim(t)
	if again == nil || again.ID != rp.ID || !bytes.Contains(again.Payload, []byte("COPY 2")) || !bytes.Contains(again.Payload, []byte("Sate ayam")) {
		t.Fatalf("the copy: %+v", again)
	}
	// the copy printed: the failed job is no longer on the list of the cashier (it was printed again)
	printed(t, p, again)
	failed, _ = p.svc.FailedJobs(p.cashier, p.propID, p.resto.ID)
	if len(failed) != 0 {
		t.Fatalf("it was printed again: %+v", failed)
	}
}

func TestAPaymentPrintsTheReceiptTheDrawerAndTheRoomSheet(t *testing.T) {
	p := newPrintFx(t)
	// the head of the paper: what the outlet says, and the property for what it leaves empty
	o, err := p.svc.GetOutlet(p.admin, p.propID, p.resto.ID)
	must(t, err)
	_, err = p.svc.UpdateOutlet(p.admin, p.propID, p.resto.ID, pos.OutletInput{Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: p.dept,
		RuleProfileID: p.k.profile.ID, PriceListID: p.k.priceList.ID, Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "PAYMENT_COMP",
		RoundingUnit: 100, AllowCompanyCharge: true, ReceiptPrinterID: &p.recv.ID, Version: o.Version,
		ReceiptTitle: ptr("Restoran Teratai"), ReceiptFooter: ptr("Terima kasih. Wi-Fi: teratai"), ReceiptCopies: ptr(2)})
	must(t, err)
	bill := p.open(t, p.cashier, nil)
	bill = p.add(t, p.cashier, bill, p.nasi, "1")
	bill = p.send(t, p.cashier, bill)
	// the bill before it is paid
	pb, err := p.svc.PrintBill(p.cashier, p.propID, bill.ID, pos.PrintBillInput{})
	must(t, err)
	if !pb.Created {
		t.Fatalf("pre-bill: %+v", pb)
	}
	pre := p.claimAll(t, "RECEIPT")
	if !bytes.Contains(pre, []byte("BUKAN STRUK")) || !bytes.Contains(pre, []byte("Restoran Teratai")) {
		t.Fatalf("the pre-bill says it is not a receipt: %q", pre)
	}
	res := p.pay(t, p.cashier, bill, "print-key-0001", pos.PayLine{Method: "CASH", Amount: dec("60500"), Tendered: ptr(dec("100000"))})
	if !res.Closed {
		t.Fatalf("closed: %+v", res)
	}
	// the drawer first, then the two copies of the receipt
	rows, err := p.Pool.Query(context.Background(), `SELECT kind, copy_no FROM pos_print_jobs WHERE property_id = $1 AND source_type = 'BILL' AND dedupe_key NOT LIKE 'PREBILL:%' ORDER BY seq`, p.propID)
	must(t, err)
	var seq []string
	for rows.Next() {
		var k string
		var c int
		must(t, rows.Scan(&k, &c))
		seq = append(seq, k+":"+itoa(int64(c)))
	}
	rows.Close()
	if strings.Join(seq, ",") != "DRAWER:1,RECEIPT:1,RECEIPT:2" {
		t.Fatalf("jobs of the payment: %v", seq)
	}
	d := p.claim(t)
	if d == nil || d.Kind != "DRAWER" || !bytes.Contains(d.Payload, []byte{0x1B, 0x70, 0x00}) {
		t.Fatalf("drawer: %+v", d)
	}
	printed(t, p, d)
	r1 := p.claim(t)
	if r1 == nil || r1.Kind != "RECEIPT" {
		t.Fatalf("receipt: %+v", r1)
	}
	for _, want := range []string{"Restoran Teratai", "R-0001", "Nasi goreng", "60.500", "Kembali", "39.500", "Terima kasih. Wi-Fi: teratai"} {
		if !bytes.Contains(r1.Payload, []byte(want)) {
			t.Fatalf("the receipt lacks %q: %q", want, r1.Payload)
		}
	}
	if bytes.Contains(r1.Payload, []byte("COPY")) || bytes.Contains(r1.Payload, []byte("BUKAN STRUK")) {
		t.Fatalf("an original has no copy mark: %q", r1.Payload)
	}
	printed(t, p, r1)
	// the same request again prints nothing more
	_, err = p.svc.PayBill(p.cashier, p.propID, bill.ID, "print-key-0001", pos.PayInput{Payments: []pos.PayLine{{Method: "CASH", Amount: dec("60500")}}})
	must(t, err)
	if n := p.jobs(t, `kind = 'RECEIPT' AND dedupe_key LIKE 'RECEIPT:%' AND reprint_of IS NULL`); n != 2 {
		t.Fatalf("receipts: %d", n)
	}
	// a card payment does not pulse the drawer
	b2 := p.open(t, p.cashier, nil)
	b2 = p.add(t, p.cashier, b2, p.tea, "1")
	b2 = p.send(t, p.cashier, b2)
	before := p.jobs(t, `kind = 'DRAWER'`)
	p.pay(t, p.cashier, b2, "print-key-0002", pos.PayLine{Method: "CARD", Amount: dec("18150")})
	if p.jobs(t, `kind = 'DRAWER'`) != before {
		t.Fatal("no cash, no pulse")
	}
	// the receipt again, after it was paid: a copy with a reason
	_, err = p.svc.PrintBill(p.cashier, p.propID, bill.ID, pos.PrintBillInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	cp, err := p.svc.PrintBill(p.cashier, p.propID, bill.ID, pos.PrintBillInput{Reason: "the guest asked"})
	must(t, err)
	if cp.CopyNo != 3 {
		t.Fatalf("copy: %+v", cp)
	}
}

// claimAll prints and returns the bytes of the jobs of a kind that are queued, until none is left of it.
func (p *printFx) claimAll(t *testing.T, kind string) []byte {
	t.Helper()
	var out []byte
	for range 6 {
		j := p.claim(t)
		if j == nil {
			break
		}
		if j.Kind == kind {
			out = append(out, j.Payload...)
		}
		printed(t, p, j)
	}
	return out
}

func TestAChargeToARoomPrintsTheSheetTheGuestSigns(t *testing.T) {
	p := newPrintFx(t)
	stay, _ := p.room(t, "402", "500000")
	bill := p.open(t, p.cashier, nil)
	bill = p.add(t, p.cashier, bill, p.nasi, "1")
	bill = p.send(t, p.cashier, bill)
	p.claimAll(t, "TICKET")
	res := p.pay(t, p.cashier, bill, "room-print-0001", pos.PayLine{Method: "ROOM", Amount: dec("60500"), StayID: &stay})
	if !res.Closed {
		t.Fatalf("closed: %+v", res.Bill)
	}
	var sheet, guest []byte
	for range 6 {
		j := p.claim(t)
		if j == nil {
			break
		}
		if j.Kind == "RECEIPT" && bytes.Contains(j.Payload, []byte("LEMBAR BEBAN KE KAMAR")) {
			sheet = j.Payload
		} else if j.Kind == "RECEIPT" {
			guest = j.Payload
		}
		printed(t, p, j)
	}
	if guest == nil || sheet == nil {
		t.Fatalf("two sheets for a charge to a room: guest %q sheet %q", guest, sheet)
	}
	for _, want := range []string{"KAMAR 402", "Tanda tangan tamu", "60.500", "____"} {
		if !bytes.Contains(sheet, []byte(want)) {
			t.Fatalf("the sheet lacks %q: %q", want, sheet)
		}
	}
	// no cash: no pulse
	if p.jobs(t, `kind = 'DRAWER'`) != 0 {
		t.Fatal("a charge to a room opens no drawer")
	}
}

func TestAnOutletWithoutCopiesPrintsNothingAndWithoutAPrinterTheBrowserPrints(t *testing.T) {
	p := newPrintFx(t)
	o, _ := p.svc.GetOutlet(p.admin, p.propID, p.resto.ID)
	zero := 0
	_, err := p.svc.UpdateOutlet(p.admin, p.propID, p.resto.ID, pos.OutletInput{Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: p.dept,
		RuleProfileID: p.k.profile.ID, PriceListID: p.k.priceList.ID, Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "PAYMENT_COMP",
		RoundingUnit: 100, AllowCompanyCharge: true, ReceiptPrinterID: &p.recv.ID, ReceiptCopies: &zero, Version: o.Version})
	must(t, err)
	bill := p.open(t, p.cashier, nil)
	bill = p.add(t, p.cashier, bill, p.tea, "1")
	bill = p.send(t, p.cashier, bill)
	p.pay(t, p.cashier, bill, "zero-copies-01", pos.PayLine{Method: "CASH", Amount: bill.Totals.CashPayable})
	if p.jobs(t, `kind = 'RECEIPT' AND source_type = 'BILL'`) != 0 || p.jobs(t, `kind = 'DRAWER'`) != 1 {
		t.Fatal("no copies: no receipt, the drawer still opens")
	}
	_, err = p.svc.UpdateOutlet(p.admin, p.propID, p.resto.ID, pos.OutletInput{Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: p.dept,
		RuleProfileID: p.k.profile.ID, PriceListID: p.k.priceList.ID, Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "PAYMENT_COMP",
		RoundingUnit: 100, AllowCompanyCharge: true, Version: o.Version + 1})
	must(t, err)
	b2 := p.open(t, p.cashier, nil)
	b2 = p.add(t, p.cashier, b2, p.tea, "1")
	b2 = p.send(t, p.cashier, b2)
	_, err = p.svc.PrintBill(p.cashier, p.propID, b2.ID, pos.PrintBillInput{})
	wantCode(t, err, "OUTLET_NO_RECEIPT_PRINTER")
}

func TestTheDrawerOpensWithoutASaleWithAReasonAndIsCounted(t *testing.T) {
	p := newPrintFx(t)
	_, err := p.svc.OpenDrawer(p.cashier, p.propID, p.resto.ID, "  ")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.svc.OpenDrawer(p.waiterC, p.propID, p.resto.ID, "change for the guest")
	wantCode(t, err, "PERMISSION_DENIED")
	v, err := p.svc.OpenDrawer(p.cashier, p.propID, p.resto.ID, "change for the guest")
	must(t, err)
	if v.ID == 0 || v.JobID == "" {
		t.Fatalf("opened: %+v", v)
	}
	j := p.claim(t)
	if j == nil || j.Kind != "DRAWER" || j.ID != v.JobID {
		t.Fatalf("the pulse: %+v", j)
	}
	if p.Count(t, `SELECT count(*) FROM pos_drawer_opens WHERE outlet_id = $1 AND reason = 'change for the guest' AND shift_id IS NOT NULL`, p.resto.ID) != 1 {
		t.Fatal("the open, its reason and its shift are recorded")
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.drawer_opened'`) != 1 {
		t.Fatal("the open is audited")
	}
	// it shows on the report of the shift (the X report of an open one): who, when and why
	var shiftID int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT id FROM cashier_shifts WHERE property_id = $1 AND shift_number = 'SHF1'`, p.propID).Scan(&shiftID))
	xr, err := p.Shifts.Report(p.admin, p.propID, shiftID)
	must(t, err)
	if xr.Kind != "X" || len(xr.DrawerOpens) != 1 || xr.DrawerOpens[0].Reason != "change for the guest" || xr.DrawerOpens[0].OpenedBy == "" {
		t.Fatalf("the X report of the shift: %+v", xr.DrawerOpens)
	}
	// it shows on the report of the outlet for the day
	rep, err := p.svc.Report(p.admin, p.propID, p.resto.ID, roomstest.BD, roomstest.BD)
	must(t, err)
	if len(rep.DrawerOpens) != 1 || rep.DrawerOpens[0].Reason != "change for the guest" || rep.DrawerOpens[0].OpenedBy == "" {
		t.Fatalf("report: %+v", rep.DrawerOpens)
	}
}

func TestADeviceSeesOnlyTheJobsOfItsPrinters(t *testing.T) {
	p := newPrintFx(t)
	p.sent(t)
	other, _ := p.enrol(t, "Bar PC") // a second agent that serves no printer
	if j, err := p.svc.NextJob(context.Background(), other, 0); err != nil || j != nil {
		t.Fatalf("a device with no printer is given nothing: %+v %v", j, err)
	}
	mine := p.claim(t)
	if mine == nil {
		t.Fatal("the owner claims")
	}
	_, err := p.svc.ReportResult(context.Background(), other, mine.ID, pos.JobResult{Status: "PRINTED", Attempt: mine.Attempt})
	wantCode(t, err, "PRINT_JOB_NOT_FOUND")
	cfg, err := p.svc.DeviceConfig(context.Background(), other)
	must(t, err)
	if len(cfg.Printers) != 0 {
		t.Fatalf("config: %+v", cfg)
	}
	cfg, err = p.svc.DeviceConfig(context.Background(), p.dev)
	must(t, err)
	if len(cfg.Printers) != 2 {
		t.Fatalf("config of the owner: %+v", cfg)
	}
}

func TestThePayloadsOfOldJobsAreRemovedAndTheRowsStay(t *testing.T) {
	p := newPrintFx(t)
	p.sent(t)
	j := p.claim(t)
	printed(t, p, j)
	p.sent(t) // a second one that nobody claims
	p.Clock.Advance(8 * 24 * time.Hour)
	n, err := p.svc.PurgePrintPayloads(context.Background())
	must(t, err)
	if n < 1 {
		t.Fatalf("purged %d", n)
	}
	if p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE payload IS NOT NULL OR document IS NOT NULL`) != 0 {
		t.Fatal("no payload is left")
	}
	if p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE payload_purged_at IS NOT NULL AND status IN ('PRINTED', 'FAILED')`) != 2 {
		t.Fatal("the rows stay: one printed, one that never printed and was failed as expired")
	}
	if p.Count(t, `SELECT count(*) FROM pos_print_jobs WHERE status = 'FAILED' AND error_code = 'EXPIRED'`) != 1 {
		t.Fatal("a job that never printed within the retention is failed")
	}
	_, err = p.svc.ReprintJob(p.cashier, p.propID, j.ID, pos.ReprintInput{Reason: "again"})
	wantCode(t, err, "PRINT_DOCUMENT_PURGED")
}

func TestThePermissionsOfPrinting(t *testing.T) {
	p := newPrintFx(t)
	for name, fn := range map[string]func(c context.Context) error{
		"enrol":   func(c context.Context) error { _, err := p.svc.CreateEnrollment(c, p.propID); return err },
		"revoke":  func(c context.Context) error { _, err := p.svc.RevokeDevice(c, p.propID, p.dev.ID); return err },
		"rotate":  func(c context.Context) error { _, err := p.svc.RotateDeviceToken(c, p.propID, p.dev.ID); return err },
		"devices": func(c context.Context) error { _, err := p.svc.ListDevices(c, p.propID); return err },
		"failed":  func(c context.Context) error { _, err := p.svc.FailedJobs(c, p.propID, p.resto.ID); return err },
		"drawer":  func(c context.Context) error { _, err := p.svc.OpenDrawer(c, p.propID, p.resto.ID, "x"); return err },
		"print": func(c context.Context) error {
			_, err := p.svc.PrintBill(c, p.propID, 1, pos.PrintBillInput{})
			return err
		},
	} {
		if err := fn(p.nobody); err == nil {
			t.Fatalf("%s: a user with no POS permission is refused", name)
		}
		if err := fn(p.waiterC); err == nil {
			t.Fatalf("%s: a waiter is refused", name)
		}
	}
}
