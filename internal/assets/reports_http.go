package assets

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The report routes (docs/architecture/37-fixed-assets.md 4.12). Each answers JSON, or a CSV with `?format=csv`; the print view is made by the screen from the same answer. The permission is checked
// before the filters are read, so a user without it gets 403 whatever the query says.

func (h *Handler) registerReports(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/assets"
	mux.Handle("GET "+p+"/reports/register", httpx.HandlerFunc(h.reportRegister))
	mux.Handle("GET "+p+"/reports/schedule", httpx.HandlerFunc(h.reportSchedule))
	mux.Handle("GET "+p+"/reports/recap", httpx.HandlerFunc(h.reportRecap))
	mux.Handle("GET "+p+"/reports/fiscal", httpx.HandlerFunc(h.reportFiscal))
	mux.Handle("GET "+p+"/reports/locations", httpx.HandlerFunc(h.reportLocations))
	mux.Handle("GET "+p+"/reports/expiring", httpx.HandlerFunc(h.reportExpiring))
}

// reportProperty reads the property and checks the permission to read before anything else is read from the request.
func (h *Handler) reportProperty(r *http.Request) (int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, err
	}
	if _, err := h.svc.requireRead(r.Context(), pid); err != nil {
		return 0, err
	}
	return pid, nil
}

func wantsCSV(r *http.Request) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "", "json":
		return false, nil
	case "csv":
		return true, nil
	}
	return false, apperr.Invalid("the format is invalid", fieldErr("format", "INVALID_VALUE", "json or csv"))
}

func queryIntOr(r *http.Request, name string, def int) (int, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, apperr.Invalid("the filter is invalid", fieldErr(name, "INVALID_VALUE", "a whole number"))
	}
	return n, nil
}

func writeCSV(w http.ResponseWriter, r *http.Request, filename string, head []string, rows [][]string) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	cw := csv.NewWriter(w)
	if err := cw.Write(csvlang.Header(r, head)); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func intStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func (h *Handler) reportRegister(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	var f RegisterFilter
	if f.AsOf, err = queryDate(r, "as_of"); err != nil {
		return err
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	if f.LocationID, err = queryInt(r, "location_id"); err != nil {
		return err
	}
	if f.DepartmentID, err = queryInt(r, "department_id"); err != nil {
		return err
	}
	rep, err := h.svc.RegisterReport(r.Context(), pid, f)
	if err != nil {
		return err
	}
	if !asCSV {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	rows := make([][]string, 0, len(rep.Rows))
	for _, a := range rep.Rows {
		life := ""
		if a.LifeMonths != nil {
			life = strconv.Itoa(int(*a.LifeMonths))
		}
		rows = append(rows, []string{a.Number, a.Name, a.CategoryCode, str(a.LocationName), str(a.DepartmentName), str(a.SupplierName), a.AcquisitionDate.String(), a.InServiceDate.String(),
			a.Cost.String(), a.AccumDepr.String(), a.CarryingValue.String(), life, strconv.Itoa(a.MonthsDone), intStr(a.MonthsLeft), str(a.Method), a.Status, str(a.SerialNo), dateStr(a.WarrantyUntil)})
	}
	return writeCSV(w, r, "asset-register-"+rep.AsOf.String()+".csv", []string{"number", "name", "category", "location", "department", "supplier", "acquisition_date", "in_service_date", "cost",
		"accumulated_depreciation", "carrying_value", "life_months", "months_done", "months_left", "method", "status", "serial_no", "warranty_until"}, rows)
}

func (h *Handler) reportSchedule(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	assetID, err := queryInt(r, "asset_id")
	if err != nil {
		return err
	}
	categoryID, err := queryInt(r, "category_id")
	if err != nil {
		return err
	}
	list, err := h.svc.ScheduleReport(r.Context(), pid, assetID, categoryID)
	if err != nil {
		return err
	}
	if !asCSV {
		return list2(w, list)
	}
	var rows [][]string
	for _, a := range list {
		for _, x := range a.Rows {
			journal, fiscal, fiscalAfter := "", "", ""
			if x.JournalNumber != nil {
				journal = *x.JournalNumber
			}
			if x.FiscalAmount != nil {
				fiscal, fiscalAfter = x.FiscalAmount.String(), x.FiscalAccumAfter.String()
			}
			rows = append(rows, []string{a.Number, a.Name, x.Month.String(), x.Kind, strconv.Itoa(x.Months), x.Amount.String(), x.AccumAfter.String(), x.CarryingAfter.String(), strconv.Itoa(x.MonthsDone),
				journal, fiscal, fiscalAfter})
		}
	}
	return writeCSV(w, r, "asset-depreciation-schedule.csv", []string{"number", "name", "month", "kind", "months", "amount", "accumulated_after", "carrying_after", "months_done", "journal",
		"fiscal_amount", "fiscal_accumulated_after"}, rows)
}

func list2(w http.ResponseWriter, l []AssetSchedule) error {
	if l == nil {
		l = []AssetSchedule{}
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[AssetSchedule]{Data: l})
}

func (h *Handler) reportRecap(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	from, err := queryDate(r, "date_from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "date_to")
	if err != nil {
		return err
	}
	rep, err := h.svc.RecapReport(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !asCSV {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	causes := func(cs []DifferenceCause) string {
		parts := make([]string, 0, len(cs))
		for _, c := range cs {
			parts = append(parts, c.Code+"="+c.Amount.String())
		}
		return strings.Join(parts, "; ")
	}
	rows := make([][]string, 0, len(rep.Rows)+1)
	for _, x := range append(append([]RecapRow{}, rep.Rows...), rep.Total) {
		rows = append(rows, []string{x.CategoryCode, x.CategoryName, x.CostOpening.String(), x.CostAdditions.String(), x.CostReclass.String(), x.CostDisposals.String(), x.CostClosing.String(),
			x.AccumOpening.String(), x.AccumAdditions.String(), x.AccumDepreciation.String(), x.AccumReclass.String(), x.AccumDisposals.String(), x.AccumClosing.String(), x.CarryingClosing.String(),
			x.LedgerCostClosing.String(), x.LedgerAccumClosing.String(), x.CostDifference.String(), x.AccumDifference.String(), causes(x.CostCauses), causes(x.AccumCauses)})
	}
	return writeCSV(w, r, "asset-recap-"+rep.DateFrom.String()+"-"+rep.DateTo.String()+".csv", []string{"category", "name", "cost_opening", "cost_additions", "cost_reclass", "cost_disposals",
		"cost_closing", "accum_opening", "accum_additions", "accum_depreciation", "accum_reclass", "accum_disposals", "accum_closing", "carrying_closing", "ledger_cost_closing",
		"ledger_accum_closing", "cost_difference", "accum_difference", "cost_causes", "accum_causes"}, rows)
}

func (h *Handler) reportFiscal(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	var f FiscalFilter
	if v := strings.TrimSpace(r.URL.Query().Get("tax_year")); v != "" {
		n, err := queryIntOr(r, "tax_year", 0)
		if err != nil {
			return err
		}
		f.TaxYear = &n
	}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	if f.AssetID, err = queryInt(r, "asset_id"); err != nil {
		return err
	}
	rep, err := h.svc.FiscalReport(r.Context(), pid, f)
	if err != nil {
		return err
	}
	if !asCSV {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	yn := func(b bool) string {
		if b {
			return "Y"
		}
		return ""
	}
	rows := make([][]string, 0, len(rep.Rows))
	for _, x := range rep.Rows {
		rows = append(rows, []string{strconv.Itoa(rep.TaxYear), x.Number, x.Name, x.CategoryCode, x.FiscalGroup, x.FiscalMethod, x.Commercial.String(), x.Fiscal.String(), x.Difference.String(),
			x.FiscalAccum.String(), yn(x.Partial), yn(x.Estimated)})
	}
	return writeCSV(w, r, "asset-fiscal-"+strconv.Itoa(rep.TaxYear)+".csv", []string{"tax_year", "number", "name", "category", "fiscal_group", "fiscal_method", "commercial", "fiscal", "difference",
		"fiscal_accumulated", "partial_year", "fiscal_estimated"}, rows)
}

func (h *Handler) reportLocations(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.PlacementReport(r.Context(), pid)
	if err != nil {
		return err
	}
	if !asCSV {
		return httpx.WriteJSON(w, http.StatusOK, rep)
	}
	var rows [][]string
	add := func(kind string, x PlacementRow) {
		rows = append(rows, []string{kind, x.Code, x.Name, strconv.Itoa(x.Assets), x.Cost.String(), x.AccumDepr.String(), x.CarryingValue.String(), strconv.Itoa(x.TotalAssets), x.TotalCost.String(),
			x.TotalCarryingValue.String()})
	}
	for _, x := range rep.Locations {
		add("LOCATION", x)
	}
	add("NO_LOCATION", rep.NoLocation)
	for _, x := range rep.Departments {
		add("DEPARTMENT", x)
	}
	add("NO_DEPARTMENT", rep.NoDepartment)
	return writeCSV(w, r, "asset-locations-departments.csv", []string{"type", "code", "name", "assets", "cost", "accumulated_depreciation", "carrying_value", "total_assets", "total_cost",
		"total_carrying_value"}, rows)
}

func (h *Handler) reportExpiring(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	days, err := queryIntOr(r, "days", 90)
	if err != nil {
		return err
	}
	months, err := queryIntOr(r, "months", 6)
	if err != nil {
		return err
	}
	rows, err := h.svc.ExpiringReport(r.Context(), pid, days, months)
	if err != nil {
		return err
	}
	if !asCSV {
		return list(w, rows, nil)
	}
	yn := func(b bool) string {
		if b {
			return "Y"
		}
		return ""
	}
	out := make([][]string, 0, len(rows))
	for _, x := range rows {
		out = append(out, []string{x.Number, x.Name, x.CategoryCode, str(x.LocationName), x.Cost.String(), x.CarryingValue.String(), dateStr(x.WarrantyUntil), yn(x.WarrantyEnding), yn(x.WarrantyExpired),
			intStr(x.MonthsLeft), yn(x.LifeEnding), yn(x.FullyDepreciated)})
	}
	return writeCSV(w, r, "asset-warranty-life.csv", []string{"number", "name", "category", "location", "cost", "carrying_value", "warranty_until", "warranty_ending", "warranty_expired", "months_left",
		"life_ending", "fully_depreciated"}, out)
}
