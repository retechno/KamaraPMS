package pos

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
)

const maxImportRows = 2000

// importColumns are the columns of the CSV of products: a header row names them (any order, comma or semicolon). code, name, category and charge_code are required; price_list and
// price go together.
var importColumns = map[string]bool{"code": true, "name": true, "short_name": true, "category": true, "charge_code": true, "station": true, "profile": true, "price_list": true, "price": true, "is_sold": true}

type importRow struct {
	n                                                                                   int
	code, name, shortName, category, chargeCode, station, profile, priceList, priceText string
	soldText                                                                            string
}

// ImportProducts reads a CSV of products: all or nothing, with the rows that are wrong listed (docs/architecture/34-pos.md 11.1). A product that exists (by code) is updated in its
// name, category, charge code, station, profile and price; its variants and add-ons are not touched. With dryRun nothing is written.
func (s *Service) ImportProducts(ctx context.Context, propertyID int64, text string, dryRun bool) (ImportResult, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return ImportResult{}, err
	}
	rows, perr := parseProductsCSV(text)
	if perr != nil {
		return ImportResult{}, perr
	}
	res := ImportResult{DryRun: dryRun, Rows: len(rows), Errors: []ImportRowError{}}
	run := func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		cats, err := q.ListCategories(ctx, posdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		catID := map[string]int64{}
		for _, c := range cats {
			if c.IsActive {
				catID[c.Code] = c.ID
			}
		}
		codes, err := q.ListChargeCodeRefs(ctx, posdb.ListChargeCodeRefsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		chargeID := map[string]int64{}
		for _, c := range codes {
			if c.IsActive && c.ChargeType != "ROOM" && c.ChargeType != "TRANSFER" {
				chargeID[c.Code] = c.ID
			}
		}
		stations, err := q.ListStations(ctx, posdb.ListStationsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		stationID := map[string]int64{}
		for _, st := range stations {
			if st.IsActive {
				stationID[st.Code] = st.ID
			}
		}
		lists, err := q.ListPriceLists(ctx, posdb.ListPriceListsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		listID := map[string]int64{}
		for _, l := range lists {
			if l.IsActive {
				listID[l.Code] = l.ID
			}
		}
		profs, err := s.profiles(ctx, p.TenantID, propertyID, nil)
		if err != nil {
			return err
		}
		profID := map[string]int64{}
		for _, pr := range profs {
			if pr.IsActive {
				profID[pr.Code] = pr.ID
			}
		}
		existing, err := s.products(ctx, p.TenantID, propertyID, nil, nil, nil)
		if err != nil {
			return err
		}
		byCode := map[string]Product{}
		for _, e := range existing {
			byCode[e.Code] = e
		}
		bad := func(r importRow, field, code, detail string) {
			res.Errors = append(res.Errors, ImportRowError{Row: r.n, Field: field, Code: code, Detail: detail})
		}
		type plan struct {
			in      ProductInput
			price   *decimal.Decimal
			listID  int64
			sold    bool
			existed *Product
		}
		var plans []plan
		seen := map[string]int{}
		for _, r := range rows {
			before := len(res.Errors)
			if r.code == "" || len(r.code) > 30 {
				bad(r, "code", "INVALID_FORMAT", "1 to 30 characters")
			} else if first, dup := seen[strings.ToLower(r.code)]; dup {
				bad(r, "code", "DUPLICATE", "the code is on row "+itoa(first))
			}
			seen[strings.ToLower(r.code)] = r.n
			if n := len([]rune(r.name)); n < 1 || n > maxName {
				bad(r, "name", "INVALID_VALUE", "1 to 100 characters")
			}
			if len([]rune(r.shortName)) > maxShort {
				bad(r, "short_name", "INVALID_VALUE", "at most 24 characters")
			}
			in := ProductInput{Code: r.code, Name: r.name, IsActive: nil}
			if r.shortName != "" {
				sn := r.shortName
				in.ShortName = &sn
			}
			cid, ok := catID[normalCode(r.category)]
			if !ok {
				bad(r, "category", "NOT_FOUND", "no such active category: "+r.category)
			}
			in.CategoryID = cid
			ccid, ok := chargeID[r.chargeCode]
			if !ok {
				ccid, ok = chargeID[normalCode(r.chargeCode)]
			}
			if !ok {
				bad(r, "charge_code", "NOT_FOUND", "no such charge code a sale may use: "+r.chargeCode)
			}
			in.ChargeCodeID = ccid
			if r.station != "" {
				if id, ok := stationID[normalCode(r.station)]; ok {
					in.StationID = &id
				} else {
					bad(r, "station", "NOT_FOUND", "no such active station: "+r.station)
				}
			}
			if r.profile != "" {
				if id, ok := profID[normalCode(r.profile)]; ok {
					in.RuleProfileID = &id
				} else {
					bad(r, "profile", "NOT_FOUND", "no such active profile: "+r.profile)
				}
			}
			pl := plan{in: in, sold: true}
			if r.soldText != "" {
				switch strings.ToLower(r.soldText) {
				case "1", "true", "yes", "y", "ya":
					pl.sold = true
				case "0", "false", "no", "n", "tidak":
					pl.sold = false
				default:
					bad(r, "is_sold", "INVALID_VALUE", "yes or no")
				}
			}
			if (r.priceList == "") != (r.priceText == "") {
				bad(r, "price", "REQUIRED", "price_list and price go together")
			} else if r.priceList != "" {
				id, ok := listID[normalCode(r.priceList)]
				if !ok {
					bad(r, "price_list", "NOT_FOUND", "no such active price list: "+r.priceList)
				}
				pl.listID = id
				v, err := decimal.NewFromString(strings.ReplaceAll(r.priceText, ",", "."))
				if err != nil {
					bad(r, "price", "INVALID_VALUE", "a number")
				} else {
					if f := checkMoney("price", v, dec, false); len(f) > 0 {
						bad(r, "price", f[0].Code, f[0].Message)
					}
					pl.price = &v
				}
			}
			if e, ok := byCode[r.code]; ok {
				ec := e
				pl.existed = &ec
			}
			if len(res.Errors) == before {
				plans = append(plans, pl)
			}
		}
		if len(res.Errors) > 0 || dryRun {
			for _, pl := range plans {
				if pl.existed != nil {
					res.Updated++
				} else {
					res.Created++
				}
			}
			return nil
		}
		for _, pl := range plans {
			var id int64
			if pl.existed != nil {
				e := *pl.existed
				id = e.ID
				if _, err := q.UpdateProduct(ctx, posdb.UpdateProductParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: e.Version, Name: pl.in.Name, ShortName: pl.in.ShortName,
					CategoryID: pl.in.CategoryID, ChargeCodeID: pl.in.ChargeCodeID, RuleProfileID: pl.in.RuleProfileID, StationID: pl.in.StationID, IsActive: e.IsActive, ActorID: p.ActorID()}); err != nil {
					return err
				}
				res.Updated++
			} else {
				nid, err := q.InsertProduct(ctx, posdb.InsertProductParams{TenantID: p.TenantID, PropertyID: propertyID, Code: pl.in.Code, Name: pl.in.Name, ShortName: pl.in.ShortName,
					CategoryID: pl.in.CategoryID, ChargeCodeID: pl.in.ChargeCodeID, RuleProfileID: pl.in.RuleProfileID, StationID: pl.in.StationID, IsActive: true, ActorID: p.ActorID()})
				if err != nil {
					return err
				}
				id = nid
				res.Created++
			}
			if pl.price != nil {
				if err := q.UpsertPriceListItem(ctx, posdb.UpsertPriceListItemParams{TenantID: p.TenantID, PropertyID: propertyID, PriceListID: pl.listID, ProductID: id, Price: *pl.price,
					IsSold: pl.sold, StationID: nil, ActorID: p.ActorID()}); err != nil {
					return err
				}
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.products_imported", "pos_product", 0, "", nil, map[string]any{"rows": res.Rows, "created": res.Created, "updated": res.Updated}))
	}
	if dryRun {
		err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
			day, err := s.days.CurrentBusinessDay(ctx, propertyID)
			if err != nil {
				return err
			}
			return run(ctx, day.BusinessDate)
		})
	} else {
		err = s.write(ctx, propertyID, run)
	}
	if err != nil {
		return ImportResult{}, err
	}
	if len(res.Errors) > 0 {
		res.Created, res.Updated = 0, 0
	}
	return res, nil
}

func parseProductsCSV(text string) ([]importRow, error) {
	text = strings.TrimPrefix(text, string(rune(65279))) // the byte order mark of a file saved by a spreadsheet
	if strings.TrimSpace(text) == "" {
		return nil, invalid("import", fieldErr("csv", "REQUIRED", "the CSV is empty"))
	}
	first := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		first = text[:i]
	}
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	if strings.Count(first, ";") > strings.Count(first, ",") {
		r.Comma = ';'
	}
	head, err := r.Read()
	if err != nil {
		return nil, invalid("import", fieldErr("csv", "INVALID_FORMAT", "the header row cannot be read"))
	}
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !importColumns[h] {
			return nil, invalid("import", fieldErr("csv", "UNKNOWN_COLUMN", "unknown column: "+h))
		}
		col[h] = i
	}
	for _, need := range []string{"code", "name", "category", "charge_code"} {
		if _, ok := col[need]; !ok {
			return nil, invalid("import", fieldErr("csv", "MISSING_COLUMN", "missing column: "+need))
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var out []importRow
	n := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		n++
		if err != nil {
			return nil, apperr.Invalid("the CSV cannot be read", fieldErr("csv", "INVALID_FORMAT", "row "+itoa(n)+": "+err.Error()))
		}
		if len(strings.Join(rec, "")) == 0 {
			continue
		}
		out = append(out, importRow{n: n, code: get(rec, "code"), name: get(rec, "name"), shortName: get(rec, "short_name"), category: get(rec, "category"),
			chargeCode: get(rec, "charge_code"), station: get(rec, "station"), profile: get(rec, "profile"), priceList: get(rec, "price_list"), priceText: get(rec, "price"), soldText: get(rec, "is_sold")})
		if len(out) > maxImportRows {
			return nil, invalid("import", fieldErr("csv", "TOO_MANY_ROWS", "at most 2000 rows in one import"))
		}
	}
	if len(out) == 0 {
		return nil, invalid("import", fieldErr("csv", "REQUIRED", "no rows"))
	}
	return out, nil
}
