package app

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stream of events of the POS through the real router and a real socket (docs/architecture/34-pos.md 10.1, 19.1).

// sseFrame is one event as it arrived.
type sseFrame struct {
	id    int64
	event string
	data  string
}

// readStream opens the stream and collects frames until `want` events were read or the time ran out. It returns what it read; the status of the answer when it is not 200.
func readStream(t *testing.T, base, token, path, lastID string, want int, wait time.Duration) ([]sseFrame, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var out []sseFrame
	cur := sseFrame{}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.event != "" {
				out = append(out, cur)
				if len(out) >= want {
					return out, http.StatusOK
				}
			}
			cur = sseFrame{}
		case strings.HasPrefix(line, "id: "):
			cur.id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out, http.StatusOK
}

func TestThePOSEventStreamResumesAndIsScopedAPI(t *testing.T) {
	e := newAPI(t)
	srv := httptest.NewServer(e.handler)
	defer srv.Close()
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	// an outlet with a table, set up through the API
	var dept any
	for _, d := range abc.do(http.MethodGet, base+"/departments", nil).body["data"].([]any) {
		if d.(map[string]any)["code"] == "FB" {
			dept = d.(map[string]any)["id"]
		}
	}
	tax := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "PB1", "name": "PB1", "rate": "10", "tax_on_service": true})
	profile := abc.do(http.MethodPost, base+"/pos/rule-profiles", map[string]any{"code": "FB", "name": "F&B", "price_mode": "EXCLUSIVE", "rules": []any{map[string]any{"tax_id": tax.body["id"]}}})
	list := abc.do(http.MethodPost, base+"/pos/price-lists", map[string]any{"code": "MAIN", "name": "Main"})
	outlet := abc.do(http.MethodPost, base+"/pos/outlets", map[string]any{
		"code": "RESTO", "name": "Restoran Teratai", "outlet_type": "RESTAURANT", "order_mode": "KDS", "department_id": dept, "rule_profile_id": profile.body["id"], "price_list_id": list.body["id"],
		"uses_tables": true, "comp_treatment": "PAYMENT_COMP", "max_discount_pct_no_approval": "0", "rounding_unit": 0,
	})
	if outlet.status != 201 {
		t.Fatalf("outlet: %d %v", outlet.status, outlet.body)
	}
	oid := idOf(outlet)
	area := abc.do(http.MethodPost, base+"/pos/outlets/"+oid+"/areas", map[string]any{"name": "Indoors"})
	saved := abc.do(http.MethodPut, base+"/pos/areas/"+idOf(area)+"/layout", map[string]any{
		"name": "Indoors", "grid_w": 40, "grid_h": 24, "layout_version": 1,
		"tables": []any{map[string]any{"code": "1", "capacity": 2, "shape": "SQUARE", "x": 2, "y": 2, "w": 4, "h": 4, "is_active": true}}, "marks": []any{},
	})
	if saved.status != 200 {
		t.Fatalf("layout: %d %v", saved.status, saved.body)
	}
	table := saved.body["tables"].([]any)[0].(map[string]any)["id"]
	open := func(n int) response {
		return abc.do(http.MethodPost, base+"/pos/outlets/"+oid+"/bills", map[string]any{
			"client_uuid": fmt.Sprintf("00000000-0000-4000-8000-%012d", n), "order_type": "TAKEAWAY", "customer_name": "Guest " + strconv.Itoa(n),
		})
	}
	_ = table

	stream := base + "/pos/events?channels=outlet:" + oid

	// a stream that begins at the present, an event that comes in while it is open
	type result struct {
		frames []sseFrame
		status int
	}
	done := make(chan result, 1)
	go func() {
		f, st := readStream(t, srv.URL, abc.token, stream, "", 1, 10*time.Second)
		done <- result{f, st}
	}()
	time.Sleep(300 * time.Millisecond) // the stream is open
	if r := open(1); r.status != 201 {
		t.Fatalf("open a bill: %d %v", r.status, r.body)
	}
	got := <-done
	if got.status != 200 || len(got.frames) != 1 || got.frames[0].event != "bill.updated" || !strings.Contains(got.frames[0].data, `"channel":"outlet:`+oid+`"`) {
		t.Fatalf("first events: %+v", got)
	}
	// ids only: no amount or name in the payload
	if strings.Contains(got.frames[0].data, "Guest") || strings.Contains(got.frames[0].data, "amount") {
		t.Fatalf("the payload carries ids only: %s", got.frames[0].data)
	}
	last := got.frames[0].id

	// the connection is gone; two things happen meanwhile
	if r := open(2); r.status != 201 {
		t.Fatalf("second bill: %d", r.status)
	}
	if r := open(3); r.status != 201 {
		t.Fatalf("third bill: %d", r.status)
	}
	// it comes back with the last id: what it missed, in order, and not what it had
	frames, st := readStream(t, srv.URL, abc.token, stream, strconv.FormatInt(last, 10), 2, 10*time.Second)
	if st != 200 || len(frames) != 2 {
		t.Fatalf("resumed: %d %+v", st, frames)
	}
	for i, f := range frames {
		if f.id <= last || (i > 0 && f.id <= frames[i-1].id) {
			t.Fatalf("ids must grow after %d: %+v", last, frames)
		}
	}

	// the scope: another tenant cannot listen to this property, and cannot name an outlet of it
	if _, st := readStream(t, srv.URL, xyz.token, stream, "", 1, 3*time.Second); st != http.StatusNotFound {
		t.Fatalf("another tenant's property: %d", st)
	}
	xp := idOf(xyz.do(http.MethodPost, "/api/v1/properties", validProperty()))
	if _, st := readStream(t, srv.URL, xyz.token, "/api/v1/properties/"+xp+"/pos/events?channels=outlet:"+oid, "", 1, 3*time.Second); st != http.StatusNotFound {
		t.Fatalf("another tenant's outlet: %d", st)
	}
	// no token, bad channels, a resume point that is not held
	if _, st := readStream(t, srv.URL, "", stream, "", 1, 3*time.Second); st != http.StatusUnauthorized {
		t.Fatalf("no token: %d", st)
	}
	if _, st := readStream(t, srv.URL, abc.token, base+"/pos/events?channels=nonsense", "", 1, 3*time.Second); st != http.StatusUnprocessableEntity {
		t.Fatalf("bad channel: %d", st)
	}
	if _, st := readStream(t, srv.URL, abc.token, base+"/pos/events", "", 1, 3*time.Second); st != http.StatusUnprocessableEntity {
		t.Fatalf("no channel: %d", st)
	}
	if _, st := readStream(t, srv.URL, abc.token, stream, "99999999", 1, 3*time.Second); st != http.StatusGone {
		t.Fatalf("an id the outbox does not hold: %d", st)
	}
	// the stream is a route of the POS module
	if r := abc.do(http.MethodGet, base+"/pos/stations/999999/board", nil); r.status != 404 || r.body["code"] != "STATION_NOT_FOUND" {
		t.Fatalf("a station that does not exist: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/pos/tickets/999999/status", map[string]any{"action": "START"}); r.status != 404 || r.body["code"] != "TICKET_NOT_FOUND" {
		t.Fatalf("a ticket that does not exist: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/pos/print-jobs", nil); r.status != 200 || len(r.body["jobs"].([]any)) != 0 {
		t.Fatalf("print jobs: %d %v", r.status, r.body)
	}
}
