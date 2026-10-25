package documents

import (
	"context"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
	"kamarapms/internal/platform/apperr"
)

// The documents of a banquet event (36 4.6, B11, B12): the BEO of a session, the proposal and the contract. They are drawn from a snapshot of the event, so the BEO of an old revision and the contract of
// a revision print as they were; the proposal that was sent is the one that was recorded.

// BanquetDoc is what the renderers of the banquet need: the letterhead, the language, the number and the snapshot.
type BanquetDoc struct {
	Hotel    Hotel
	Lang     Lang
	Printed  string
	Decimals int32
	// Number is the number of the contract; the proposal and the BEO carry the number of the event.
	Number string
	// Revision is the revision of the BEO or of the contract; 0 is a draft, a BEO that has none yet.
	Revision int
	Snap     banquet.Snapshot
	// SessionID limits a BEO to one session; 0 prints every active session.
	SessionID int64
}

func (d BanquetDoc) money(v decimal.Decimal) string { return d.Lang.Money(v, d.Decimals) }

var setupLabel = map[string]string{"THEATER": "Theater", "CLASSROOM": "Classroom", "U_SHAPE": "U-shape", "ROUND_TABLE": "Round table", "COCKTAIL": "Cocktail", "BOARDROOM": "Boardroom"}

var sectionLabel = map[string]string{
	banquet.SectionGeneral: "General", banquet.SectionKitchen: "Kitchen", banquet.SectionFBService: "F&B service", banquet.SectionHousekeeping: "Housekeeping",
	banquet.SectionEngineering: "Engineering", banquet.SectionSecurity: "Security", banquet.SectionFrontOffice: "Front office",
}

func statusText(st string) string {
	switch st {
	case "DEFINITE":
		return "Definite"
	case "COMPLETED":
		return "Completed"
	case "TENTATIVE":
		return "Tentative"
	}
	return st
}

func timeSpan(sn banquet.SnapSession) string {
	if sn.Start == nil || sn.End == nil {
		return "-"
	}
	out := sn.Start.String() + " - " + sn.End.String()
	if sn.EndsNextDay {
		out += " (+1)"
	}
	return out
}

func guestsText(l Lang, sn banquet.SnapSession) string {
	if sn.PaxGuaranteed != nil {
		return l.T(strconv.Itoa(*sn.PaxGuaranteed) + " guaranteed")
	}
	return l.T(strconv.Itoa(sn.PaxEstimated) + " estimated")
}

func (d BanquetDoc) setup(sn banquet.SnapSession) string {
	if sn.Setup == "" {
		return "-"
	}
	if w, ok := setupLabel[sn.Setup]; ok {
		return d.Lang.T(w)
	}
	return sn.Setup
}

func (d BanquetDoc) room(sn banquet.SnapSession) string {
	if sn.Room == "" {
		return "-"
	}
	return sn.Room
}

func activeSessions(s banquet.Snapshot, only int64) []banquet.SnapSession {
	var out []banquet.SnapSession
	for _, sn := range s.Sessions {
		if sn.Status == "ACTIVE" && (only == 0 || sn.ID == only) {
			out = append(out, sn)
		}
	}
	return out
}

func sessionTag(s banquet.Snapshot, id int64) string {
	for _, sn := range s.Sessions {
		if sn.ID == id {
			return strconv.Itoa(sn.No) + " · " + sn.Name
		}
	}
	return ""
}

func modeText(l Lang, mode string) string {
	if mode == "INCLUSIVE" {
		return l.T("nett")
	}
	return "++"
}

// itemText is the description of a line with what the package holds and the discount.
func (d BanquetDoc) itemText(l banquet.Line) string {
	out := l.Description
	if len(l.Components) > 0 {
		names := make([]string, len(l.Components))
		for i, c := range l.Components {
			names[i] = c.Name
		}
		out += "\n" + d.Lang.T("Includes: ") + strings.Join(names, ", ")
	}
	switch {
	case l.DiscountPct != nil:
		out += "\n" + d.Lang.T("Discount ") + l.DiscountPct.String() + "%"
	case l.DiscountAmount != nil:
		out += "\n" + d.Lang.T("Discount ") + d.money(*l.DiscountAmount)
	}
	return out
}

func (d BanquetDoc) priceRows() [][]string {
	by := map[int64]banquet.EstimateLine{}
	for _, e := range d.Snap.Estimate.Lines {
		by[e.LineID] = e
	}
	rows := make([][]string, 0, len(d.Snap.Lines))
	for _, l := range d.Snap.Lines {
		e := by[l.ID]
		qty := l.Quantity.String()
		if l.PackageID != nil {
			qty = strconv.Itoa(e.Pax) + " pax"
		}
		rows = append(rows, []string{sessionTag(d.Snap, l.SessionID), d.itemText(l), qty, d.money(l.UnitPrice) + " " + modeText(d.Lang, l.PriceMode), d.money(e.Total)})
	}
	return rows
}

var priceCols = []col{{34, "Session", "L"}, {64, "Item", "L"}, {20, "Quantity", "R"}, {34, "Price", "R"}, {28, "Amount", "R"}}

func (d BanquetDoc) totals(g *page) {
	e := d.Snap.Estimate
	g.totals([][2]string{
		{"Net (before service and tax)", d.money(e.Net)}, {"Service charge", d.money(e.Service)}, {"Tax", d.money(e.Tax)}, {"Total", d.money(e.Total)},
	})
	g.p.Ln(1)
	g.color(muted)
	g.note("A price marked nett includes the service charge and the tax; a price marked ++ is before them. The amounts are worked out at the guaranteed number of guests (the estimated number while none is guaranteed); the bill follows the guests served, never fewer than the guaranteed number.", "I", 8)
	g.color(ink)
}

func (d BanquetDoc) schedule(g *page) {
	if len(d.Snap.Deposits) == 0 {
		return
	}
	g.section("Deposit schedule")
	rows := make([][]string, len(d.Snap.Deposits))
	for i, r := range d.Snap.Deposits {
		pct := "-"
		if r.Percent != nil {
			pct = r.Percent.String() + "%"
		}
		rows[i] = []string{r.Label, d.Lang.Date(r.DueDate), pct, d.money(r.Amount)}
	}
	g.table([]col{{80, "Payment", "L"}, {40, "Due date", "L"}, {25, "Percent", "R"}, {35, "Amount", "R"}}, rows)
}

func tierText(l Lang, t banquet.Tier) string {
	if t.DaysTo == nil {
		return l.T(strconv.Itoa(t.DaysFrom) + " days or more before the first session")
	}
	return l.T(strconv.Itoa(t.DaysFrom) + " to " + strconv.Itoa(*t.DaysTo) + " days before the first session")
}

func (d BanquetDoc) policy(g *page) {
	p := d.Snap.Policy
	if len(p.Tiers) == 0 {
		return
	}
	g.section("Cancellation policy")
	base := "The percentages apply to the estimated net revenue of the event (before the service charge and the tax)."
	if p.Base == "GROSS" {
		base = "The percentages apply to the contract total (with the service charge and the tax)."
	}
	g.note(base, "", 9)
	rows := make([][]string, len(p.Tiers))
	for i, t := range p.Tiers {
		what := d.Lang.T(t.Percent.String() + "% charged as a fee")
		if t.Action == "FORFEIT" {
			what = d.Lang.T(t.Percent.String() + "% of the deposit kept")
		}
		rows[i] = []string{tierText(d.Lang, t), what}
	}
	g.table([]col{{100, "Days before the event", "L"}, {80, "Charge", "L"}}, rows)
}

func (d BanquetDoc) parties(g *page) {
	e := d.Snap.Event
	contact := strings.Join(nonEmpty(e.ContactName, e.ContactPhone, e.ContactEmail), "  |  ")
	g.pairs([][2]string{{"Event", e.Name}, {"Event number", e.Number}, {"Client", e.Booker}, {"Contact", contact}, {"Event type", e.EventType}, {"Prepared by", e.SalesName}})
}

func (d BanquetDoc) programme(g *page) {
	g.section("Programme")
	sessions := activeSessions(d.Snap, 0)
	rows := make([][]string, len(sessions))
	for i, sn := range sessions {
		rows[i] = []string{strconv.Itoa(sn.No) + " · " + sn.Name, d.Lang.Date(sn.Date), timeSpan(sn), d.room(sn) + " / " + d.setup(sn), guestsText(d.Lang, sn)}
	}
	g.table([]col{{44, "Session", "L"}, {26, "Date", "L"}, {32, "Time", "L"}, {48, "Room / set-up", "L"}, {30, "Guests", "L"}}, rows)
}

// RenderBanquetProposal draws the proposal sent to the client: the sessions, the items with their prices (nett or ++), the estimate, the deposits and the cancellation policy.
func RenderBanquetProposal(d BanquetDoc) ([]byte, error) {
	g := newPage(d.Hotel, d.Lang, "Banquet proposal", d.Printed)
	g.title("BANQUET PROPOSAL", d.Snap.Event.Number)
	d.parties(g)
	if d.Snap.Event.OptionUntil != "" {
		g.pairs([][2]string{{"Valid until", d.Snap.Event.OptionUntil}})
	}
	d.programme(g)
	g.section("Items and prices")
	g.table(priceCols, d.priceRows())
	d.totals(g)
	d.schedule(g)
	d.policy(g)
	g.p.Ln(3)
	g.color(muted)
	g.note("This proposal is an offer, not a booking: the date is held for the client only by the option date of the hotel or by a signed contract.", "I", 8.5)
	return g.bytes()
}

var contractTerms = []string{
	"The hotel provides the rooms, the items and the services listed for the sessions above, at the prices and on the dates shown.",
	"The client pays the deposits on the dates of the schedule. The hotel may release the rooms if a deposit is not paid on its due date.",
	"The client confirms the number of guests (the guaranteed number) before the first session. The bill follows the guests served, never fewer than the guaranteed number.",
	"A change to the dates, the hours, the rooms, the number of guests or the items is a new revision of the event; the hotel issues a new contract for it.",
	"A cancellation is charged by the cancellation policy above, by the number of days before the first session.",
}

// RenderBanquetContract draws the contract of a revision of an event: its number and revision, the terms of that revision and the signatures.
func RenderBanquetContract(d BanquetDoc) ([]byte, error) {
	g := newPage(d.Hotel, d.Lang, "Banquet contract", d.Printed)
	g.title("BANQUET CONTRACT", d.Number)
	d.parties(g)
	g.pairs([][2]string{{"Revision", strconv.Itoa(d.Revision)}, {"Status of the event", statusText(d.Snap.Event.Status)}})
	d.programme(g)
	g.section("Items and prices")
	g.table(priceCols, d.priceRows())
	d.totals(g)
	d.schedule(g)
	d.policy(g)
	g.section("Terms")
	for i, t := range contractTerms {
		g.note(strconv.Itoa(i+1)+". "+d.Lang.T(t), "", 9)
	}
	g.color(muted)
	g.p.Ln(1)
	g.note(d.Lang.T("This contract is final for revision ")+strconv.Itoa(d.Revision)+d.Lang.T(" of the event ")+d.Snap.Event.Number+d.Lang.T(" and replaces any earlier contract of the event."), "I", 8.5)
	g.color(ink)
	if g.p.GetY() > footerAt-50 {
		g.p.AddPage()
	}
	y := g.p.GetY() + 22
	g.signature(margin, y, 75, "For the client: "+d.Snap.Event.Booker)
	g.signature(margin+100, y, 75, "For the hotel: "+d.Hotel.Name)
	g.p.SetXY(margin, y+8)
	g.color(muted)
	g.note("Name, date and signature", "", 8)
	return g.bytes()
}

// RenderBeo draws the BEO: a page for each session with its facts, the programme, the items and the text of each section.
func RenderBeo(d BanquetDoc) ([]byte, error) {
	g := newPage(d.Hotel, d.Lang, "Banquet event order", d.Printed)
	state := d.Lang.T("Draft")
	if d.Revision > 0 {
		state = d.Lang.T("Revision ") + strconv.Itoa(d.Revision)
	}
	for i, sn := range activeSessions(d.Snap, d.SessionID) {
		if i > 0 {
			g.p.AddPage()
			g.letterhead()
		}
		g.title("BANQUET EVENT ORDER", d.Snap.Event.Number+" · "+state)
		e := d.Snap.Event
		contact := strings.Join(nonEmpty(e.ContactName, e.ContactPhone), "  |  ")
		g.pairs([][2]string{
			{"Event", e.Name}, {"Client", e.Booker},
			{"Session", strconv.Itoa(sn.No) + " · " + sn.Name}, {"Date", d.Lang.Date(sn.Date)},
			{"Time", timeSpan(sn)}, {"Room", d.room(sn)},
			{"Set-up", d.setup(sn)}, {"Guests", guestsText(d.Lang, sn)},
			{"Contact", contact}, {"Sales", e.SalesName},
		})
		var rows [][]string
		for _, l := range d.Snap.Lines {
			if l.SessionID == sn.ID {
				qty := l.Quantity.String()
				if l.PackageID != nil {
					qty = guestsText(d.Lang, sn)
				}
				rows = append(rows, []string{d.itemText(l), qty})
			}
		}
		if len(rows) > 0 {
			g.section("Items")
			g.table([]col{{130, "Item", "L"}, {50, "Quantity", "R"}}, rows)
		}
		if len(sn.RunOfShow) > 0 {
			g.section("Run of the day")
			prog := make([][]string, len(sn.RunOfShow))
			for j, r := range sn.RunOfShow {
				prog[j] = []string{r.At.String(), r.Text}
			}
			g.table([]col{{25, "Time", "L"}, {155, "Programme", "L"}}, prog)
		}
		for _, sec := range banquet.Sections {
			text := strings.TrimSpace(sn.Sections[sec])
			if text == "" && sec == banquet.SectionGeneral {
				continue
			}
			g.section(sectionLabel[sec])
			g.font("", 9.5)
			g.paragraph(blank(text), 4.6)
		}
	}
	return g.bytes()
}

// ---------------------------------------------------------------- the service

// SetBanquet gives the documents the banquet service they are drawn from (the banquet does not know the documents, so the two are tied by the application).
func (s *Service) SetBanquet(b *banquet.Service) { s.banquet = b }

func (s *Service) banquetDoc(ctx context.Context, propertyID int64, snap banquet.Snapshot) (BanquetDoc, error) {
	if s.banquet == nil {
		return BanquetDoc{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return BanquetDoc{}, err
	}
	return BanquetDoc{Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Decimals: dc.decimals, Snap: snap}, nil
}

// BanquetProposal renders the proposal of an event as it is now (banquet.read).
func (s *Service) BanquetProposal(ctx context.Context, propertyID, eventID int64) (Document, error) {
	if s.banquet == nil {
		return Document{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	snap, err := s.banquet.LiveSnapshot(ctx, propertyID, eventID)
	if err != nil {
		return Document{}, err
	}
	d, err := s.banquetDoc(ctx, propertyID, snap)
	if err != nil {
		return Document{}, err
	}
	b, err := RenderBanquetProposal(d)
	return Document{Filename: "proposal-" + snap.Event.Number + ".pdf", PDF: b}, err
}

// BanquetBeo renders the BEO of an event: of one session or of all of them, as it is now, or as it was at a revision.
func (s *Service) BanquetBeo(ctx context.Context, propertyID, eventID int64, sessionID int64, revision *int) (Document, error) {
	if s.banquet == nil {
		return Document{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	snap, rev, err := s.banquet.BeoSnapshot(ctx, propertyID, eventID, revision)
	if err != nil {
		return Document{}, err
	}
	if sessionID != 0 && len(activeSessions(snap, sessionID)) == 0 {
		return Document{}, apperr.NotFound("SESSION_NOT_FOUND", "the session does not exist in this event")
	}
	d, err := s.banquetDoc(ctx, propertyID, snap)
	if err != nil {
		return Document{}, err
	}
	d.Revision, d.SessionID = rev, sessionID
	b, err := RenderBeo(d)
	name := "beo-" + snap.Event.Number
	if rev > 0 {
		name += "-rev" + strconv.Itoa(rev)
	}
	return Document{Filename: name + ".pdf", PDF: b}, err
}

// BanquetDocument renders a document that was recorded: the contract of a revision, or a proposal that was sent.
func (s *Service) BanquetDocument(ctx context.Context, propertyID, docID int64) (Document, error) {
	if s.banquet == nil {
		return Document{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	f, err := s.banquet.DocumentFileOf(ctx, propertyID, docID)
	if err != nil {
		return Document{}, err
	}
	return s.renderRecorded(ctx, propertyID, f)
}

// BanquetContract renders the contract of the revision in force of an event.
func (s *Service) BanquetContract(ctx context.Context, propertyID, eventID int64) (Document, error) {
	if s.banquet == nil {
		return Document{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	row, err := s.banquet.LatestContract(ctx, propertyID, eventID)
	if err != nil {
		return Document{}, err
	}
	return s.BanquetDocument(ctx, propertyID, row.ID)
}

func (s *Service) renderRecorded(ctx context.Context, propertyID int64, f banquet.DocumentFile) (Document, error) {
	d, err := s.banquetDoc(ctx, propertyID, f.Snapshot)
	if err != nil {
		return Document{}, err
	}
	switch f.Kind {
	case banquet.DocContract:
		if f.Number != nil {
			d.Number = *f.Number
		}
		if f.RevisionNo != nil {
			d.Revision = *f.RevisionNo
		}
		b, err := RenderBanquetContract(d)
		return Document{Filename: "contract-" + d.Number + ".pdf", PDF: b}, err
	case banquet.DocProposal:
		b, err := RenderBanquetProposal(d)
		return Document{Filename: "proposal-" + f.Snapshot.Event.Number + ".pdf", PDF: b}, err
	}
	return Document{}, apperr.NotFound("DOCUMENT_NOT_FOUND", "the document does not exist")
}

// BanquetRecorded is the entry of the e-mail worker: the PDF of a recorded document in the language the e-mail asked for.
func (s *Service) BanquetRecorded(ctx context.Context, propertyID, docID int64, lang Lang) (Document, error) {
	return s.BanquetDocument(WithLang(ctx, lang), propertyID, docID)
}
