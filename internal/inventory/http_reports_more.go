package inventory

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the other reports (35-inventory.md 12.5, section 22). Each answers JSON, and a CSV file with ?format=csv as the reports of PR 5 do.
func (h *Handler) registerMoreReports(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/reports/waste", httpx.HandlerFunc(h.reportWaste))
	mux.Handle("GET "+p+"/reports/count-variances", httpx.HandlerFunc(h.reportCountVariances))
	mux.Handle("GET "+p+"/reports/price-variances", httpx.HandlerFunc(h.reportPriceVariances))
	mux.Handle("GET "+p+"/reports/transfers-without-requisition", httpx.HandlerFunc(h.reportTransfersNoSR))
	mux.Handle("GET "+p+"/reports/amenities-by-month", httpx.HandlerFunc(h.reportAmenityMonths))
	mux.Handle("GET "+p+"/reports/pending-approvals", httpx.HandlerFunc(h.reportPendingApprovals))
	mux.Handle("GET "+p+"/reports/recipe-costs", httpx.HandlerFunc(h.reportRecipeCosts))
	mux.Handle("GET "+p+"/reports/meals", httpx.HandlerFunc(h.reportMeals))
	mux.Handle("GET "+p+"/reports/cost-day", httpx.HandlerFunc(h.reportCostDay))
	mux.Handle("POST "+p+"/requests/from-low-stock", httpx.HandlerFunc(h.requestFromLowStock))
}

func (h *Handler) reportWaste(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	var f WasteFilter
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		return err
	}
	if f.DepartmentID, err = queryInt(r, "department_id"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	rep, err := h.svc.Waste(r.Context(), pid, from, to, f)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"reason", "warehouse", "department", "item", "name", "category", "unit", "qty", "value", "documents"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.Reason, x.WarehouseName, x.DepartmentName, x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.Qty), dec(x.Value), strconv.Itoa(x.Docs)})
	}
	return writeCSV(w, r, "waste-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportCountVariances(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	wid, err := queryInt(r, "warehouse_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.CountVariances(r.Context(), pid, from, to, wid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"count", "approved_on", "warehouse", "item", "name", "category", "unit", "book_qty", "counted_qty", "variance_qty", "variance_value"}}
	for _, x := range rep.Lines {
		rows = append(rows, []string{x.CountNumber, x.ApprovedOn.String(), x.WarehouseCode, x.ItemCode, x.ItemName, x.CategoryName, x.UnitCode, dec(x.BookQty), dec(x.CountedQty), dec(x.VarianceQty), dec(x.VarianceValue)})
	}
	rows = append(rows, []string{}, []string{"count", "approved_on", "warehouse", "approver", "gain", "loss", "net", "lines"})
	for _, c := range rep.Counts {
		rows = append(rows, []string{c.Number, c.ApprovedOn.String(), c.WarehouseCode, c.ApproverName, dec(c.Gain), dec(c.Loss), dec(c.Net), strconv.Itoa(c.LinesWithVariance)})
	}
	return writeCSV(w, r, "count-variances-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportPriceVariances(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	sid, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.PriceVariances(r.Context(), pid, from, to, sid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"supplier", "item", "name", "unit", "qty", "receipt_value", "bill_cost", "receipt_unit_price", "bill_unit_price", "variance", "pct", "bills", "lines_above_tolerance"}}
	for _, s := range rep.Suppliers {
		for _, x := range s.Items {
			rows = append(rows, []string{s.SupplierName, x.ItemCode, x.ItemName, x.UnitCode, dec(x.Qty), dec(x.ReceiptValue), dec(x.BillCost), decp(x.ReceiptPrice), decp(x.BillPrice), dec(x.Variance), decp(x.Pct),
				strconv.Itoa(x.Bills), strconv.Itoa(x.LinesAboveTol)})
		}
	}
	return writeCSV(w, r, "price-variances-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportTransfersNoSR(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	fw, err := queryInt(r, "from_warehouse_id")
	if err != nil {
		return err
	}
	tw, err := queryInt(r, "to_warehouse_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.TransfersWithoutRequisition(r.Context(), pid, from, to, fw, tw)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"transfer", "date", "status", "from_warehouse", "to_warehouse", "created_by", "item", "name", "unit", "qty", "value"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.Number, x.BusinessDate.String(), x.Status, x.FromWarehouse, x.ToWarehouse, x.CreatedByName, x.ItemCode, x.ItemName, x.UnitCode, dec(x.Qty), dec(x.Value)})
	}
	return writeCSV(w, r, "transfers-without-requisition-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportAmenityMonths(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.AmenityMonths(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"month", "item", "name", "unit", "qty", "cost", "occupied_rooms", "guests", "month_cost", "cost_per_room", "prev_cost_per_room", "change_pct"}}
	for _, m := range rep.Months {
		head := []string{m.Month.String()[:7]}
		tail := []string{strconv.Itoa(m.Rooms), strconv.Itoa(m.Guests), dec(m.Cost), decp(m.CostPerRoom), decp(m.PrevCostPerRoom), decp(m.ChangePct)}
		if len(m.Items) == 0 {
			rows = append(rows, append(append(head, "", "", "", "", ""), tail...))
		}
		for _, it := range m.Items {
			rows = append(rows, append(append(append([]string{}, head...), it.ItemCode, it.ItemName, it.UnitCode, dec(it.Qty), dec(it.Cost)), tail...))
		}
	}
	return writeCSV(w, r, "amenities-by-month-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportPendingApprovals(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.PendingApprovals(r.Context(), pid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"type", "number", "department", "requester", "level", "step", "awaiting", "entered_at", "age_hours", "value"}}
	for _, x := range rep.Rows {
		at := ""
		if x.EnteredAt != nil {
			at = x.EnteredAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		rows = append(rows, []string{x.DocType, x.Number, x.DepartmentName, x.RequesterName, strconv.Itoa(x.LevelNo), x.StepName, x.Awaiting, at, strconv.FormatInt(x.AgeHours, 10), dec(x.Value)})
	}
	return writeCSV(w, r, "pending-approvals.csv", rows)
}

func (h *Handler) reportRecipeCosts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	oid, err := queryInt(r, "outlet_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.RecipeCosts(r.Context(), pid, oid)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"outlet", "product", "name", "category", "class", "has_recipe", "cost", "net_price", "margin", "cost_pct", "target_pct", "above_target"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.OutletName, x.ProductCode, x.ProductName, x.CategoryName, x.Class, strconv.FormatBool(x.HasRecipe), decp(x.Cost), decp(x.NetPrice), decp(x.Margin), decp(x.CostPct), decp(x.TargetPct),
			strconv.FormatBool(x.AboveTarget)})
	}
	return writeCSV(w, r, "recipe-costs.csv", rows)
}

func (h *Handler) reportMeals(w http.ResponseWriter, r *http.Request) error {
	pid, from, to, err := period(r)
	if err != nil {
		return err
	}
	dept, err := queryInt(r, "department_id")
	if err != nil {
		return err
	}
	rep, err := h.svc.Meals(r.Context(), pid, from, to, dept)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"department", "kind", "documents", "covers", "value", "cost_per_cover"}}
	for _, x := range rep.Rows {
		rows = append(rows, []string{x.DepartmentName, x.Purpose, strconv.Itoa(x.Docs), strconv.Itoa(x.Covers), dec(x.Value), decp(x.CostPerCover)})
	}
	return writeCSV(w, r, "meals-"+rep.From.String()+"-"+rep.To.String()+".csv", rows)
}

func (h *Handler) reportCostDay(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	d, err := queryDate(r, "date")
	if err != nil {
		return err
	}
	var date civil.Date
	if d != nil {
		date = *d
	} else {
		return invalid("report", fieldErr("date", "REQUIRED", "YYYY-MM-DD"))
	}
	rep, err := h.svc.CostDay(r.Context(), pid, date)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := [][]string{{"kind", "source_type", "document", "department", "outlet", "lines", "amount"}}
	for _, k := range rep.Kinds {
		for _, x := range k.Docs {
			rows = append(rows, []string{x.CostKind, x.SourceType, x.Number, x.DepartmentName, x.OutletName, strconv.Itoa(x.Lines), dec(x.Amount)})
		}
	}
	return writeCSV(w, r, "cost-day-"+rep.Date.String()+".csv", rows)
}

func (h *Handler) requestFromLowStock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[LowStockRequestInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateRequestFromLowStock(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}
