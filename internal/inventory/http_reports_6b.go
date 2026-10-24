package inventory

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the reports of PR 6b (35-inventory.md 12.5, section 22.5). Each answers JSON, and a CSV file with ?format=csv like the other reports.
func (h *Handler) registerReports6b(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/reports/item-movements", httpx.HandlerFunc(h.reportItemMovements))
	mux.Handle("GET "+p+"/reports/purchases", httpx.HandlerFunc(h.reportPurchases))
	mux.Handle("GET "+p+"/reports/price-history", httpx.HandlerFunc(h.reportPriceHistory))
	mux.Handle("GET "+p+"/reports/slow-stock", httpx.HandlerFunc(h.reportSlowStock))
	mux.Handle("GET "+p+"/reports/top-usage", httpx.HandlerFunc(h.reportTopUsage))
}

func (h *Handler) reportItemMovements(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	var f ItemMovementFilter
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	if f.ItemID, err = queryInt(r, "item_id"); err != nil {
		return err
	}
	rep, err := h.svc.ItemMovements(r.Context(), pid, from, to, f)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	head := []string{"item", "name", "category", "unit", "opening_qty", "opening_value"}
	order := append(append(append([]string{}, bucketsIn...), bucketsOut...), MoveCountVariance, MoveCostAdjust)
	for _, b := range order {
		head = append(head, strings.ToLower(b)+"_qty", strings.ToLower(b)+"_value")
	}
	head = append(head, "closing_qty", "closing_value")
	rows := [][]string{head}
	for _, x := range rep.Rows {
		line := []string{x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Opening.Qty), dec(x.Opening.Value)}
		for _, b := range order {
			line = append(line, dec(x.Buckets[b].Qty), dec(x.Buckets[b].Value))
		}
		rows = append(rows, append(line, dec(x.Closing.Qty), dec(x.Closing.Value)))
	}
	return writeCSV(w, r, "item-movements-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportPurchases(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	var f PurchaseFilter
	if f.SupplierID, err = queryInt(r, "supplier_id"); err != nil {
		return err
	}
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	rep, err := h.svc.Purchases(r.Context(), pid, from, to, f)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"supplier", "item", "name", "category", "unit", "qty", "value", "avg_price", "receipts", "bills", "billed_value"}}
	for _, s := range rep.Suppliers {
		for _, x := range s.Items {
			rows = append(rows, []string{s.SupplierName, x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Qty), dec(x.Value), decp(x.AvgPrice), strconv.Itoa(x.Receipts), strconv.Itoa(x.Bills), dec(x.BilledValue)})
		}
	}
	return writeCSV(w, r, "purchases-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportPriceHistory(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	item, err := queryInt(r, "item_id")
	if err != nil {
		return err
	}
	sid, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.PriceHistory(r.Context(), pid, item, from, to, sid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"date", "receipt", "supplier", "item", "name", "purchase_unit", "qty_purchase", "unit_price", "unit", "price_per_stock", "change_pct"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.ReceivedOn.String(), x.ReceiptNumber, x.SupplierName, rep.ItemCode, rep.ItemName, x.PurchaseUnitCode, dec(x.QtyPurchase), dec(x.UnitPrice), rep.UnitCode, dec(x.PricePerStock), decp(x.ChangePct)})
	}
	return writeCSV(w, r, "price-history-"+rep.ItemCode+"-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportSlowStock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var f SlowStockFilter
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	days, err := queryInt(r, "days")
	if err != nil {
		return err
	}
	n := 0
	if days != nil {
		n = int(*days)
	}
	rep, err := h.svc.SlowStock(r.Context(), pid, n, f)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"warehouse", "item", "name", "category", "unit", "qty", "value", "last_out_on", "days_since_out", "never_out", "age_days"}}
	for _, x := range rep.Rows {
		last, since, age := "", "", ""
		if x.LastOutOn != nil {
			last, since = x.LastOutOn.String(), strconv.Itoa(*x.DaysSinceOut)
		}
		if x.AgeDays != nil {
			age = strconv.Itoa(*x.AgeDays)
		}
		rows = append(rows, []string{x.WarehouseCode, x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Qty), dec(x.Value), last, since, strconv.FormatBool(x.NeverOut), age})
	}
	return writeCSV(w, r, "slow-stock-"+rep.AsOf.String()+".csv", rows)
}

func (h *Handler) reportTopUsage(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	var f TopUsageFilter
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		return err
	}
	if limit != nil {
		f.Limit = int(*limit)
	}
	for _, k := range strings.Split(r.URL.Query().Get("kinds"), ",") {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
			f.Kinds = append(f.Kinds, k)
		}
	}
	rep, err := h.svc.TopUsage(r.Context(), pid, from, to, f)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"rank", "item", "name", "category", "unit", "qty", "value", "share_pct", "cumulative_pct"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{strconv.Itoa(x.Rank), x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Qty), dec(x.Value), dec(x.SharePct), dec(x.CumulativePct)})
	}
	return writeCSV(w, r, "top-usage-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}
