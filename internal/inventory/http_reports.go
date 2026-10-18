package inventory

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the reports (35-inventory.md 12): each answers JSON, and a CSV file with ?format=csv (the names of the columns follow ?lang=id, the cells stay plain numbers and ISO dates).
func (h *Handler) registerReports(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/dashboard", httpx.HandlerFunc(h.dashboard))
	mux.Handle("GET "+p+"/cost-targets", httpx.HandlerFunc(h.listCostTargets))
	mux.Handle("PUT "+p+"/cost-targets", httpx.HandlerFunc(h.putCostTarget))
	mux.Handle("DELETE "+p+"/cost-targets", httpx.HandlerFunc(h.deleteCostTarget))
	mux.Handle("GET "+p+"/reports/daily-cost", httpx.HandlerFunc(h.reportDailyCost))
	mux.Handle("GET "+p+"/reports/outlet-cost", httpx.HandlerFunc(h.reportOutletCost))
	mux.Handle("GET "+p+"/reports/fb-reconciliation", httpx.HandlerFunc(h.reportReconciliation))
	mux.Handle("GET "+p+"/reports/stock-value", httpx.HandlerFunc(h.reportStockValue))
	mux.Handle("GET "+p+"/reports/low-stock", httpx.HandlerFunc(h.reportLowStock))
	mux.Handle("GET "+p+"/reports/negative-stock", httpx.HandlerFunc(h.reportNegativeStock))
	mux.Handle("GET "+p+"/reports/received-not-invoiced", httpx.HandlerFunc(h.reportGrni))
	mux.Handle("GET "+p+"/reports/usage", httpx.HandlerFunc(h.reportUsage))
}

func wantsCSV(r *http.Request) bool { return r.URL.Query().Get("format") == "csv" }

// writeCSV answers rows as a download. Text that a spreadsheet could read as a formula is prefixed with an apostrophe.
func writeCSV(w http.ResponseWriter, r *http.Request, filename string, rows [][]string) error {
	if len(rows) > 0 {
		rows[0] = csvlang.Header(r, rows[0])
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	out := csv.NewWriter(w)
	for _, row := range rows {
		safe := make([]string, len(row))
		for i, c := range row {
			if c != "" && (strings.ContainsRune("=+@\t\r", rune(c[0])) || strings.HasPrefix(c, "-") && !isNumber(c)) {
				c = "'" + c
			}
			safe[i] = c
		}
		if err := out.Write(safe); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func dec(d decimal.Decimal) string { return d.String() }

func decp(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func period(r *http.Request) (pid int64, from, to *civil.Date, err error) {
	if pid, err = tenancy.PropertyID(r); err != nil {
		return
	}
	if from, err = queryDate(r, "from"); err != nil {
		return
	}
	to, err = queryDate(r, "to")
	return
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) error {
	return jsonOf(w, r, http.StatusOK, func(pid int64) (Dashboard, error) { return h.svc.Dashboard(r.Context(), pid) })
}

func (h *Handler) listCostTargets(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListCostTargets(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) putCostTarget(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[CostTarget](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetCostTarget(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) deleteCostTarget(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	outlet, err := queryInt(r, "outlet_id")
	if err != nil {
		return err
	}
	class := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("class")))
	if err := h.svc.DeleteCostTarget(r.Context(), pid, outlet, class); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) reportDailyCost(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.DailyCost(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	head := []string{"date", "live"}
	head = append(head, costKinds...)
	head = append(head, "total", "food_revenue", "food_cost", "food_pct", "beverage_revenue", "beverage_cost", "beverage_pct", "fb_pct", "occupied_rooms", "amenity_cost_per_room", "food_no_recipe_days", "beverage_no_recipe_days")
	rows := [][]string{head}
	line := func(label, live string, d DailyCostDay) []string {
		row := []string{label, live}
		for _, k := range costKinds {
			row = append(row, dec(d.Kinds[k]))
		}
		return append(row, dec(d.Total), dec(d.Food.Revenue), dec(d.Food.Cost), decp(d.Food.Pct), dec(d.Beverage.Revenue), dec(d.Beverage.Cost), decp(d.Beverage.Pct), decp(d.Fb.Pct),
			strconv.Itoa(d.Rooms), decp(d.CostPerRoom), strconv.Itoa(d.Food.NoRecipeDays), strconv.Itoa(d.Beverage.NoRecipeDays))
	}
	for _, d := range rep.Days {
		rows = append(rows, line(d.Date.String(), strconv.FormatBool(d.Live), d))
	}
	rows = append(rows, line("Period", "", rep.Period), line("Month to date", "", rep.Mtd))
	rows[len(rows)-2][0], rows[len(rows)-1][0] = csvlang.Word(r, "Period"), csvlang.Word(r, "Month to date")
	return writeCSV(w, r, "daily-cost-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportOutletCost(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.OutletCost(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"outlet", "name", "class", "revenue", "theory", "theory_pct", "count_variance", "actual", "actual_pct", "difference", "basis", "waste", "staff_meal", "entertainment", "target_pct", "no_recipe_days"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.OutletCode, x.OutletName, x.Class, dec(x.Revenue), dec(x.Theory), decp(x.TheoryPct), dec(x.CountVariance), dec(x.Actual), decp(x.ActualPct), dec(x.Difference),
			x.Basis, dec(x.Waste), dec(x.StaffMeal), dec(x.Entertainment), decp(x.TargetPct), strconv.Itoa(x.NoRecipeDays)})
	}
	return writeCSV(w, r, "fb-cost-per-outlet-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportReconciliation(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.FbReconciliation(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"line", "food", "beverage", "total"}}
	add := func(name string, get func(ReconColumn) string) {
		rows = append(rows, []string{name, get(rep.Food), get(rep.Beverage), get(rep.Total)})
	}
	add("opening", func(c ReconColumn) string { return dec(c.Opening) })
	add("opening_posted", func(c ReconColumn) string { return dec(c.OpeningPosted) })
	add("purchases", func(c ReconColumn) string { return dec(c.Purchases) })
	add("transfer_in", func(c ReconColumn) string { return dec(c.TransferIn) })
	add("transfer_out", func(c ReconColumn) string { return dec(c.TransferOut) })
	add("closing", func(c ReconColumn) string { return dec(c.Closing) })
	add("goods_used", func(c ReconColumn) string { return dec(c.GoodsUsed) })
	add("staff_meal", func(c ReconColumn) string { return dec(c.StaffMeal) })
	add("entertainment", func(c ReconColumn) string { return dec(c.Entertainment) })
	add("net_cost", func(c ReconColumn) string { return dec(c.NetCost) })
	add("revenue", func(c ReconColumn) string { return dec(c.Revenue) })
	add("actual_pct", func(c ReconColumn) string { return decp(c.ActualPct) })
	add("theory", func(c ReconColumn) string { return dec(c.Theory) })
	add("theory_pct", func(c ReconColumn) string { return decp(c.TheoryPct) })
	add("difference", func(c ReconColumn) string { return dec(c.Difference) })
	add("waste", func(c ReconColumn) string { return dec(c.Waste) })
	add("count_variance", func(c ReconColumn) string { return dec(c.CountVariance) })
	add("no_recipe_estimate", func(c ReconColumn) string { return dec(c.NoRecipeEstimate) })
	add("other_outflows", func(c ReconColumn) string { return dec(c.OtherOutflows) })
	rows = append(rows, []string{}, []string{"gl_check", "stock", "gl", "difference"})
	for _, l := range rep.GL.Lines {
		rows = append(rows, []string{l.Key, dec(l.Stock), dec(l.GL), dec(l.Diff)})
	}
	return writeCSV(w, r, "fb-reconciliation-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportStockValue(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	asOf, err := queryDate(r, "as_of")
	if err != nil {
		return err
	}
	rep, err := h.svc.StockValue(r.Context(), pid, asOf)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"warehouse", "name", "category", "category_name", "items", "value"}}
	for _, wh := range rep.Warehouses {
		for _, c := range wh.Cells {
			rows = append(rows, []string{wh.Code, wh.Name, c.CategoryCode, c.CategoryName, strconv.Itoa(c.Items), dec(c.Value)})
		}
	}
	rows = append(rows, []string{}, []string{"account", "name", "stock", "gl", "difference"})
	for _, a := range rep.Accounts {
		rows = append(rows, []string{a.Code, a.Name, dec(a.Stock), dec(a.GL), dec(a.Diff)})
	}
	return writeCSV(w, r, "stock-value-"+rep.AsOf.String()+".csv", rows)
}

func (h *Handler) reportLowStock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	wid, err := queryInt(r, "warehouse_id")
	if err != nil {
		return err
	}
	list, err := h.svc.LowStock(r.Context(), pid, wid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return page(w, list, nil)
	}
	rows := [][]string{{"warehouse", "item", "name", "unit", "qty", "min_qty", "reorder_qty", "missing", "value"}}
	for _, x := range list {
		rows = append(rows, []string{x.WarehouseCode, x.ItemCode, x.ItemName, x.UnitCode, dec(x.Qty), dec(x.MinQty), dec(x.ReorderQty), dec(x.Missing), dec(x.Value)})
	}
	return writeCSV(w, r, "low-stock.csv", rows)
}

func (h *Handler) reportNegativeStock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.NegativeStock(r.Context(), pid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"warehouse", "item", "name", "unit", "qty_open", "est_unit_cost", "est_value", "balance_qty", "since", "age_days", "source"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.WarehouseCode, x.ItemCode, x.ItemName, x.UnitCode, dec(x.QtyOpen), dec(x.EstUnitCost), dec(x.EstValue), dec(x.BalanceQty), x.Since.String(), strconv.Itoa(x.AgeDays), x.Source})
	}
	return writeCSV(w, r, "negative-stock-"+rep.BusinessDate.String()+".csv", rows)
}

func (h *Handler) reportGrni(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.ReceivedNotInvoiced(r.Context(), pid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"receipt", "received_on", "supplier", "warehouse", "status", "age_days", "bucket", "value"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.ReceiptNumber, x.ReceivedOn.String(), x.SupplierName, x.WarehouseName, x.Status, strconv.Itoa(x.AgeDays), x.Bucket, dec(x.Value)})
	}
	return writeCSV(w, r, "received-not-invoiced-"+rep.AsOf.String()+".csv", rows)
}

func (h *Handler) reportUsage(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	dept, err := queryInt(r, "department_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.Usage(r.Context(), pid, from, to, dept)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"department", "kind", "item", "name", "category", "unit", "qty", "value"}}
	for _, d := range rep.Departments {
		for _, x := range d.Items {
			rows = append(rows, []string{d.Name, x.Kind, x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Qty), dec(x.Value)})
		}
	}
	return writeCSV(w, r, "usage-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}
