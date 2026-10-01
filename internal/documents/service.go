package documents

import (
	"context"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/guests"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/reservations"
	"kamarapms/internal/tenancy"
)

// Document is a rendered PDF with the file name it should be saved under.
type Document struct {
	Filename string
	PDF      []byte
}

// Service assembles the documents through the services that own the data, so each document needs exactly the
// permissions of what it shows: folio.read for invoices and receipts, reservation.read for the registration card
// and the confirmation (and for the reservation behind an invoice). Nothing here writes.
type Service struct {
	clock  clock.Clock
	days   *tenancy.Service
	folios *folios.Service
	front  *frontdesk.Service
	res    *reservations.Service
	guests *guests.Service
}

// NewService wires the service.
func NewService(c clock.Clock, days *tenancy.Service, f *folios.Service, fd *frontdesk.Service, r *reservations.Service, g *guests.Service) *Service {
	return &Service{clock: c, days: days, folios: f, front: fd, res: r, guests: g}
}

const registrationTerms = "I confirm that the details above are correct and that I will settle my account in full on departure. " +
	"I accept responsibility for the room and its contents during my stay and understand that the hotel is not liable for valuables left in the room. " +
	"The hotel may charge for loss or damage."

// context is what every document needs: the letterhead, the currency and the printing time.
type docContext struct {
	hotel    Hotel
	prop     tenancy.Property
	decimals int32
	printed  string
}

func (s *Service) context(ctx context.Context, propertyID int64) (docContext, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return docContext{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return docContext{}, err
	}
	return docContext{
		hotel: Hotel{Name: prop.Name, Address: prop.Address, City: prop.City, Country: prop.CountryCode, Phone: prop.Phone, Email: prop.Email, TaxID: prop.TaxID, Footer: prop.DocumentFooter},
		prop:  prop, decimals: prop.CurrencyDecimals,
		printed: "Printed " + fmtTime(s.clock.Now(), prop.Location()) + "  ·  business date " + fmtDate(day.BusinessDate),
	}, nil
}

// party describes a guest for a document. The profile needs guest.read; without it the name from the reservation
// is all there is (the card then leaves room to write the rest).
func (s *Service) party(ctx context.Context, guestID *int64, fallback string) Party {
	p := Party{Name: fallback}
	if guestID == nil {
		return p
	}
	v, err := s.guests.Get(ctx, *guestID)
	if err != nil {
		return p
	}
	p.Name = v.FullName()
	p.Address, p.City, p.Email, p.Phone = v.Address, v.City, v.Email, v.Phone
	p.Nationality, p.IDType, p.IDNumber = v.Nationality, v.IDType, v.IDNumber
	if v.DateOfBirth != nil {
		p.DateOfBirth = fmtDate(*v.DateOfBirth)
	}
	return p
}

func guestName(g *reservations.GuestName) string {
	if g == nil {
		return ""
	}
	return strings.TrimSpace(g.FirstName + " " + g.LastName)
}

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

// money prints an amount with the property's decimals and thousands separators: 1110000 as 1,110,000.
func money(d decimal.Decimal, decimals int32) string {
	s := d.StringFixed(decimals)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if frac != "" {
		b.WriteString("." + frac)
	}
	return sign + b.String()
}

// Invoice renders a folio: the final invoice once the folio is closed, a guest bill while it is open.
func (s *Service) Invoice(ctx context.Context, propertyID, folioID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	f, err := s.folios.GetFolio(ctx, propertyID, folioID)
	if err != nil {
		return Document{}, err
	}
	res, err := s.res.Get(ctx, propertyID, f.ReservationID)
	if err != nil {
		return Document{}, err
	}
	d := InvoiceData{
		Hotel: dc.hotel, Printed: dc.printed, Number: f.FolioNumber, Final: f.Status == "CLOSED", Currency: dc.prop.CurrencyCode,
		Booking: res.ConfirmationNumber, Arrival: res.ArrivalDate, Departure: res.DepartureDate,
		Guest: s.party(ctx, res.GuestID, guestName(res.Guest)),
	}
	if f.StayID != nil {
		if st, err := s.front.GetStay(ctx, propertyID, *f.StayID); err == nil {
			d.Stay, d.Arrival, d.Departure = st.Stay.StayNumber, st.Stay.ArrivalDate, st.Stay.DepartureDate
			var rooms []string
			for _, sg := range st.Segments {
				rooms = append(rooms, sg.RoomNumber)
			}
			d.Room = strings.Join(rooms, " > ")
			if st.Guest != nil && res.GuestID != nil && st.Guest.ID != *res.GuestID {
				d.Guest = s.party(ctx, &st.Guest.ID, strings.TrimSpace(st.Guest.FirstName+" "+st.Guest.LastName))
			}
		}
	}

	type key struct{ typ, code, rate string }
	type agg struct {
		label  string
		amount decimal.Decimal
	}
	var order []key
	parts := map[key]*agg{}
	net, charges, payments := decimal.Zero, decimal.Zero, decimal.Zero
	for _, it := range f.Items {
		debit, credit := dec(it.Debit), dec(it.Credit)
		d.Lines = append(d.Lines, InvoiceLine{Date: it.ServiceDate, Description: it.Description,
			Debit: money(debit, dc.decimals), Credit: money(credit, dc.decimals), DebitSet: !debit.IsZero(), CreditSet: !credit.IsZero()})
		switch it.TransactionType {
		case "PAYMENT", "REFUND":
			payments = payments.Add(credit.Sub(debit))
		default:
			charges = charges.Add(debit.Sub(credit))
			net = net.Add(dec(it.NetAmount))
			for _, c := range it.Components {
				k := key{c.ComponentType, c.Code, c.Rate}
				a := parts[k]
				if a == nil {
					label := c.Name + " " + trimRate(c.Rate) + "%"
					if c.ComponentType == "SERVICE_CHARGE" {
						label = "Service charge " + label
					}
					a = &agg{label: label}
					parts[k] = a
					order = append(order, k)
				}
				a.amount = a.amount.Add(dec(c.Amount))
			}
		}
	}
	d.Summary = append(d.Summary, Amount{"Charges (net)", money(net, dc.decimals)})
	for _, typ := range []string{"SERVICE_CHARGE", "TAX"} {
		for _, k := range order {
			if k.typ == typ {
				d.Summary = append(d.Summary, Amount{parts[k].label, money(parts[k].amount, dc.decimals)})
			}
		}
	}
	balance := dec(f.Balance)
	label := "Balance due"
	if balance.IsNegative() {
		label = "Balance (credit)"
	}
	d.Summary = append(d.Summary, Amount{"Total charges", money(charges, dc.decimals)}, Amount{"Payments received", money(payments, dc.decimals)}, Amount{label, money(balance, dc.decimals)})
	pdf, err := RenderInvoice(d)
	if err != nil {
		return Document{}, err
	}
	prefix := "invoice-"
	if !d.Final {
		prefix = "bill-"
	}
	return Document{Filename: prefix + f.FolioNumber + ".pdf", PDF: pdf}, nil
}

// RegistrationCard renders the card a guest signs at check-in.
func (s *Service) RegistrationCard(ctx context.Context, propertyID, stayID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	st, err := s.front.GetStay(ctx, propertyID, stayID)
	if err != nil {
		return Document{}, err
	}
	res, err := s.res.Get(ctx, propertyID, st.Line.ReservationID)
	if err != nil {
		return Document{}, err
	}
	name := ""
	if st.Guest != nil {
		name = strings.TrimSpace(st.Guest.FirstName + " " + st.Guest.LastName)
	}
	d := CardData{
		Hotel: dc.hotel, Printed: dc.printed, Number: st.Stay.StayNumber, Booking: res.ConfirmationNumber, Guest: s.party(ctx, &st.Stay.GuestID, name),
		RoomType: st.Line.RoomTypeCode, Arrival: st.Stay.ArrivalDate, Departure: st.Stay.DepartureDate, Nights: st.Stay.ArrivalDate.DaysUntil(st.Stay.DepartureDate),
		CheckInTime: dc.prop.CheckInTime.String(), CheckOutTime: dc.prop.CheckOutTime.String(), Adults: st.Stay.AdultCount, Children: st.Stay.ChildCount,
		Currency: dc.prop.CurrencyCode, SpecialNeeds: res.SpecialRequest, Terms: registrationTerms,
	}
	var rooms []string
	for _, sg := range st.Segments {
		rooms = append(rooms, sg.RoomNumber)
	}
	d.Room = strings.Join(rooms, " > ")
	for _, l := range res.Rooms {
		if l.ID == st.Line.ID {
			d.RatePlan = l.RatePlanCode
		}
	}
	if len(st.NightlyRates) > 0 {
		d.NightlyRate = money(dec(st.NightlyRates[0].Amount), dc.decimals)
	}
	for _, g := range st.Guests {
		d.Companions = append(d.Companions, strings.TrimSpace(g.FirstName+" "+g.LastName))
	}
	pdf, err := RenderRegistrationCard(d)
	if err != nil {
		return Document{}, err
	}
	return Document{Filename: "registration-" + st.Stay.StayNumber + ".pdf", PDF: pdf}, nil
}

// Receipt renders a payment or refund receipt (a voided payment is stamped VOID).
func (s *Service) Receipt(ctx context.Context, propertyID, paymentID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	pay, err := s.folios.GetPayment(ctx, propertyID, paymentID)
	if err != nil {
		return Document{}, err
	}
	f, err := s.folios.GetFolio(ctx, propertyID, pay.FolioID)
	if err != nil {
		return Document{}, err
	}
	res, err := s.res.Get(ctx, propertyID, f.ReservationID)
	if err != nil {
		return Document{}, err
	}
	d := ReceiptData{
		Hotel: dc.hotel, Printed: dc.printed, Number: pay.PaymentNumber, Refund: pay.PaymentType == "REFUND", Voided: pay.Status == "VOIDED",
		Guest: s.party(ctx, res.GuestID, guestName(res.Guest)), Folio: f.FolioNumber, Booking: res.ConfirmationNumber, Date: pay.BusinessDate,
		At: pay.PaidAt.In(dc.prop.Location()).Format("15:04"), Method: methodLabel(pay.PaymentMethod), Reference: pay.ReferenceNumber,
		Currency: dc.prop.CurrencyCode, Amount: money(dec(pay.Amount), dc.decimals), Remarks: pay.Remarks,
	}
	if pay.VoidedAt != nil {
		d.VoidNote = "Cancelled on " + fmtTime(*pay.VoidedAt, dc.prop.Location())
		if pay.VoidReason != "" {
			d.VoidNote += ": " + pay.VoidReason
		}
	}
	if pay.RefundOfPaymentID != nil {
		if orig, err := s.folios.GetPayment(ctx, propertyID, *pay.RefundOfPaymentID); err == nil {
			d.RefundOf = orig.PaymentNumber
		}
	}
	pdf, err := RenderReceipt(d)
	if err != nil {
		return Document{}, err
	}
	return Document{Filename: "receipt-" + pay.PaymentNumber + ".pdf", PDF: pdf}, nil
}

func methodLabel(m string) string {
	switch m {
	case "BANK_TRANSFER":
		return "Bank transfer"
	case "CARD":
		return "Card"
	case "CASH":
		return "Cash"
	}
	return "Other"
}

// Confirmation renders the reservation confirmation.
func (s *Service) Confirmation(ctx context.Context, propertyID, reservationID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	res, err := s.res.Get(ctx, propertyID, reservationID)
	if err != nil {
		return Document{}, err
	}
	d := ConfirmationData{
		Hotel: dc.hotel, Printed: dc.printed, Number: res.ConfirmationNumber, Status: statusLabel(res.DisplayStatus), Guest: s.party(ctx, res.GuestID, guestName(res.Guest)),
		Booked: res.ReservationDate, Currency: dc.prop.CurrencyCode, CheckInTime: dc.prop.CheckInTime.String(), CheckOutTime: dc.prop.CheckOutTime.String(), Request: res.SpecialRequest,
	}
	total := decimal.Zero
	for _, l := range res.Rooms {
		if l.Status == reservations.LineCancelled {
			continue
		}
		total = total.Add(l.Estimate.Total)
		d.Rooms = append(d.Rooms, BookedRoom{RoomType: l.RoomTypeCode, RatePlan: l.RatePlanCode, Arrival: l.ArrivalDate, Departure: l.DepartureDate, Nights: l.Nights,
			Guests: strconv.Itoa(l.AdultCount) + " adult(s)" + childText(l.ChildCount), Estimate: money(l.Estimate.Total, dc.decimals)})
	}
	d.Total = money(total, dc.decimals)
	pdf, err := RenderConfirmation(d)
	if err != nil {
		return Document{}, err
	}
	return Document{Filename: "confirmation-" + res.ConfirmationNumber + ".pdf", PDF: pdf}, nil
}

func childText(n int) string {
	if n == 0 {
		return ""
	}
	return ", " + strconv.Itoa(n) + " child(ren)"
}

func statusLabel(s string) string {
	return strings.ToUpper(s[:1]) + strings.ToLower(strings.ReplaceAll(s[1:], "_", " "))
}

// trimRate prints 11.0000 as 11 and 7.5000 as 7.5.
func trimRate(r string) string {
	if strings.Contains(r, ".") {
		r = strings.TrimRight(strings.TrimRight(r, "0"), ".")
	}
	return r
}
