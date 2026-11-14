package operator

import (
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/entitlement"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/subscription"
)

// Register mounts the routes of the console that need an operator (doc 39 section 7). The caller has put the operator's authentication in front of them.
func (c *Console) Register(mux *http.ServeMux) {
	const p = "/api/v1/platform"
	mux.Handle("GET "+p+"/tenants", httpx.HandlerFunc(c.listTenants))
	mux.Handle("GET "+p+"/tenants/export.csv", httpx.HandlerFunc(c.exportTenants))
	mux.Handle("POST "+p+"/tenants", httpx.HandlerFunc(c.createTenant))
	mux.Handle("GET "+p+"/tenants/{code}", httpx.HandlerFunc(c.getTenant))
	mux.Handle("GET "+p+"/tenants/{code}/admins", httpx.HandlerFunc(c.tenantAdmins))
	mux.Handle("GET "+p+"/tenants/{code}/activity", httpx.HandlerFunc(c.tenantActivity))
	mux.Handle("PATCH "+p+"/tenants/{code}/subscription", httpx.HandlerFunc(c.changeSubscription))
	mux.Handle("POST "+p+"/tenants/{code}/suspend", httpx.HandlerFunc(c.suspend))
	mux.Handle("POST "+p+"/tenants/{code}/reactivate", httpx.HandlerFunc(c.reactivate))
	mux.Handle("GET "+p+"/plans", httpx.HandlerFunc(c.listPlans))
	mux.Handle("POST "+p+"/plans", httpx.HandlerFunc(c.createPlan))
	mux.Handle("PATCH "+p+"/plans/{code}", httpx.HandlerFunc(c.updatePlan))
	mux.Handle("GET "+p+"/invoices", httpx.HandlerFunc(c.listInvoices))
	mux.Handle("GET "+p+"/tenants/{code}/invoices", httpx.HandlerFunc(c.tenantInvoices))
	mux.Handle("POST "+p+"/tenants/{code}/invoices", httpx.HandlerFunc(c.issueInvoice))
	mux.Handle("GET "+p+"/tenants/{code}/invoices/preview", httpx.HandlerFunc(c.previewInvoice))
	mux.Handle("POST "+p+"/invoices/{number}/pay", httpx.HandlerFunc(c.payInvoice))
	mux.Handle("POST "+p+"/invoices/{number}/void", httpx.HandlerFunc(c.voidInvoice))
}

func (c *Console) listTenants(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	out, err := c.ListTenants(r.Context(), TenantFilter{Q: qs.Get("q"), Status: qs.Get("status"), Plan: qs.Get("plan")})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) exportTenants(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	out, err := c.ListTenants(r.Context(), TenantFilter{Q: qs.Get("q"), Status: qs.Get("status"), Plan: qs.Get("plan")})
	if err != nil {
		return err
	}
	rows := [][]string{{"code", "name", "plan", "state", "paid_through", "days_left", "properties", "rooms", "users", "open_invoices", "overdue_invoices"}}
	for _, t := range out.Items {
		paid, left := "", ""
		if t.PaidThrough != nil {
			paid = t.PaidThrough.String()
		}
		if t.DaysLeft != nil {
			left = strconv.Itoa(*t.DaysLeft)
		}
		rows = append(rows, []string{t.Code, t.Name, t.PlanCode, t.State, paid, left, strconv.Itoa(t.Usage.Properties), strconv.Itoa(t.Usage.Rooms), strconv.Itoa(t.Usage.Users),
			strconv.Itoa(t.OpenInvoiceCount), strconv.Itoa(t.OverdueInvoiceCount)})
	}
	return csvlang.Write(w, r, "tenants.csv", rows)
}

func (c *Console) createTenant(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Admin    *struct {
			Email    string `json:"email"`
			FullName string `json:"full_name"`
		} `json:"admin"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := NewTenantInput{Code: req.Code, Name: req.Name, Timezone: req.Timezone}
	if req.Admin != nil {
		in.Admin = &NewAdminInput{Email: req.Admin.Email, FullName: req.Admin.FullName}
	}
	out, err := c.CreateTenant(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, out)
}

func (c *Console) getTenant(w http.ResponseWriter, r *http.Request) error {
	out, err := c.Tenant(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) tenantAdmins(w http.ResponseWriter, r *http.Request) error {
	out, err := c.TenantAdmins(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (c *Console) tenantActivity(w http.ResponseWriter, r *http.Request) error {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fieldError("limit", "INVALID_VALUE", "1 to 200")
		}
		limit = n
	}
	out, err := c.TenantActivity(r.Context(), r.PathValue("code"), limit)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (c *Console) changeSubscription(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		PlanCode    *string                `json:"plan_code"`
		PaidThrough *civil.Date            `json:"paid_through"`
		NoEnd       bool                   `json:"no_end"`
		GraceDays   *int                   `json:"grace_days"`
		Status      *string                `json:"status"`
		Limits      subscription.Limits    `json:"limits"`
		ClearLimits []entitlement.Resource `json:"clear_limits"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.ChangeSubscription(r.Context(), r.PathValue("code"), subscription.ChangeInput{PlanCode: req.PlanCode, PaidThrough: req.PaidThrough, NoEnd: req.NoEnd,
		GraceDays: req.GraceDays, Status: req.Status, Limits: req.Limits, ClearLimits: req.ClearLimits})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type statusRequest struct {
	Reason      string `json:"reason"`
	ConfirmCode string `json:"confirm_code"`
}

func (c *Console) suspend(w http.ResponseWriter, r *http.Request) error {
	var req statusRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.SetSuspended(r.Context(), r.PathValue("code"), req.Reason, req.ConfirmCode, true)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) reactivate(w http.ResponseWriter, r *http.Request) error {
	var req statusRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.SetSuspended(r.Context(), r.PathValue("code"), req.Reason, "", false)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) listPlans(w http.ResponseWriter, r *http.Request) error {
	out, err := c.Plans(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// planRequest is the editable part of a plan; the code comes from the body on create and from the path on update.
type planRequest struct {
	Code                string                `json:"code"`
	Name                string                `json:"name"`
	Modules             []subscription.Module `json:"modules"`
	Limits              subscription.Limits   `json:"limits"`
	Price               decimal.Decimal       `json:"price"`
	CurrencyCode        string                `json:"currency_code"`
	BillingPeriodMonths int                   `json:"billing_period_months"`
	TrialDays           int                   `json:"trial_days"`
	IsActive            bool                  `json:"is_active"`
	IsDefault           bool                  `json:"is_default"`
}

func (p planRequest) input() subscription.PlanInput {
	return subscription.PlanInput{Code: p.Code, Name: p.Name, Modules: p.Modules, Limits: p.Limits, Price: p.Price, CurrencyCode: p.CurrencyCode,
		BillingPeriodMonths: p.BillingPeriodMonths, TrialDays: p.TrialDays, IsActive: p.IsActive, IsDefault: p.IsDefault}
}

func (c *Console) createPlan(w http.ResponseWriter, r *http.Request) error {
	var req planRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.CreatePlan(r.Context(), req.input())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, out)
}

func (c *Console) updatePlan(w http.ResponseWriter, r *http.Request) error {
	var req planRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Code != "" && req.Code != r.PathValue("code") {
		return fieldError("code", "IMMUTABLE", "the code of a plan cannot change")
	}
	req.Code = r.PathValue("code")
	out, err := c.UpdatePlan(r.Context(), req.input())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) listInvoices(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	out, err := c.ListInvoices(r.Context(), qs.Get("status"), qs.Get("tenant"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) tenantInvoices(w http.ResponseWriter, r *http.Request) error {
	out, err := c.TenantInvoices(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (c *Console) issueInvoice(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		PlanCode string           `json:"plan_code"`
		Periods  int              `json:"periods"`
		Amount   *decimal.Decimal `json:"amount"`
		DueOn    *civil.Date      `json:"due_on"`
		Notes    string           `json:"notes"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.IssueInvoice(r.Context(), subscription.IssueInput{TenantCode: r.PathValue("code"), PlanCode: req.PlanCode, Periods: req.Periods, Amount: req.Amount, DueOn: req.DueOn, Notes: req.Notes})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, out)
}

func (c *Console) previewInvoice(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	periods := 1
	if s := qs.Get("periods"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return apperr.Invalid("the request is invalid", apperr.FieldError{Field: "periods", Code: "INVALID_VALUE", Message: "1 to 36"})
		}
		periods = n
	}
	out, err := c.PreviewInvoice(r.Context(), r.PathValue("code"), qs.Get("plan"), periods)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) payInvoice(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		PaidOn    civil.Date `json:"paid_on"`
		Reference string     `json:"reference"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.PayInvoice(r.Context(), r.PathValue("number"), req.PaidOn, req.Reference)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (c *Console) voidInvoice(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := c.VoidInvoice(r.Context(), r.PathValue("number"), req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
