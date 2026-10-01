package documents_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/documents"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/tenancy"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	adminEmail       string
	stay             frontdesk.CheckInResult
	reservation      reservations.Reservation
	minibar          int64
}

// setup: a property with letterhead details, a guest with a full profile, a stay in room 101 with a minibar charge
// (10% service, 11% VAT on it), and a cash payment.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	_, err := e.Tenancy.UpdateProperty(admin, p.ID, tenancy.PropertyPatch{Name: ptr("Hotel Bali"), Address: ptr("Jl. Sunset 1"), City: ptr("Denpasar"), Phone: ptr("+62 361 1"), Email: ptr("info@bali.test"), TaxID: ptr("01.234.567.8"), DocumentFooter: ptr("Thank you for staying with us")})
	must(t, err)
	dlx := e.RoomType(t, admin, p.ID, "DLX")
	room := e.Room(t, admin, p.ID, dlx.ID, "101", housekeeping.Clean)
	ctx := context.Background()
	var chargeID, plan, guest int64
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, p.ID).Scan(&f.minibar))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&plan))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO guests (tenant_id, code, first_name, last_name, email, nationality, id_type, id_number, address, city, origin_property_id)
		VALUES ($1, 'G1', 'Siti', 'Nurhaliza', 'siti@example.test', 'ID', 'KTP', '3171234567', 'Jl. Mawar 2', 'Jakarta', $2) RETURNING id`, tn.ID, p.ID).Scan(&guest))
	_, err = e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: plan, RoomTypeIDs: []int64{dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	svc, err := e.Billing.CreateServiceCharge(admin, p.ID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	vat, err := e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, IsActive: true})
	must(t, err)
	_, err = e.Billing.ReplaceRules(admin, p.ID, f.minibar, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}}})
	must(t, err)
	f.reservation, err = e.Res.Create(admin, p.ID, "", reservations.CreateInput{GuestID: &guest, Source: "PHONE", Confirm: true, SpecialRequest: "Quiet room", Rooms: []reservations.LineInput{
		{RoomTypeID: dlx.ID, RatePlanID: plan, Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Adults: 2}}})
	must(t, err)
	f.stay, err = e.Front.CheckIn(admin, p.ID, f.reservation.ID, f.reservation.Rooms[0].ID, "", frontdesk.CheckInInput{Version: f.reservation.Version, RoomID: &room.ID, GuestID: guest, AdultCount: 2})
	must(t, err)
	_, err = e.Folios.PostCharge(admin, p.ID, f.stay.Folio.ID, "c1", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	_, err = e.Folios.PostPayment(admin, p.ID, f.stay.Folio.ID, "p1", folios.PaymentInput{Amount: "50000", PaymentMethod: "CASH", ReferenceNumber: "REF-9"})
	must(t, err)
	return f
}

func pdfText(t *testing.T, doc documents.Document) string {
	t.Helper()
	if !strings.HasPrefix(string(doc.PDF), "%PDF-") || !strings.HasSuffix(doc.Filename, ".pdf") {
		t.Fatalf("not a PDF: %s %q", doc.Filename, doc.PDF[:min(10, len(doc.PDF))])
	}
	return string(doc.PDF)
}

func TestInvoiceFromARealFolio(t *testing.T) {
	f := setup(t)
	doc, err := f.Docs.Invoice(f.admin, f.propID, f.stay.Folio.ID)
	must(t, err)
	s := pdfText(t, doc)
	if !strings.HasPrefix(doc.Filename, "bill-FOL") {
		t.Fatalf("an open folio is a bill: %s", doc.Filename)
	}
	for _, want := range []string{"Hotel Bali", "01.234.567.8", "Siti Nurhaliza", "Jl. Mawar 2, Jakarta", f.reservation.ConfirmationNumber, f.stay.Stay.StayNumber, "GUEST BILL", "101",
		"MINIBAR", "Service charge Service 10%", "VAT 11%", "Total charges", "122,100", "Payments received", "50,000", "72,100", "Thank you for staying with us"} {
		if !strings.Contains(s, want) {
			t.Errorf("bill lacks %q", want)
		}
	}
	// checked out and settled, the folio is closed: the final invoice, with the room night and the payments
	_, err = f.Folios.PostPayment(f.admin, f.propID, f.stay.Folio.ID, "p2", folios.PaymentInput{Amount: "1072100", PaymentMethod: "CARD"})
	must(t, err)
	_, err = f.Front.CheckOut(f.admin, f.propID, f.stay.Stay.ID, frontdesk.CheckOutInput{Version: f.stay.Stay.Version, ConfirmEarlyDeparture: true})
	must(t, err)
	final, err := f.Docs.Invoice(f.admin, f.propID, f.stay.Folio.ID)
	must(t, err)
	fs := pdfText(t, final)
	if !strings.HasPrefix(final.Filename, "invoice-FOL") || strings.Contains(fs, "GUEST BILL") || strings.Contains(fs, "not final") {
		t.Fatalf("a closed folio is an invoice: %s", final.Filename)
	}
	for _, want := range []string{"INVOICE", "Room 101 - 30 Sep 2026", "Total charges", "1,122,100", "Payments received", "1,122,100", "Balance due"} {
		if !strings.Contains(fs, want) {
			t.Errorf("invoice lacks %q", want)
		}
	}
}

func TestReceiptsRegistrationCardAndConfirmation(t *testing.T) {
	f := setup(t)
	var payID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM payments WHERE property_id = $1`, f.propID).Scan(&payID))
	rc, err := f.Docs.Receipt(f.admin, f.propID, payID)
	must(t, err)
	s := pdfText(t, rc)
	for _, want := range []string{"PAYMENT RECEIPT", "PAY", "Siti Nurhaliza", "Cash", "REF-9", "IDR 50,000", "FOL"} {
		if !strings.Contains(s, want) {
			t.Errorf("receipt lacks %q", want)
		}
	}
	card, err := f.Docs.RegistrationCard(f.admin, f.propID, f.stay.Stay.ID)
	must(t, err)
	cs := pdfText(t, card)
	for _, want := range []string{"REGISTRATION CARD", f.stay.Stay.StayNumber, "KTP 3171234567", "Jl. Mawar 2, Jakarta", "siti@example.test", "Quiet room", "BAR", "IDR 1,000,000 per night", "from 14:00"} {
		if !strings.Contains(cs, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	conf, err := f.Docs.Confirmation(f.admin, f.propID, f.reservation.ID)
	must(t, err)
	if c := pdfText(t, conf); !strings.Contains(c, "RESERVATION CONFIRMATION") || !strings.Contains(c, f.reservation.ConfirmationNumber) || !strings.Contains(c, "DLX") || !strings.Contains(c, "Quiet room") {
		t.Error("confirmation content")
	}
	// a voided payment prints as void
	_, err = f.Folios.Void(f.admin, f.propID, payID, folios.CorrectionInput{Reason: "typo", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}})
	must(t, err)
	v, err := f.Docs.Receipt(f.admin, f.propID, payID)
	must(t, err)
	if vs := pdfText(t, v); !strings.Contains(vs, "VOID") || !strings.Contains(vs, "typo") {
		t.Error("a voided receipt is stamped")
	}
}

func TestDocumentsNeedThePermissionsOfWhatTheyShow(t *testing.T) {
	f := setup(t)
	cashier := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Docs.Invoice(cashier, f.propID, f.stay.Folio.ID)
	wantCode(t, err, "PERMISSION_DENIED") // the invoice names the reservation: reservation.read too
	both := f.User(t, f.tenantID, f.propID, auth.PermFolioRead, auth.PermReservationRead)
	if _, err := f.Docs.Invoice(both, f.propID, f.stay.Folio.ID); err != nil {
		t.Fatalf("folio.read and reservation.read: %v", err)
	}
	_, err = f.Docs.RegistrationCard(cashier, f.propID, f.stay.Stay.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	desk := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Docs.Receipt(desk, f.propID, 1)
	wantCode(t, err, "PERMISSION_DENIED")
	// without guest.read the profile is left out: the document still prints with the name from the reservation
	card, err := f.Docs.RegistrationCard(desk, f.propID, f.stay.Stay.ID)
	must(t, err)
	if s := pdfText(t, card); strings.Contains(s, "3171234567") {
		t.Error("the ID number needs guest.read")
	}
	// unknown ids and other tenants
	_, err = f.Docs.Invoice(f.admin, f.propID, 999999)
	wantCode(t, err, "FOLIO_NOT_FOUND")
	_, err = f.Docs.Receipt(f.admin, f.propID, 999999)
	wantCode(t, err, "PAYMENT_NOT_FOUND")
	_, err = f.Docs.RegistrationCard(f.admin, f.propID, 999999)
	wantCode(t, err, "STAY_NOT_FOUND")
	other := f.Tenant(t, "XYZ")
	_, err = f.Docs.Invoice(roomstest.Admin(other.ID), f.propID, f.stay.Folio.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
