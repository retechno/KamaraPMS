package pos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
	"kamarapms/internal/pos/printing"
)

// The documents of the paper and their queue (docs/architecture/34-pos.md 10.3 and 20). The server writes a document, renders it to ESC/POS
// bytes for the printer it is meant for, and queues it. The agent carries the bytes; it never decides what is on the paper.

// PayloadRetention is how long the document and the bytes of a job are kept; the row (who, what, when, how it ended) stays.
const PayloadRetention = 7 * 24 * time.Hour

// paper is what the outlet puts at the head and the foot of its papers, with what it leaves empty taken from the property.
type paper struct {
	header   printing.Header
	footer   string
	loc      *time.Location
	decimals int
}

func (s *Service) paperOf(ctx context.Context, tenantID, propertyID int64, o Outlet) (paper, error) {
	pp, err := s.q(ctx).PropertyPaper(ctx, posdb.PropertyPaperParams{TenantID: tenantID, ID: propertyID})
	if err != nil {
		return paper{}, err
	}
	loc, err := time.LoadLocation(pp.Timezone)
	if err != nil {
		loc = time.UTC
	}
	address := pp.Address
	if pp.City != "" {
		address = strings.TrimSpace(address + ", " + pp.City)
	}
	pick := func(own *string, fallback string) string {
		if own != nil && strings.TrimSpace(*own) != "" {
			return strings.TrimSpace(*own)
		}
		return fallback
	}
	return paper{
		header: printing.Header{Hotel: pick(o.ReceiptHotel, pp.Name), Address: pick(o.ReceiptAddress, address), TaxID: pick(o.ReceiptTaxID, pp.TaxID), Outlet: pick(o.ReceiptTitle, o.Name)},
		footer: pick(o.ReceiptFooter, ""), loc: loc, decimals: int(pp.CurrencyDecimals),
	}, nil
}

// ---------------------------------------------------------------- queueing

type jobSpec struct {
	PrinterID  int64
	Kind       string
	SourceType string
	SourceID   int64
	DedupeKey  string
	CopyNo     int
	Doc        printing.Document
	ReprintOf  pgtype.UUID
	Mark       string // the line on top of a copy, "COPY 2" (set for a reprint)
	Reason     string
	By         *int64
}

// enqueue renders a document for a printer and queues it. The same dedupe key again is no second job (created is false). A printer that is
// not in use takes no job. A document too large for the payload limit is logged and skipped: a problem of paper never stops a sale.
func (s *Service) enqueue(ctx context.Context, tenantID, propertyID int64, spec jobSpec) (id pgtype.UUID, created bool, err error) {
	q := s.q(ctx)
	pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: propertyID, ID: spec.PrinterID})
	if err != nil {
		return pgtype.UUID{}, false, orNotFound(err, notFound("PRINTER_NOT_FOUND", "printer"))
	}
	if !pr.IsActive {
		return pgtype.UUID{}, false, nil
	}
	mark := spec.Mark
	if mark == "" && spec.ReprintOf.Valid {
		mark = fmt.Sprintf("COPY %d", spec.CopyNo)
	}
	bytes, err := printing.Render(spec.Doc, printing.Profile{WidthMM: int(pr.PaperWidthMm), Codepage: pr.Codepage}, mark)
	if err != nil {
		slog.Default().WarnContext(ctx, "pos: a document was not queued", "kind", spec.Kind, "source", spec.SourceID, "error", err)
		return pgtype.UUID{}, false, nil
	}
	doc, err := json.Marshal(spec.Doc)
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	var reason *string
	if spec.Reason != "" {
		reason = &spec.Reason
	}
	var by *int64
	if spec.ReprintOf.Valid {
		by = spec.By
	}
	if spec.ReprintOf.Valid && reason == nil {
		return pgtype.UUID{}, false, errors.New("a reprint has a reason")
	}
	id, err = q.InsertPrintJob(ctx, posdb.InsertPrintJobParams{TenantID: tenantID, PropertyID: propertyID, PrinterID: spec.PrinterID, Kind: spec.Kind, SourceType: spec.SourceType,
		SourceID: spec.SourceID, DedupeKey: spec.DedupeKey, CopyNo: int16(spec.CopyNo), Document: doc, Payload: bytes, ReprintOf: spec.ReprintOf, //nolint:gosec // G115: a few copies
		ReprintReason: reason, ReprintBy: by, CreatedBy: spec.By, CreatedAt: s.clock.Now()})
	if errors.Is(err, pgx.ErrNoRows) { // the same document again: one job
		return pgtype.UUID{}, false, nil
	}
	return id, err == nil, err
}

// ---------------------------------------------------------------- kitchen tickets

func (s *Service) ticketDocument(ctx context.Context, tenantID, propertyID, ticketID int64) (printing.Document, error) {
	q := s.q(ctx)
	t, err := q.TicketForPrint(ctx, posdb.TicketForPrintParams{TenantID: tenantID, PropertyID: propertyID, ID: ticketID})
	if err != nil {
		return printing.Document{}, orNotFound(err, notFound("TICKET_NOT_FOUND", "ticket"))
	}
	lines, err := q.StationBoardLines(ctx, posdb.StationBoardLinesParams{TenantID: tenantID, PropertyID: propertyID, TicketIds: []int64{ticketID}})
	if err != nil {
		return printing.Document{}, err
	}
	loc := time.UTC
	if pp, err := q.PropertyPaper(ctx, posdb.PropertyPaperParams{TenantID: tenantID, ID: propertyID}); err == nil {
		if l, err := time.LoadLocation(pp.Timezone); err == nil {
			loc = l
		}
	}
	l := printing.LabelsID
	place := ""
	switch {
	case t.TableCode != "":
		place = l.Table + " " + t.TableCode
	case t.RoomNumber != "":
		place = l.Room + " " + t.RoomNumber
	case t.CustomerName != "":
		place = t.CustomerName
	case t.OrderType == OrderTakeaway:
		place = "BAWA PULANG"
	}
	in := printing.TicketInput{Outlet: t.OutletName, Station: t.StationName, BillNumber: t.BillNumber, Round: int(t.RoundNo), TicketNo: t.ID, Place: place, Waiter: t.WaiterName,
		SentAt: t.CreatedAt.In(loc).Format("15:04"), Addition: t.RoundNo > 1, Cancel: t.Kind == "VOID"}
	for _, ln := range lines {
		in.Lines = append(in.Lines, printing.TicketLine{Qty: printing.Qty(ln.Quantity), Name: ln.Name, Options: ln.OptionsText, Note: ln.Note, Void: ln.IsVoid})
	}
	return printing.Ticket(in, l), nil
}

// ---------------------------------------------------------------- receipts

type paidEvent struct {
	Cash     bool
	Tendered decimal.Decimal
	Change   decimal.Decimal
	Room     bool
}

func methodLabel(l printing.Labels, m string) string {
	switch m {
	case PayCash:
		return l.Cash
	case PayCard:
		return l.Card
	case PayQRIS:
		return l.QRIS
	case PayBankTransfer:
		return l.Bank
	case PayCompany, "CITY_LEDGER":
		return l.Company
	case PayComp:
		return l.Comp
	}
	return l.Other
}

func (s *Service) receiptInput(b Bill, o Outlet, pp paper, cashier string, at time.Time, prebill bool, change decimal.Decimal) printing.ReceiptInput {
	l := printing.LabelsID
	m := func(d decimal.Decimal) string { return printing.FormatMoney(d, pp.decimals) }
	in := printing.ReceiptInput{Header: pp.header, Footer: pp.footer, BillNumber: b.Number, Waiter: b.WaiterName, Cashier: cashier, At: at.In(pp.loc).Format("02/01/2006 15:04"), Prebill: prebill}
	switch {
	case b.TableCode != "":
		in.Place = l.Table + " " + b.TableCode
	case b.TargetRoom != "":
		in.Place = l.Room + " " + b.TargetRoom
	case b.CustomerName != "":
		in.Place = b.CustomerName
	case b.OrderType == OrderTakeaway:
		in.Place = "Bawa pulang"
	}
	if b.GuestCount != nil && *b.GuestCount > 0 {
		in.Guests = fmt.Sprintf("%d tamu", *b.GuestCount)
	}
	for _, ln := range b.Lines {
		if ln.Status == LineVoid {
			continue
		}
		var opts []string
		for _, op := range ln.Options {
			if op.Qty > 1 {
				opts = append(opts, fmt.Sprintf("%s x%d", op.Name, op.Qty))
			} else {
				opts = append(opts, op.Name)
			}
		}
		in.Lines = append(in.Lines, printing.ReceiptLine{Qty: printing.Qty(ln.Quantity), Name: ln.ProductName, Options: strings.Join(opts, ", "), Note: ln.Note, Amount: m(ln.Net)})
	}
	in.Summary = []printing.Row{{Label: l.Net, Amount: m(b.Totals.Net)}, {Label: l.Service, Amount: m(b.Totals.Service)}, {Label: l.Tax, Amount: m(b.Totals.Tax)}}
	if !b.Totals.Rounding.IsZero() {
		in.Summary = append(in.Summary, printing.Row{Label: l.Rounding, Amount: m(b.Totals.Rounding)})
	}
	if prebill {
		in.TotalLabel, in.Total = l.ToPay, m(b.Totals.Payable)
		if b.Totals.Paid.IsPositive() {
			in.Balance = m(b.Totals.Payable)
		}
	} else {
		in.TotalLabel, in.Total = l.Total, m(b.Totals.Sold.Add(b.Totals.Rounding))
	}
	for _, p := range b.Payments {
		if p.Status == "POSTED" {
			in.Payments = append(in.Payments, printing.Row{Label: methodLabel(l, p.Method), Amount: m(p.Amount)})
		}
	}
	for _, c := range b.RoomCharges {
		if c.ReturnedAt == nil {
			in.Payments = append(in.Payments, printing.Row{Label: l.RoomCharge + " " + c.Room, Amount: m(c.Amount)})
		}
	}
	if change.IsPositive() {
		in.Change = m(change)
	}
	if !prebill && b.Status == BillOpen && b.Totals.Payable.IsPositive() { // a part payment: what is left
		in.Balance = m(b.Totals.Payable)
	}
	return in
}

// queueReceiptJobs prints what a payment makes: the pulse of the drawer for cash, the receipt (copies as the outlet says), and for a charge to a
// room the sheet the guest signs. Without a receipt printer nothing is queued: the browser prints it from the bill.
func (s *Service) queueReceiptJobs(ctx context.Context, p auth.Principal, propertyID int64, o Outlet, billID int64, ev paidEvent) error {
	if o.ReceiptPrinterID == nil {
		return nil
	}
	q := s.q(ctx)
	pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: propertyID, ID: *o.ReceiptPrinterID})
	if err != nil || !pr.IsActive {
		return orNotFound(err, nil)
	}
	by := p.ActorID()
	if ev.Cash && pr.OpensDrawer {
		n, err := q.ReceiptRound(ctx, posdb.ReceiptRoundParams{PropertyID: propertyID, BillID: billID, Kind: printing.KindDrawer, KeyPrefix: fmt.Sprintf("DRAWER:%d:", billID)})
		if err != nil {
			return err
		}
		if _, _, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: pr.ID, Kind: printing.KindDrawer, SourceType: "BILL", SourceID: billID,
			DedupeKey: fmt.Sprintf("DRAWER:%d:%d", billID, n+1), CopyNo: 1, Doc: printing.Drawer(), By: by}); err != nil {
			return err
		}
	}
	if o.ReceiptCopies < 1 {
		return nil
	}
	b, err := s.loadBill(ctx, p, propertyID, billID)
	if err != nil {
		return err
	}
	pp, err := s.paperOf(ctx, p.TenantID, propertyID, o)
	if err != nil {
		return err
	}
	cashier, _ := q.UserName(ctx, p.UserID)
	now := s.clock.Now()
	in := s.receiptInput(b, o, pp, cashier, now, false, ev.Change)
	doc := printing.Receipt(in, printing.LabelsID)
	n, err := q.ReceiptRound(ctx, posdb.ReceiptRoundParams{PropertyID: propertyID, BillID: billID, Kind: printing.KindReceipt, KeyPrefix: fmt.Sprintf("RECEIPT:%d:", billID)})
	if err != nil {
		return err
	}
	k := n + 1
	for c := 1; c <= o.ReceiptCopies; c++ {
		if _, _, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: pr.ID, Kind: printing.KindReceipt, SourceType: "BILL", SourceID: billID,
			DedupeKey: fmt.Sprintf("RECEIPT:%d:%d:%d", billID, k, c), CopyNo: c, Doc: doc, By: by}); err != nil {
			return err
		}
	}
	if ev.Room {
		var stay *int64
		room := ""
		amount := decimal.Zero
		for _, c := range b.RoomCharges {
			if c.ReturnedAt == nil {
				stay, room = c.StayID, c.Room
				amount = amount.Add(c.Amount)
			}
		}
		guest := ""
		if stay != nil {
			guest, _ = q.GuestOfStay(ctx, posdb.GuestOfStayParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: *stay})
		}
		sheet := printing.RoomChargeSheet(in, printing.RoomSheet{Room: room, Guest: guest, Amount: printing.FormatMoney(amount, pp.decimals)}, printing.LabelsID)
		if _, _, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: pr.ID, Kind: printing.KindReceipt, SourceType: "BILL", SourceID: billID,
			DedupeKey: fmt.Sprintf("RECEIPT:%d:%d:ROOM", billID, k), CopyNo: o.ReceiptCopies + 1, Doc: sheet, By: by}); err != nil {
			return err
		}
	}
	return nil
}

// queuePrebill prints the bill before it is paid, marked "BUKAN STRUK".
func (s *Service) queuePrebill(ctx context.Context, p auth.Principal, propertyID int64, o Outlet, billID int64) error {
	if o.ReceiptPrinterID == nil {
		return nil
	}
	q := s.q(ctx)
	b, err := s.loadBill(ctx, p, propertyID, billID)
	if err != nil {
		return err
	}
	pp, err := s.paperOf(ctx, p.TenantID, propertyID, o)
	if err != nil {
		return err
	}
	cashier, _ := q.UserName(ctx, p.UserID)
	n, err := q.ReceiptRound(ctx, posdb.ReceiptRoundParams{PropertyID: propertyID, BillID: billID, Kind: printing.KindReceipt, KeyPrefix: fmt.Sprintf("PREBILL:%d:", billID)})
	if err != nil {
		return err
	}
	doc := printing.Receipt(s.receiptInput(b, o, pp, cashier, s.clock.Now(), true, decimal.Zero), printing.LabelsID)
	_, _, err = s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: *o.ReceiptPrinterID, Kind: printing.KindReceipt, SourceType: "BILL", SourceID: billID,
		DedupeKey: fmt.Sprintf("PREBILL:%d:%d", billID, n+1), CopyNo: 1, Doc: doc, By: p.ActorID()})
	return err
}

// ---------------------------------------------------------------- reprint and the failed jobs

// FailedJob is a job that did not print, as the cashier sees it with its Reprint.
type FailedJob struct {
	ID          string    `json:"id"`
	PrinterID   int64     `json:"printer_id"`
	PrinterName string    `json:"printer_name"`
	Kind        string    `json:"kind"`
	SourceType  string    `json:"source_type"`
	SourceID    int64     `json:"source_id"`
	BillID      int64     `json:"bill_id,omitempty"`
	BillNumber  string    `json:"bill_number,omitempty"`
	CopyNo      int       `json:"copy_no"`
	Attempts    int       `json:"attempts"`
	ErrorCode   string    `json:"error_code"`
	CreatedAt   time.Time `json:"created_at"`
}

// FailedJobs lists the jobs of an outlet that failed for good in the last two days and were not printed again (pos.cashier or pos.manage).
func (s *Service) FailedJobs(ctx context.Context, propertyID, outletID int64) ([]FailedJob, error) {
	p, err := s.canRead(ctx, propertyID, auth.PermPOSCashier, auth.PermPOSManage)
	if err != nil {
		return nil, err
	}
	if _, err := s.outletOf(ctx, p.TenantID, propertyID, outletID); err != nil {
		return nil, err
	}
	s.maybePurgePayloads(ctx)
	rows, err := s.q(ctx).FailedJobsOfOutlet(ctx, posdb.FailedJobsOfOutletParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, Since: s.clock.Now().Add(-48 * time.Hour)})
	if err != nil {
		return nil, err
	}
	out := make([]FailedJob, len(rows))
	for i, r := range rows {
		out[i] = FailedJob{ID: uuidString(r.ID), PrinterID: r.PrinterID, PrinterName: r.PrinterName, Kind: r.Kind, SourceType: r.SourceType, SourceID: r.SourceID, BillID: r.BillID,
			BillNumber: r.BillNumber, CopyNo: int(r.CopyNo), Attempts: int(r.Attempts), ErrorCode: r.ErrorCode, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// ReprintInput asks for a job to be printed again: the reason is written on the audit entry; PrinterID, when it is set, sends the copy to another printer.
type ReprintInput struct {
	Reason    string `json:"reason"`
	PrinterID *int64 `json:"printer_id"`
}

// JobCreated answers a reprint or a print of a bill.
type JobCreated struct {
	ID      string `json:"id"`
	CopyNo  int    `json:"copy_no"`
	Created bool   `json:"created"`
}

// ReprintJob prints a job again (pos.cashier): a new job that points to the original, with the same document and "COPY n" on top. The original
// is never changed. A reason is required and audited.
func (s *Service) ReprintJob(ctx context.Context, propertyID int64, jobID string, in ReprintInput) (JobCreated, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return JobCreated{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if n := len([]rune(reason)); n < 1 || n > maxReasonLen {
		return JobCreated{}, invalid("reprint", fieldErr("reason", "REQUIRED", "the reason, up to 200 characters"))
	}
	u, fe := parseUUID("id", jobID)
	if fe != nil {
		return JobCreated{}, notFound("PRINT_JOB_NOT_FOUND", "print job")
	}
	var out JobCreated
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		j, err := q.JobToReprint(ctx, posdb.JobToReprintParams{TenantID: p.TenantID, PropertyID: propertyID, ID: u})
		if err != nil {
			return orNotFound(err, notFound("PRINT_JOB_NOT_FOUND", "print job"))
		}
		if j.Kind == printing.KindDrawer {
			return apperr.Conflict("PRINT_JOB_NOT_REPRINTABLE", "a drawer pulse is not printed again: open the drawer")
		}
		var doc printing.Document
		switch {
		case j.PayloadPurgedAt != nil || len(j.Document) == 0:
			return apperr.Conflict("PRINT_DOCUMENT_PURGED", "the document of this job was removed after seven days: print the bill again from the bill")
		default:
			if err := json.Unmarshal(j.Document, &doc); err != nil {
				return err
			}
		}
		printer := j.PrinterID
		if in.PrinterID != nil {
			pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: propertyID, ID: *in.PrinterID})
			if err != nil {
				return orNotFound(err, invalid("reprint", fieldErr("printer_id", "NOT_FOUND", "no such printer in this property")))
			}
			printer = pr.ID
		}
		m, err := q.MaxCopyOfSource(ctx, posdb.MaxCopyOfSourceParams{PropertyID: propertyID, SourceType: j.SourceType, SourceID: j.SourceID, Kind: j.Kind})
		if err != nil {
			return err
		}
		copyNo := int(m) + 1
		id, created, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: printer, Kind: j.Kind, SourceType: j.SourceType, SourceID: j.SourceID,
			DedupeKey: fmt.Sprintf("REPRINT:%s:%d", jobID, copyNo), CopyNo: copyNo, Doc: doc, ReprintOf: u, Reason: reason, By: p.ActorID()})
		if err != nil {
			return err
		}
		out = JobCreated{ID: uuidString(id), CopyNo: copyNo, Created: created}
		pid := propertyID
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &pid, UserID: p.ActorID(), Action: "pos.print_reprinted", EntityType: "pos_printer", EntityID: printer,
			EntityLabel: j.Kind, New: map[string]any{"job": jobID, "kind": j.Kind, "copy_no": copyNo, "reason": reason, "source_type": j.SourceType, "source_id": j.SourceID}})
	})
	return out, err
}

// PrintBillInput asks for the paper of a bill: a pre-bill while it is open, a copy of the receipt after it is paid (with a reason).
type PrintBillInput struct {
	Reason string `json:"reason"`
}

// PrintBill queues the paper of a bill at the receipt printer of its outlet (pos.cashier): the pre-bill of an open bill, "BUKAN STRUK"; the
// receipt of a closed bill again, with "COPY n" and a reason that is audited.
func (s *Service) PrintBill(ctx context.Context, propertyID, billID int64, in PrintBillInput) (JobCreated, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return JobCreated{}, err
	}
	r, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return JobCreated{}, err
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, r.OutletID)
	if err != nil {
		return JobCreated{}, err
	}
	if o.ReceiptPrinterID == nil {
		return JobCreated{}, apperr.Conflict("OUTLET_NO_RECEIPT_PRINTER", "this outlet has no receipt printer: print from the browser")
	}
	reason := strings.TrimSpace(in.Reason)
	if r.Status != BillOpen && (reason == "" || len([]rune(reason)) > maxReasonLen) {
		return JobCreated{}, invalid("print", fieldErr("reason", "REQUIRED", "the reason a receipt is printed again, up to 200 characters"))
	}
	var out JobCreated
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		b, err := s.loadBill(ctx, p, propertyID, billID)
		if err != nil {
			return err
		}
		pp, err := s.paperOf(ctx, p.TenantID, propertyID, o)
		if err != nil {
			return err
		}
		cashier, _ := q.UserName(ctx, p.UserID)
		if b.Status == BillOpen {
			n, err := q.ReceiptRound(ctx, posdb.ReceiptRoundParams{PropertyID: propertyID, BillID: billID, Kind: printing.KindReceipt, KeyPrefix: fmt.Sprintf("PREBILL:%d:", billID)})
			if err != nil {
				return err
			}
			doc := printing.Receipt(s.receiptInput(b, o, pp, cashier, s.clock.Now(), true, decimal.Zero), printing.LabelsID)
			id, created, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: *o.ReceiptPrinterID, Kind: printing.KindReceipt, SourceType: "BILL", SourceID: billID,
				DedupeKey: fmt.Sprintf("PREBILL:%d:%d", billID, n+1), CopyNo: 1, Doc: doc, By: p.ActorID()})
			out = JobCreated{ID: uuidString(id), CopyNo: 1, Created: created}
			return err
		}
		if b.Status != BillClosed {
			return apperr.Conflict("BILL_NOT_PRINTABLE", "a voided or merged bill has no receipt").WithContext("status", b.Status)
		}
		m, err := q.MaxCopyOfSource(ctx, posdb.MaxCopyOfSourceParams{PropertyID: propertyID, SourceType: "BILL", SourceID: billID, Kind: printing.KindReceipt})
		if err != nil {
			return err
		}
		copyNo := int(m) + 1
		at := s.clock.Now()
		if b.ClosedAt != nil {
			at = *b.ClosedAt
		}
		doc := printing.Receipt(s.receiptInput(b, o, pp, cashier, at, false, decimal.Zero), printing.LabelsID)
		orig, err := q.OriginalReceiptJob(ctx, posdb.OriginalReceiptJobParams{PropertyID: propertyID, BillID: billID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id, created, err := s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: *o.ReceiptPrinterID, Kind: printing.KindReceipt, SourceType: "BILL", SourceID: billID,
			DedupeKey: fmt.Sprintf("RECEIPT:%d:COPY:%d", billID, copyNo), CopyNo: copyNo, Doc: doc, ReprintOf: orig, Mark: fmt.Sprintf("COPY %d", copyNo), Reason: reason, By: p.ActorID()})
		if err != nil {
			return err
		}
		out = JobCreated{ID: uuidString(id), CopyNo: copyNo, Created: created}
		pid := propertyID
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &pid, UserID: p.ActorID(), Action: "pos.print_reprinted", EntityType: "pos_bill", EntityID: billID,
			EntityLabel: b.Number, New: map[string]any{"kind": printing.KindReceipt, "copy_no": copyNo, "reason": reason}})
	})
	return out, err
}

// ---------------------------------------------------------------- the drawer

// DrawerOpened answers a no-sale open.
type DrawerOpened struct {
	ID    int64  `json:"id"`
	JobID string `json:"job_id,omitempty"`
}

// OpenDrawer opens the cash drawer of an outlet without a sale (pos.cashier): a reason is required, the person and the shift are recorded, an
// audit entry is written and the count shows on the report of the outlet. No approval of a supervisor is asked.
func (s *Service) OpenDrawer(ctx context.Context, propertyID, outletID int64, reason string) (DrawerOpened, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return DrawerOpened{}, err
	}
	reason, fields := requireReason(reason)
	if len(fields) > 0 {
		return DrawerOpened{}, invalid("drawer", fields...)
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, outletID)
	if err != nil {
		return DrawerOpened{}, err
	}
	if o.Drawer == nil {
		return DrawerOpened{}, apperr.Conflict("OUTLET_NO_CASH", "this outlet has no drawer")
	}
	var out DrawerOpened
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		shift, err := s.ledger.PrepareOutletCash(ctx, propertyID, *o.Drawer) // the open shift of the cashier for this drawer (409 NO_OPEN_SHIFT when the property asks for one)
		if err != nil {
			return err
		}
		// the pulse is a job when the receipt printer of the outlet opens the drawer; without one the open is still recorded (the drawer is opened by hand)
		var jobID pgtype.UUID
		if o.ReceiptPrinterID != nil {
			pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: propertyID, ID: *o.ReceiptPrinterID})
			if err != nil {
				return orNotFound(err, notFound("PRINTER_NOT_FOUND", "printer"))
			}
			if pr.IsActive && pr.OpensDrawer {
				if jobID, _, err = s.enqueue(ctx, p.TenantID, propertyID, jobSpec{PrinterID: pr.ID, Kind: printing.KindDrawer, SourceType: "OUTLET", SourceID: outletID,
					DedupeKey: "DRAWER:OPEN:" + uuidString(newUUID()), CopyNo: 1, Doc: printing.Drawer(), By: p.ActorID()}); err != nil {
					return err
				}
			}
		}
		id, err := q.InsertDrawerOpen(ctx, posdb.InsertDrawerOpenParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, Drawer: *o.Drawer, ShiftID: shift,
			BusinessDate: bd, Reason: reason, OpenedBy: p.UserID, OpenedAt: s.clock.Now(), JobID: jobID})
		if err != nil {
			return err
		}
		out = DrawerOpened{ID: id}
		if jobID.Valid {
			out.JobID = uuidString(jobID)
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.drawer_opened", "pos_outlet", outletID, o.Code, nil, map[string]any{"reason": reason, "drawer": *o.Drawer, "shift_id": shift}))
	})
	return out, err
}

// DrawerOpen is one open without a sale, for the report.
type DrawerOpen struct {
	ID       int64      `json:"id"`
	OpenedAt time.Time  `json:"opened_at"`
	Date     civil.Date `json:"business_date"`
	Reason   string     `json:"reason"`
	Drawer   string     `json:"drawer"`
	ShiftID  *int64     `json:"shift_id"`
	OpenedBy string     `json:"opened_by"`
}

// drawerOpens reads the opens of an outlet between two business dates.
func (s *Service) drawerOpens(ctx context.Context, tenantID, propertyID, outletID int64, from, to civil.Date) ([]DrawerOpen, error) {
	rows, err := s.q(ctx).DrawerOpensOfOutlet(ctx, posdb.DrawerOpensOfOutletParams{TenantID: tenantID, PropertyID: propertyID, OutletID: outletID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make([]DrawerOpen, len(rows))
	for i, r := range rows {
		out[i] = DrawerOpen{ID: r.ID, OpenedAt: r.OpenedAt, Date: r.BusinessDate, Reason: r.Reason, Drawer: r.Drawer, ShiftID: r.ShiftID, OpenedBy: r.OpenedByName}
	}
	return out, nil
}
