package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/notifications"
)

type captureMail struct {
	mu   sync.Mutex
	sent []notifications.Message
}

func (c *captureMail) Send(_ context.Context, m notifications.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

func (e *apiEnv) withMail(m notifications.Sender) {
	e.t.Helper()
	e.mail = m
	e.app = e.build(0)
	e.handler = e.app.Handler
}

func TestDocumentsAndConfirmationEmailAPI(t *testing.T) {
	e := newAPI(t)
	mail := &captureMail{}
	e.withMail(mail)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	if r := abc.do(http.MethodPatch, base, map[string]any{"phone": "+62 361 1", "email": "info@bali.test", "tax_id": "01.234.567.8", "document_footer": "Thank you"}); r.status != 200 || r.body["tax_id"] != "01.234.567.8" {
		t.Fatalf("property contact: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base, map[string]any{"email": "not an address"}); r.status != 422 {
		t.Fatalf("bad property e-mail: %d %v", r.status, r.body)
	}

	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "102", "initial_housekeeping_status": "CLEAN"})
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "first_name": "Siti", "last_name": "Nurhaliza", "email": "siti@example.test"}))

	// confirming queues the e-mail in the booking's transaction; the worker sends it
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	resID := idOf(res)
	emails := abc.do(http.MethodGet, base+"/reservations/"+resID+"/emails", nil)
	data := emails.body["data"].([]any)
	if emails.status != 200 || emails.body["enabled"] != true || len(data) != 1 || data[0].(map[string]any)["status"] != "QUEUED" || data[0].(map[string]any)["to"] != "siti@example.test" {
		t.Fatalf("emails: %d %v", emails.status, emails.body)
	}
	if len(mail.sent) != 0 {
		t.Fatal("nothing is sent inside the request")
	}
	if sent, _, err := e.app.notifier.ProcessDue(context.Background()); err != nil || sent != 1 || len(mail.sent) != 1 {
		t.Fatalf("worker: %d %v", sent, err)
	}
	m := mail.sent[0]
	if m.To != "siti@example.test" || !strings.Contains(m.Subject, "Hotel Bali") || len(m.Attachments) != 1 || !strings.HasPrefix(string(m.Attachments[0].Data), "%PDF-") {
		t.Fatalf("message: %+v", m)
	}
	if again := abc.do(http.MethodGet, base+"/reservations/"+resID+"/emails", nil); again.body["data"].([]any)[0].(map[string]any)["status"] != "SENT" {
		t.Fatalf("after sending: %v", again.body)
	}
	if r := abc.do(http.MethodPost, base+"/reservations/"+resID+"/emails", nil); r.status != http.StatusAccepted || r.body["status"] != "QUEUED" {
		t.Fatalf("resend: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/reservations/"+resID+"/emails", nil); r.status != 409 || r.body["code"] != "EMAIL_ALREADY_QUEUED" {
		t.Fatalf("second resend: %d %v", r.status, r.body)
	}

	// a stay with a charge and a payment, then every document
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"guest_id": mustInt(guest), "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-03", "adult_count": 2, "child_count": 0},
		map[string]string{"Idempotency-Key": "w-1"})
	if walk.status != http.StatusCreated {
		t.Fatalf("walk-in: %d %v", walk.status, walk.body)
	}
	stayID := idOf(response{body: walk.body["stay"].(map[string]any)})
	folioID := idOf(response{body: walk.body["folio"].(map[string]any)})
	pay := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "300000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "p1"})
	payID := idOf(response{body: pay.body["payment"].(map[string]any)})
	for name, path := range map[string]string{
		"invoice":      "/folios/" + folioID + "/invoice.pdf",
		"registration": "/stays/" + stayID + "/registration-card.pdf",
		"receipt":      "/payments/" + payID + "/receipt.pdf",
		"confirmation": "/reservations/" + resID + "/confirmation.pdf",
	} {
		r := e.raw(abc, http.MethodGet, base+path)
		if r.status != 200 || r.contentType != "application/pdf" || !strings.HasPrefix(r.body, "%PDF-") || len(r.body) < 1000 {
			t.Fatalf("%s: %d %q (%d bytes)", name, r.status, r.contentType, len(r.body))
		}
		h := e.header(abc, base+path)
		if !strings.HasPrefix(h.Get("Content-Disposition"), `inline; filename="`) || h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s headers: %v", name, h)
		}
	}
	for _, path := range []string{"/folios/999999/invoice.pdf", "/stays/999999/registration-card.pdf", "/payments/999999/receipt.pdf", "/reservations/999999/confirmation.pdf", "/folios/abc/invoice.pdf"} {
		if r := e.raw(abc, http.MethodGet, base+path); r.status != 404 {
			t.Fatalf("%s: %d", path, r.status)
		}
	}
	if r := e.raw(xyz, http.MethodGet, base+"/folios/"+folioID+"/invoice.pdf"); r.status != 404 {
		t.Fatalf("another tenant must not read the invoice: %d", r.status)
	}
}

func TestEmailOffMeansNothingIsQueuedAndTheScreenSaysSo(t *testing.T) {
	e := newAPI(t) // no mail server
	abc := e.login("ABC")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "101"})
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest", "email": "g@example.test"}))
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	emails := abc.do(http.MethodGet, base+"/reservations/"+idOf(res)+"/emails", nil)
	if emails.status != 200 || emails.body["enabled"] != false || len(emails.body["data"].([]any)) != 0 {
		t.Fatalf("emails: %d %v", emails.status, emails.body)
	}
	if r := abc.do(http.MethodPost, base+"/reservations/"+idOf(res)+"/emails", nil); r.status != 409 || r.body["code"] != "EMAIL_NOT_CONFIGURED" {
		t.Fatalf("resend: %d %v", r.status, r.body)
	}
}
