package folios_test

import (
	"context"
	"testing"

	"kamarapms/internal/folios"
)

// labelOf is the label of the newest audit entry of an action (empty when it has none).
func (f *fx) labelOf(t *testing.T, action string) string {
	t.Helper()
	var label *string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT entity_label FROM audit_logs WHERE action = $1 ORDER BY id DESC LIMIT 1`, action).Scan(&label))
	if label == nil {
		return ""
	}
	return *label
}

func TestAuditEntriesNameWhatTheyAreAbout(t *testing.T) {
	f := setup(t)
	// a charge is about the folio, a payment about its number, a line of the folio is named by the folio it is on
	item := f.charge(t, "c1")
	if got := f.labelOf(t, "folio.charge_posted"); got != "F1" {
		t.Fatalf("a charge is about the folio: %q", got)
	}
	pay := f.payment(t, "p1", "50000")
	if got := f.labelOf(t, "payment.posted"); got != pay.Payment.PaymentNumber || got == "" {
		t.Fatalf("a payment is about its number: %q, want %q", got, pay.Payment.PaymentNumber)
	}
	_, err := f.Folios.Reverse(f.admin, f.propID, item.Item.ID, folios.CorrectionInput{Reason: "a mistake", Approval: f.approval()})
	must(t, err)
	if got := f.labelOf(t, "folio.item_reversed"); got != "F1" {
		t.Fatalf("a reversal is about the folio of the line: %q", got)
	}
	_, err = f.Folios.Void(f.admin, f.propID, pay.Payment.ID, folios.CorrectionInput{Reason: "wrong amount", Approval: f.approval()})
	must(t, err)
	if got := f.labelOf(t, "payment.voided"); got != pay.Payment.PaymentNumber {
		t.Fatalf("a void is about the number of the payment, read before anything else: %q", got)
	}
	// the setup of the property is labelled too: the rooms and the taxes it made
	if got := f.labelOf(t, "room.created"); got != "101" {
		t.Fatalf("room: %q", got)
	}
	if got := f.labelOf(t, "tax.created"); got != "VAT" {
		t.Fatalf("tax: %q", got)
	}
	if got := f.labelOf(t, "service_charge.created"); got != "SVC" {
		t.Fatalf("service charge: %q", got)
	}
	// and the property: its code, not its id
	if got := f.labelOf(t, "property.created"); got != "BALI" {
		t.Fatalf("property: %q", got)
	}
	// no entry of this property holds the name of a guest or a free text as its label: every label is a number or a code
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE entity_label ~ '\s' AND entity_type <> 'bank_statement'`).Scan(&n))
	if n != 0 {
		t.Fatalf("%d labels hold a blank (a name or a sentence)", n)
	}
}

func TestPaymentsAndFoliosNameTheirFolioAndReservation(t *testing.T) {
	f := setup(t)
	pay := f.payment(t, "p1", "50000")
	if pay.Payment.FolioNumber != "F1" {
		t.Fatalf("the payment names its folio: %+v", pay.Payment)
	}
	list, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{}, 0, 10)
	must(t, err)
	if len(list.Data) != 1 || list.Data[0].FolioNumber != "F1" {
		t.Fatalf("the list names the folio: %+v", list)
	}
	folioList, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{}, 0, 10)
	must(t, err)
	if len(folioList) == 0 || folioList[0].ConfirmationNumber == "" {
		t.Fatalf("a folio of the list names its reservation: %+v", folioList)
	}
	var want string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT confirmation_number FROM reservations WHERE id = $1`, folioList[0].ReservationID).Scan(&want))
	if folioList[0].ConfirmationNumber != want {
		t.Fatalf("the number of the reservation: %q, want %q", folioList[0].ConfirmationNumber, want)
	}
}
