package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// `pms-seed inventory-days` (docs/architecture/35-inventory.md, PR 5): ten business days of demo activity, so that the daily cost, its chart and the reports of the inventory have something to show. It talks to a running
// API of the DEVELOPMENT stack, as a person at the till and in the office would: POS bills for the restaurant and the bar (sent and paid by card, so no shift is needed), waste, staff meals, entertainment, a use
// by engineering and by the office, and then the night audit of the day, which stores the snapshots. The business date of the property moves forward one day for each day made.
//
// The password comes from the environment (PMS_SEED_PASSWORD) and is never printed. The API must run on this machine. Run it again and it does nothing once the last N closed days have cost.

// demoDay is what happens on the day number i of the run, a pure function of i so that the plan can be tested and shown by -dry-run.
type demoDay struct {
	// bills of the restaurant: portions of Nasi Goreng, with options
	nasgor []daySale
	// bills of the bar
	bar []daySale
	// issues
	wasteEggs      int
	staffRiceKg    int
	entertainBotol int
	engineering    int
	office         int
}

type daySale struct {
	product string
	qty     int
	options []string
}

func planDay(i int) demoDay {
	d := demoDay{staffRiceKg: 2, office: 0}
	// a quiet start of the week and a busy end of it
	busy := 1 + i%4
	d.nasgor = append(d.nasgor, daySale{"NASGOR", 3 + busy*2, nil})
	if i%2 == 0 {
		d.nasgor = append(d.nasgor, daySale{"NASGOR", 2, []string{"Jumbo", "Telur ceplok"}})
	}
	if i%3 == 0 {
		d.nasgor = append(d.nasgor, daySale{"NASGOR", 3, []string{"Telur ceplok"}})
	}
	d.bar = append(d.bar, daySale{"ESTEH", 4 + busy*2, nil}, daySale{"KOPIBALI", 2 + busy, nil})
	if i%3 == 1 {
		d.wasteEggs = 4 + i
	}
	if i%4 == 2 {
		d.entertainBotol = 1
	}
	if i%5 == 3 {
		d.engineering = 2
	}
	if i%5 == 4 {
		d.office = 1
	}
	return d
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *apiClient) do(method, path string, body any, headers map[string]string) (map[string]any, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode >= 300 {
		detail := firstOf(out, "code", "detail")
		if f, ok := out["errors"]; ok {
			b, _ := json.Marshal(f)
			detail += " " + string(b)
		}
		return out, fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, detail)
	}
	return out, nil
}

func firstOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func uuid4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func asList(m map[string]any, key string) []map[string]any {
	var out []map[string]any
	if l, ok := m[key].([]any); ok {
		for _, x := range l {
			if mm, ok := x.(map[string]any); ok {
				out = append(out, mm)
			}
		}
	}
	return out
}

func idOf(m map[string]any) int64 {
	f, _ := m["id"].(float64)
	return int64(f)
}

func runInventoryDays(args []string) error {
	fs := flag.NewFlagSet("inventory-days", flag.ContinueOnError)
	tenantCode := fs.String("tenant", "", "tenant code")
	propertyCode := fs.String("property", "", "property code")
	days := fs.Int("days", 10, "days of activity to have (1 to 31)")
	apiURL := fs.String("api", "http://127.0.0.1:18080", "the API of the development stack (this machine only)")
	email := fs.String("email", "admin@demo.test", "the administrator to sign in as")
	dryRun := fs.Bool("dry-run", false, "show the plan, change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tenantCode == "" || *propertyCode == "" || *days < 1 || *days > 31 {
		return fmt.Errorf("-tenant and -property are required and -days is 1 to 31")
	}
	u, err := url.Parse(*apiURL)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return fmt.Errorf("refusing to seed demo days into an API that is not on this machine")
	}
	password := os.Getenv("PMS_SEED_PASSWORD")
	if password == "" {
		return fmt.Errorf("set PMS_SEED_PASSWORD (the password of %s) in the environment", *email)
	}
	c := &apiClient{base: strings.TrimRight(*apiURL, "/"), http: &http.Client{Timeout: 60 * time.Second}}
	login, err := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": *tenantCode, "email": *email, "password": password}, nil)
	if err != nil {
		return fmt.Errorf("sign in: %w", err)
	}
	c.token, _ = login["access_token"].(string)

	props, err := c.do(http.MethodGet, "/api/v1/properties", nil, nil)
	if err != nil {
		return err
	}
	var pid int64
	for _, p := range asList(props, "data") {
		if strings.EqualFold(fmt.Sprint(p["code"]), *propertyCode) {
			pid = idOf(p)
		}
	}
	if pid == 0 {
		return fmt.Errorf("no property %q for this user", *propertyCode)
	}
	base := fmt.Sprintf("/api/v1/properties/%d", pid)
	bd, err := c.do(http.MethodGet, base+"/business-date", nil, nil)
	if err != nil {
		return err
	}
	open, err := civil.ParseDate(fmt.Sprint(bd["business_date"]))
	if err != nil {
		return fmt.Errorf("business date: %w", err)
	}

	// the days that already have a cost: the last N closed days
	have, err := daysWithCost(c, base, open.AddDays(-*days), open.AddDays(-1))
	if err != nil {
		return err
	}
	todo := *days - have
	if todo <= 0 {
		fmt.Printf("the last %d closed days already have a cost: nothing to do\n", *days)
		return nil
	}
	fmt.Printf("business date %s: %d of the last %d days have a cost; %d day(s) to make\n", open, have, *days, todo)
	for i := 0; i < todo; i++ {
		d := planDay(i + have)
		fmt.Printf("  day %d (%s): %d lines at the restaurant, %d at the bar, waste %d egg(s), staff meal %d kg of rice, entertainment %d, engineering %d, office %d\n",
			i+1, open.AddDays(i), len(d.nasgor), len(d.bar), d.wasteEggs, d.staffRiceKg, d.entertainBotol, d.engineering, d.office)
	}
	if *dryRun {
		fmt.Println("dry run: nothing was changed")
		return nil
	}

	w, err := loadDemoWorld(c, base)
	if err != nil {
		return err
	}
	if err := w.ensureEngineering(c, base); err != nil {
		return err
	}
	if err := w.settleOpenBills(c, base); err != nil {
		return err
	}
	for i := 0; i < todo; i++ {
		date := open.AddDays(i)
		if err := w.makeDay(c, base, planDay(i+have), date); err != nil {
			return fmt.Errorf("day %s: %w", date, err)
		}
		if err := extendDueStays(c, base, open.AddDays(todo+1)); err != nil {
			return err
		}
		if _, err := c.do(http.MethodPost, base+"/night-audit/run", map[string]string{"business_date": date.String()}, nil); err != nil {
			return fmt.Errorf("the night audit of %s: %w%s", date, err, blockersOf(c, base))
		}
		fmt.Printf("day %s made and closed\n", date)
	}
	return w.ensureTargets(c, base, open, open.AddDays(todo-1))
}

// daysWithCost counts the closed days of the range whose daily cost is not nil.
func daysWithCost(c *apiClient, base string, from, to civil.Date) (int, error) {
	r, err := c.do(http.MethodGet, fmt.Sprintf("%s/inventory/reports/daily-cost?from=%s&to=%s", base, from, to), nil, nil)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range asList(r, "days") {
		if live, _ := d["live"].(bool); live {
			continue
		}
		if t, ok := d["total"].(string); ok && t != "0" && t != "" {
			n++
		}
	}
	return n, nil
}

type demoWorld struct {
	outlets    map[string]int64
	products   map[string]map[string]any
	warehouses map[string]int64
	items      map[string]int64
	depts      map[string]int64
	engDept    int64
	officeDept int64
}

func loadDemoWorld(c *apiClient, base string) (*demoWorld, error) {
	w := &demoWorld{outlets: map[string]int64{}, products: map[string]map[string]any{}, warehouses: map[string]int64{}, items: map[string]int64{}, depts: map[string]int64{}}
	o, err := c.do(http.MethodGet, base+"/pos/outlets", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, x := range asList(o, "data") {
		w.outlets[fmt.Sprint(x["code"])] = idOf(x)
	}
	p, err := c.do(http.MethodGet, base+"/pos/products", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, x := range asList(p, "data") {
		full, err := c.do(http.MethodGet, fmt.Sprintf("%s/pos/products/%d", base, idOf(x)), nil, nil)
		if err != nil {
			return nil, err
		}
		w.products[fmt.Sprint(x["code"])] = full
	}
	wh, err := c.do(http.MethodGet, base+"/inventory/warehouses", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, x := range asList(wh, "data") {
		w.warehouses[fmt.Sprint(x["code"])] = idOf(x)
	}
	it, err := c.do(http.MethodGet, base+"/inventory/items?limit=200", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, x := range asList(it, "items") {
		w.items[fmt.Sprint(x["code"])] = idOf(x)
	}
	dp, err := c.do(http.MethodGet, base+"/departments", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, x := range asList(dp, "data") {
		w.depts[strings.ToUpper(fmt.Sprint(x["code"]))] = idOf(x)
	}
	for _, code := range []string{"ENG", "POM", "MAINT", "ENGINEERING"} {
		if id, ok := w.depts[code]; ok && w.engDept == 0 {
			w.engDept = id
		}
	}
	for _, code := range []string{"AG", "ADM", "ADMIN"} {
		if id, ok := w.depts[code]; ok && w.officeDept == 0 {
			w.officeDept = id
		}
	}
	for _, need := range []string{"RESTO", "BAR"} {
		if w.outlets[need] == 0 {
			return nil, fmt.Errorf("outlet %s is missing: run pms-seed pos and pms-seed inventory first", need)
		}
	}
	return w, nil
}

// ensureEngineering names the engineering department in the settings (the cost of its issues is the engineering cost), once.
func (w *demoWorld) ensureEngineering(c *apiClient, base string) error {
	if w.engDept == 0 {
		fmt.Println("no engineering department found: the engineering column stays empty")
		return nil
	}
	s, err := c.do(http.MethodGet, base+"/inventory/settings", nil, nil)
	if err != nil {
		return err
	}
	if cur, ok := s["engineering_department_id"].(float64); ok && int64(cur) == w.engDept {
		return nil
	}
	body := map[string]any{"engineering_department_id": w.engDept}
	for _, k := range []string{"costing_method", "price_tolerance_pct", "qty_tolerance_pct", "over_receipt_pct", "po_requires_pr", "direct_receipt_limit", "count_blocks_night_audit", "count_blind", "version"} {
		body[k] = s[k]
	}
	_, err = c.do(http.MethodPut, base+"/inventory/settings", body, nil)
	return err
}

// sell sends and pays one bill of an outlet.
func (w *demoWorld) sell(c *apiClient, base, outlet string, lines []daySale) error {
	bill, err := c.do(http.MethodPost, fmt.Sprintf("%s/pos/outlets/%d/bills", base, w.outlets[outlet]), map[string]any{"client_uuid": uuid4(), "order_type": "TAKEAWAY"}, nil)
	if err != nil {
		return err
	}
	billPath := fmt.Sprintf("%s/pos/bills/%d", base, idOf(bill))
	for _, l := range lines {
		p, ok := w.products[l.product]
		if !ok {
			continue
		}
		opt := map[string]int64{}
		var chosen []map[string]any
		for _, g := range asList(p, "option_groups") {
			for _, o := range asList(g, "options") {
				opt[fmt.Sprint(o["name"])] = idOf(o)
			}
		}
		for _, g := range asList(p, "option_groups") {
			if g["kind"] != "VARIANT" {
				continue
			}
			pick := ""
			for _, o := range asList(g, "options") {
				if def, _ := o["is_default"].(bool); def && pick == "" {
					pick = fmt.Sprint(o["name"])
				}
				for _, want := range l.options {
					if want == fmt.Sprint(o["name"]) {
						pick = want
					}
				}
			}
			chosen = append(chosen, map[string]any{"option_id": opt[pick], "qty": 1})
		}
		for _, want := range l.options {
			id := opt[want]
			dup := false
			for _, c := range chosen {
				dup = dup || c["option_id"] == id
			}
			if id != 0 && !dup {
				chosen = append(chosen, map[string]any{"option_id": id, "qty": 1})
			}
		}
		if _, err = c.do(http.MethodPost, billPath+"/lines", map[string]any{"client_uuid": uuid4(), "product_id": idOf(p), "quantity": fmt.Sprint(l.qty), "options": chosen}, nil); err != nil {
			return err
		}
	}
	if bill, err = c.do(http.MethodPost, billPath+"/send", map[string]any{"client_uuid": uuid4()}, nil); err != nil {
		return err
	}
	totals, _ := bill["totals"].(map[string]any)
	amount := fmt.Sprint(totals["payable"])
	if amount == "" || amount == "<nil>" {
		return fmt.Errorf("the bill has no amount to pay")
	}
	_, err = c.do(http.MethodPost, billPath+"/pay", map[string]any{"payments": []map[string]any{{"method": "CARD", "amount": amount}}}, map[string]string{"Idempotency-Key": uuid4()})
	return err
}

func (w *demoWorld) issue(c *apiClient, base string, in map[string]any) error {
	in["client_uuid"] = uuid4()
	_, err := c.do(http.MethodPost, base+"/inventory/issues", in, nil)
	return err
}

// makeDay does the day: the sales, then the issues.
func (w *demoWorld) makeDay(c *apiClient, base string, d demoDay, date civil.Date) error {
	// one bill for each pair of lines keeps the bills small, like a restaurant's
	for _, l := range d.nasgor {
		if err := w.sell(c, base, "RESTO", []daySale{l}); err != nil {
			return fmt.Errorf("restaurant: %w", err)
		}
	}
	if err := w.sell(c, base, "BAR", d.bar); err != nil {
		return fmt.Errorf("bar: %w", err)
	}
	if d.wasteEggs > 0 {
		if err := w.issue(c, base, map[string]any{"warehouse_id": w.warehouses["UTAMA"], "purpose": "WASTE", "reason_code": "SPOILAGE", "note": "Demo " + date.String(),
			"lines": []map[string]any{{"item_id": w.items["TELUR"], "qty": fmt.Sprint(d.wasteEggs)}}}); err != nil {
			return fmt.Errorf("waste: %w", err)
		}
	}
	if err := w.issue(c, base, map[string]any{"warehouse_id": w.warehouses["UTAMA"], "purpose": "STAFF_MEAL", "department_id": w.depts["FB"], "covers": 20, "note": "Demo " + date.String(),
		"lines": []map[string]any{{"item_id": w.items["BERAS"], "qty": fmt.Sprint(d.staffRiceKg)}}}); err != nil {
		return fmt.Errorf("staff meal: %w", err)
	}
	if d.entertainBotol > 0 {
		if err := w.issue(c, base, map[string]any{"warehouse_id": w.warehouses["BAR"], "purpose": "ENTERTAINMENT", "department_id": w.depts["FB"], "covers": 4, "beneficiary": "Tamu undangan", "note": "Demo " + date.String(),
			"lines": []map[string]any{{"item_id": w.items["SIRUP"], "qty": fmt.Sprint(d.entertainBotol)}}}); err != nil {
			return fmt.Errorf("entertainment: %w", err)
		}
	}
	if d.engineering > 0 && w.engDept != 0 {
		if err := w.issue(c, base, map[string]any{"warehouse_id": w.warehouses["UTAMA"], "purpose": "USE", "department_id": w.engDept, "note": "Demo " + date.String(),
			"lines": []map[string]any{{"item_id": w.items["LAMPU"], "qty": fmt.Sprint(d.engineering)}}}); err != nil {
			return fmt.Errorf("engineering: %w", err)
		}
	}
	if d.office > 0 && w.officeDept != 0 {
		if err := w.issue(c, base, map[string]any{"warehouse_id": w.warehouses["UTAMA"], "purpose": "USE", "department_id": w.officeDept, "note": "Demo " + date.String(),
			"lines": []map[string]any{{"item_id": w.items["KERTASA4"], "qty": fmt.Sprint(d.office)}}}); err != nil {
			return fmt.Errorf("office: %w", err)
		}
	}
	return nil
}

// ensureTargets sets the target cost % of the property when it has none: a little above the middle of the days just made, so that some days are above it and some are not.
func (w *demoWorld) ensureTargets(c *apiClient, base string, from, to civil.Date) error {
	t, err := c.do(http.MethodGet, base+"/inventory/cost-targets", nil, nil)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, x := range asList(t, "data") {
		if x["outlet_id"] == nil {
			have[fmt.Sprint(x["class"])] = true
		}
	}
	r, err := c.do(http.MethodGet, fmt.Sprintf("%s/inventory/reports/daily-cost?from=%s&to=%s", base, from, to), nil, nil)
	if err != nil {
		return err
	}
	for _, class := range []string{"FOOD", "BEVERAGE"} {
		if have[class] {
			continue
		}
		var series []decimal.Decimal
		for _, d := range asList(r, "days") {
			part, _ := d[strings.ToLower(class)].(map[string]any)
			if s, ok := part["pct"].(string); ok {
				if v, err := decimal.NewFromString(s); err == nil {
					series = append(series, v)
				}
			}
		}
		if len(series) == 0 {
			continue
		}
		sort.Slice(series, func(i, j int) bool { return series[i].LessThan(series[j]) })
		// the 60th percentile, rounded up to the half point
		target := series[len(series)*6/10].Mul(decimal.NewFromInt(2)).Ceil().Div(decimal.NewFromInt(2))
		if !target.IsPositive() {
			continue
		}
		if _, err := c.do(http.MethodPut, base+"/inventory/cost-targets", map[string]any{"outlet_id": nil, "class": class, "target_pct": target.String()}, nil); err != nil {
			return err
		}
		fmt.Printf("target %s cost: %s%%\n", strings.ToLower(class), target)
	}
	return nil
}

// settleOpenBills pays the bills of the outlets that a run which stopped half way left open, so that the night audit does not stop on them.
func (w *demoWorld) settleOpenBills(c *apiClient, base string) error {
	for _, outlet := range []string{"RESTO", "BAR"} {
		list, err := c.do(http.MethodGet, fmt.Sprintf("%s/pos/outlets/%d/bills?status=OPEN", base, w.outlets[outlet]), nil, nil)
		if err != nil {
			return err
		}
		for _, b := range asList(list, "data") {
			bill, err := c.do(http.MethodGet, fmt.Sprintf("%s/pos/bills/%d", base, idOf(b)), nil, nil)
			if err != nil {
				return err
			}
			totals, _ := bill["totals"].(map[string]any)
			amount := fmt.Sprint(totals["payable"])
			if amount == "" || amount == "<nil>" || amount == "0" {
				continue
			}
			if _, err := c.do(http.MethodPost, fmt.Sprintf("%s/pos/bills/%d/pay", base, idOf(b)), map[string]any{"payments": []map[string]any{{"method": "CARD", "amount": amount}}}, map[string]string{"Idempotency-Key": uuid4()}); err != nil {
				return fmt.Errorf("settle bill %d: %w", idOf(b), err)
			}
		}
	}
	return nil
}

// blockersOf names what stops the night audit, to tell the person why a day could not be closed.
func blockersOf(c *apiClient, base string) string {
	pv, err := c.do(http.MethodGet, base+"/night-audit/preview", nil, nil)
	if err != nil {
		return ""
	}
	var out []string
	if groups, ok := pv["blockers"].(map[string]any); ok {
		for name, list := range groups {
			if l, ok := list.([]any); ok {
				for _, x := range l {
					if m, ok := x.(map[string]any); ok {
						out = append(out, fmt.Sprintf("%s:%v %v", name, m["code"], m["message"]))
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return " (blockers: " + strings.Join(out, "; ") + ")"
}

// extendDueStays keeps the guests of the demo in the house: a stay that is due out and not checked out would stop the night audit, and the demo is about the stock, so its departure is moved to a later date.
func extendDueStays(c *apiClient, base string, until civil.Date) error {
	pv, err := c.do(http.MethodGet, base+"/night-audit/preview", nil, nil)
	if err != nil {
		return err
	}
	groups, _ := pv["blockers"].(map[string]any)
	list, _ := groups["unresolved_departures"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		id := int64(0)
		if f, ok := m["stay_id"].(float64); ok {
			id = int64(f)
		}
		if id == 0 {
			continue
		}
		stay, err := c.do(http.MethodGet, fmt.Sprintf("%s/stays/%d", base, id), nil, nil)
		if err != nil {
			return err
		}
		if _, err := c.do(http.MethodPost, fmt.Sprintf("%s/stays/%d/change-departure", base, id), map[string]any{"version": versionOfStay(stay), "departure_date": until.String()}, nil); err != nil {
			return fmt.Errorf("keep stay %d in the house: %w", id, err)
		}
		fmt.Printf("stay %v stays in the house until %s\n", m["stay_number"], until)
	}
	return nil
}

func versionOfStay(stay map[string]any) any {
	if inner, ok := stay["stay"].(map[string]any); ok {
		return inner["version"]
	}
	return stay["version"]
}
