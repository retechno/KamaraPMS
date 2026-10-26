package banquet

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the reports of 36-banquet.md 4.10: each answers JSON, and a CSV file with ?format=csv (the names of the columns follow ?lang=id; the cells stay plain numbers and ISO dates).
func (h *Handler) registerReports(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet/reports"
	mux.Handle("GET "+p+"/forecast", httpx.HandlerFunc(h.reportForecast))
	mux.Handle("GET "+p+"/rooms", httpx.HandlerFunc(h.reportRooms))
	mux.Handle("GET "+p+"/guests", httpx.HandlerFunc(h.reportGuests))
	mux.Handle("GET "+p+"/sales", httpx.HandlerFunc(h.reportSales))
	mux.Handle("GET "+p+"/lost", httpx.HandlerFunc(h.reportLost))
	mux.Handle("GET "+p+"/schedule", httpx.HandlerFunc(h.reportSchedule))
}

func wantsCSV(r *http.Request) bool { return r.URL.Query().Get("format") == "csv" }

func dec(d decimal.Decimal) string { return d.String() }

func decp(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func intp(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func strp(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timep(t *civil.TimeOfDay) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// period reads from and to; a missing one takes the default of the report.
func period(r *http.Request) (civil.Date, civil.Date, error) {
	var from, to civil.Date
	f, err := queryDate(r, "from")
	if err != nil {
		return from, to, err
	}
	t, err := queryDate(r, "to")
	if err != nil {
		return from, to, err
	}
	if f != nil {
		from = *f
	}
	if t != nil {
		to = *t
	}
	return from, to, nil
}

func (h *Handler) reportForecast(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, err := period(r)
	if err != nil {
		return err
	}
	withTax, err := queryBool(r, "with_tax")
	if err != nil {
		return err
	}
	v, err := h.svc.Forecast(r.Context(), pid, from, to, withTax != nil && *withTax)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"group", "key", "name", "definite", "definite_events", "tentative", "tentative_events"}}
	for _, g := range []struct {
		name string
		list []ForecastGroup
	}{{"month", v.Months}, {"event_type", v.ByType}, {"sales", v.BySales}} {
		for _, x := range g.list {
			rows = append(rows, []string{g.name, x.Key, x.Name, dec(x.Definite), strconv.Itoa(x.DefiniteEvents), dec(x.Tentative), strconv.Itoa(x.TentativeEvents)})
		}
	}
	return csvlang.Write(w, r, "banquet-forecast-"+v.From.String()+"-"+v.To.String()+".csv", rows)
}

func (h *Handler) reportRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, err := period(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Rooms(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"room_code", "room", "sessions", "hours", "locked_hours", "available_hours", "occupancy_pct", "posted_sessions", "revenue", "revenue_per_hour"}}
	for _, x := range v.Rooms {
		rows = append(rows, []string{x.Code, x.Name, strconv.Itoa(x.Sessions), dec(x.Hours), dec(x.LockedHours), dec(x.AvailableHours), decp(x.OccupancyPct), strconv.Itoa(x.PostedSessions), dec(x.Revenue), decp(x.RevenuePerHour)})
	}
	return csvlang.Write(w, r, "banquet-rooms-"+v.From.String()+"-"+v.To.String()+".csv", rows)
}

func (h *Handler) reportGuests(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, err := period(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Guests(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"date", "event_number", "event", "session", "room", "estimated", "guaranteed", "actual", "difference"}}
	for _, x := range v.Rows {
		rows = append(rows, []string{x.SessionDate.String(), x.EventNumber, x.EventName, x.SessionName, strp(x.RoomName), strconv.Itoa(x.PaxEstimated), intp(x.PaxGuaranteed), intp(x.PaxActual), intp(x.Difference)})
	}
	return csvlang.Write(w, r, "banquet-guests-"+v.From.String()+"-"+v.To.String()+".csv", rows)
}

func (h *Handler) reportSales(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, err := period(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Sales(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"sales", "created", "definite", "lost", "definite_value", "lost_value", "conversion_pct"}}
	for _, x := range append(append([]SalesRow{}, v.Rows...), SalesRow{SalesName: "Total", Created: v.Total.Created, Definite: v.Total.Definite, Lost: v.Total.Lost,
		DefiniteValue: v.Total.DefiniteValue, LostValue: v.Total.LostValue, Conversion: v.Total.Conversion}) {
		rows = append(rows, []string{x.SalesName, strconv.Itoa(x.Created), strconv.Itoa(x.Definite), strconv.Itoa(x.Lost), dec(x.DefiniteValue), dec(x.LostValue), decp(x.Conversion)})
	}
	return csvlang.Write(w, r, "banquet-sales-"+v.From.String()+"-"+v.To.String()+".csv", rows)
}

func (h *Handler) reportLost(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, err := period(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Lost(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"closed_on", "event_number", "event", "status", "sales", "reason_code", "reason", "note", "value"}}
	for _, x := range v.Rows {
		rows = append(rows, []string{x.ClosedOn.String(), x.EventNumber, x.EventName, x.Status, x.SalesName, x.ReasonCode, x.ReasonName, strp(x.ReasonNote), dec(x.Value)})
	}
	return csvlang.Write(w, r, "banquet-lost-"+v.From.String()+"-"+v.To.String()+".csv", rows)
}

func (h *Handler) reportSchedule(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	day, err := queryDate(r, "day")
	if err != nil {
		return err
	}
	var d civil.Date
	if day != nil {
		d = *day
	}
	v, err := h.svc.Schedule(r.Context(), pid, d)
	if err != nil {
		return err
	}
	if !wantsCSV(r) {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	rows := [][]string{{"start", "end", "room", "event_number", "event", "session", "status", "setup", "estimated", "guaranteed", "actual", "contact", "phone", "sales", "beo_revision", "beo_unread"}}
	for _, x := range v.Rows {
		end := timep(x.EndTime)
		if x.EndsNextDay && end != "" {
			end += " +1"
		}
		rows = append(rows, []string{timep(x.StartTime), end, strp(x.RoomName), x.EventNumber, x.EventName, x.SessionName, x.Status, strp(x.SetupStyle), strconv.Itoa(x.PaxEstimated),
			intp(x.PaxGuaranteed), intp(x.PaxActual), strp(x.ContactName), strp(x.ContactPhone), x.SalesName, strconv.Itoa(x.BeoRevision), strconv.Itoa(x.BeoUnread)})
	}
	return csvlang.Write(w, r, "banquet-schedule-"+v.Day.String()+".csv", rows)
}

// unpostedCSV answers the sessions that are due and not posted as a download.
func unpostedCSV(w http.ResponseWriter, r *http.Request, list []Unposted) error {
	rows := [][]string{{"date", "event_number", "event", "session", "room", "estimated", "guaranteed", "actual", "posting_mode", "sales", "days_late", "blocked", "inactive_code"}}
	for _, x := range list {
		rows = append(rows, []string{x.SessionDate.String(), x.EventNumber, x.EventName, x.SessionName, strp(x.RoomName), strconv.Itoa(x.PaxEstimated), intp(x.PaxGuaranteed), intp(x.PaxActual),
			x.PostingMode, x.SalesName, strconv.Itoa(x.DaysLate), strings.ToUpper(strconv.FormatBool(x.Blocked)), strp(x.InactiveCode)})
	}
	return csvlang.Write(w, r, "banquet-unposted.csv", rows)
}
