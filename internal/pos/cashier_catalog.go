package pos

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

// CatalogView is everything the order screen needs of an outlet, in one answer: the categories, the products it sells with their price and options, and what is sold out today.
type CatalogView struct {
	OutletID   int64      `json:"outlet_id"`
	Date       civil.Date `json:"business_date"`
	Categories []Category `json:"categories"`
	Products   []Product  `json:"products"`
	SoldOut    []int64    `json:"sold_out"`
}

// Catalog answers the catalogue of an outlet for the order screen: only what the outlet sells (its price list, active, sold), with the option groups. The screen never computes a price from it:
// the price of a line is the server's.
func (s *Service) Catalog(ctx context.Context, propertyID, outletID int64) (CatalogView, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return CatalogView{}, err
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, outletID)
	if err != nil {
		return CatalogView{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return CatalogView{}, err
	}
	active := true
	list, err := s.products(ctx, p.TenantID, propertyID, nil, nil, &active)
	if err != nil {
		return CatalogView{}, err
	}
	items, err := s.ListPriceItems(ctx, propertyID, o.PriceListID)
	if err != nil {
		return CatalogView{}, err
	}
	price := map[int64]PriceItem{}
	for _, it := range items {
		price[it.ProductID] = it
	}
	var sold []Product
	for _, pr := range list {
		it, ok := price[pr.ID]
		if !ok || !it.IsSold {
			continue
		}
		v := it.Price
		t := true
		pr.Price, pr.IsSold = &v, &t
		sold = append(sold, pr)
	}
	if sold, err = s.withDetails(ctx, p.TenantID, propertyID, sold, nil); err != nil {
		return CatalogView{}, err
	}
	for i := range sold {
		sold[i].Prices = nil // the price list of the outlet is the only one this screen knows
	}
	cats, err := s.ListCategories(ctx, propertyID)
	if err != nil {
		return CatalogView{}, err
	}
	so, err := s.ListSoldOut(ctx, propertyID, outletID)
	if err != nil {
		return CatalogView{}, err
	}
	if so == nil {
		so = []int64{}
	}
	if sold == nil {
		sold = []Product{}
	}
	return CatalogView{OutletID: outletID, Date: day.BusinessDate, Categories: cats, Products: sold, SoldOut: so}, nil
}

// QuoteInput asks what a line would come to.
type QuoteInput struct {
	OutletID  int64             `json:"outlet_id"`
	ProductID int64             `json:"product_id"`
	Quantity  decimal.Decimal   `json:"quantity"`
	Options   []LineOptionInput `json:"options"`
	Discount  *decimal.Decimal  `json:"discount_amount"`
}

// QuoteResult is the answer of the one charge engine for a line: its price with the options, and what it comes to with service and tax.
type QuoteResult struct {
	UnitPrice decimal.Decimal `json:"unit_price"`
	Base      decimal.Decimal `json:"base_amount"`
	Discount  decimal.Decimal `json:"discount_amount"`
	Net       decimal.Decimal `json:"net_amount"`
	Service   decimal.Decimal `json:"service_amount"`
	Tax       decimal.Decimal `json:"tax_amount"`
	Total     decimal.Decimal `json:"total"`
}

// PriceQuote prices a line without writing anything: the same engine that posts it, never a figure computed in the client.
func (s *Service) PriceQuote(ctx context.Context, propertyID int64, in QuoteInput) (QuoteResult, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return QuoteResult{}, err
	}
	if fields := validateQty(in.Quantity, ""); len(fields) > 0 {
		return QuoteResult{}, invalid("quote", fields...)
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, in.OutletID)
	if err != nil {
		return QuoteResult{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return QuoteResult{}, err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return QuoteResult{}, err
	}
	_, unit, code, prof, _, _, err := s.priceLine(ctx, p, propertyID, o, day.BusinessDate, in.ProductID, in.Options, decimals)
	if err != nil {
		return QuoteResult{}, err
	}
	disc := decimal.Zero
	if in.Discount != nil {
		if in.Discount.IsNegative() || in.Discount.Exponent() < -decimals {
			return QuoteResult{}, apperr.Invalid("the quote is invalid", fieldErr("discount_amount", "INVALID_AMOUNT", "not negative, at most the decimals of the currency"))
		}
		disc = money.Round(*in.Discount, decimals)
	}
	q, err := s.quote(ctx, propertyID, code, in.Quantity, unit, disc, prof)
	if err != nil {
		return QuoteResult{}, err
	}
	return QuoteResult{UnitPrice: unit, Base: q.Base, Discount: q.Discount, Net: q.Net, Service: q.Service, Tax: q.Tax, Total: q.Total}, nil
}
