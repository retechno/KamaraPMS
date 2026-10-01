package billingconfig

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig/billingconfigdb"
	"kamarapms/internal/platform/apperr"
)

func errTaxNotFound() *apperr.Error {
	return apperr.NotFound("TAX_NOT_FOUND", "the tax does not exist in this property")
}

func errServiceChargeNotFound() *apperr.Error {
	return apperr.NotFound("SERVICE_CHARGE_NOT_FOUND", "the service charge does not exist in this property")
}

func errChargeCodeNotFound() *apperr.Error {
	return apperr.NotFound("CHARGE_CODE_NOT_FOUND", "the charge code does not exist in this property")
}

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func toTax(t billingconfigdb.Tax) Tax {
	return Tax{ID: t.ID, Code: t.Code, Name: t.Name, Rate: FormatRate(t.Rate), TaxOnService: t.TaxOnService, GLAccountCode: t.GlAccountCode, IsActive: t.IsActive,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

func toServiceCharge(s billingconfigdb.ServiceCharge) ServiceCharge {
	return ServiceCharge{ID: s.ID, Code: s.Code, Name: s.Name, Rate: FormatRate(s.Rate), GLAccountCode: s.GlAccountCode, IsActive: s.IsActive,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

// toChargeCode maps a row; amounts are formatted with the property's currency decimals.
func toChargeCode(c billingconfigdb.ChargeCode, decimals int32) ChargeCode {
	out := ChargeCode{
		ID: c.ID, Code: c.Code, Name: c.Name, ChargeType: c.ChargeType, PriceMode: c.PriceMode, GLAccountCode: c.GlAccountCode, IsSystem: c.IsSystem, IsActive: c.IsActive,
		Taxes: []TaxRule{}, ServiceCharges: []ServiceRule{}, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.DefaultUnitPrice != nil {
		s := c.DefaultUnitPrice.StringFixed(decimals)
		out.DefaultUnitPrice = &s
	}
	return out
}

func unitPrice(s string) *decimal.Decimal {
	if s == "" {
		return nil
	}
	d, _ := decimal.NewFromString(s) // validated by ParseUnitPrice
	return &d
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}

// glOrNil maps an input account code ("" = none) to the nullable column.
func glOrNil(code string) *string {
	if code == "" {
		return nil
	}
	return &code
}

// glString is the editable form of a stored account code.
func glString(code *string) string {
	if code == nil {
		return ""
	}
	return *code
}
