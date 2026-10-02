package bankrec

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

const maxStatementLines = 5000

// parsedLine is a row of an imported statement.
type parsedLine struct {
	date                   civil.Date
	description, reference string
	amount                 decimal.Decimal
}

var headerAliases = map[string]string{
	"date": "date", "transaction date": "date", "posting date": "date", "tanggal": "date",
	"description": "description", "details": "description", "memo": "description", "narrative": "description", "keterangan": "description",
	"reference": "reference", "ref": "reference", "reference number": "reference",
	"amount": "amount", "jumlah": "amount",
	"credit": "in", "deposit": "in", "money in": "in", "cr": "in", "kredit": "in",
	"debit": "out", "withdrawal": "out", "money out": "out", "dr": "out",
}

// parseDate reads YYYY-MM-DD, DD/MM/YYYY and DD-MM-YYYY.
func parseDate(s string) (civil.Date, bool) {
	s = strings.TrimSpace(s)
	if d, err := civil.ParseDate(s); err == nil {
		return d, true
	}
	for _, sep := range []string{"/", "-"} {
		parts := strings.Split(s, sep)
		if len(parts) == 3 && len(parts[2]) == 4 {
			if d, err := civil.ParseDate(parts[2] + "-" + pad2(parts[1]) + "-" + pad2(parts[0])); err == nil {
				return d, true
			}
		}
	}
	return civil.Date{}, false
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// parseAmount reads a plain amount; spaces and thousands commas are dropped. Empty is zero.
func parseAmount(s string) (decimal.Decimal, bool) {
	s = strings.NewReplacer(" ", "", ",", "", " ", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return decimal.Zero, true
	}
	d, err := decimal.NewFromString(s)
	return d, err == nil
}

// parseStatement reads the lines of a statement from CSV. The header names the columns in any order: date, description,
// reference (optional), and either amount (signed: money in positive) or two columns for money in (credit, deposit) and
// money out (debit, withdrawal). A bad file changes nothing: every problem is reported by row.
func parseStatement(text string, decimals int32) ([]parsedLine, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	if strings.TrimSpace(text) == "" {
		return nil, apperr.Invalid("the statement is invalid", fieldErr("csv", "REQUIRED", "paste or upload the lines of the statement"))
	}
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	head, err := r.Read()
	if err != nil {
		return nil, apperr.Invalid("the statement is invalid", fieldErr("csv", "UNREADABLE", "the first row must name the columns"))
	}
	col := map[string]int{}
	for i, h := range head {
		if k, ok := headerAliases[strings.ToLower(strings.TrimSpace(h))]; ok {
			if _, dup := col[k]; !dup {
				col[k] = i
			}
		}
	}
	_, hasAmount := col["amount"]
	_, hasIn := col["in"]
	_, hasOut := col["out"]
	var fields []apperr.FieldError
	if _, ok := col["date"]; !ok {
		fields = append(fields, fieldErr("csv", "NO_DATE_COLUMN", "a column named date"))
	}
	if !hasAmount && !hasIn && !hasOut {
		fields = append(fields, fieldErr("csv", "NO_AMOUNT_COLUMN", "a column named amount, or credit and debit columns"))
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the statement is invalid", fields...)
	}
	get := func(rec []string, k string) string {
		i, ok := col[k]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var out []parsedLine
	for row := 2; ; row++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fields = append(fields, fieldErr(fmt.Sprintf("rows[%d]", row), "UNREADABLE", "the row cannot be read"))
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		at := func(f string) string { return fmt.Sprintf("rows[%d].%s", row, f) }
		d, ok := parseDate(get(rec, "date"))
		if !ok {
			fields = append(fields, fieldErr(at("date"), "INVALID_DATE", "a date as YYYY-MM-DD or DD/MM/YYYY"))
		}
		var amount decimal.Decimal
		var okAmount bool
		if hasAmount {
			amount, okAmount = parseAmount(get(rec, "amount"))
		} else {
			in, ok1 := parseAmount(get(rec, "in"))
			outv, ok2 := parseAmount(get(rec, "out"))
			okAmount = ok1 && ok2
			amount = in.Sub(outv.Abs())
		}
		switch {
		case !okAmount:
			fields = append(fields, fieldErr(at("amount"), "INVALID_AMOUNT", "a number"))
		case !amount.Equal(amount.Round(3)):
			fields = append(fields, fieldErr(at("amount"), "TOO_PRECISE", "at most 3 decimals"))
		case amount.IsZero():
			continue // a line without an amount (a heading, a balance row) is skipped
		}
		if len(out) >= maxStatementLines {
			fields = append(fields, fieldErr("csv", "TOO_MANY_ROWS", fmt.Sprintf("at most %d lines", maxStatementLines)))
			break
		}
		out = append(out, parsedLine{date: d, description: get(rec, "description"), reference: get(rec, "reference"), amount: amount})
	}
	_ = decimals
	if len(fields) > 0 {
		if len(fields) > 20 {
			fields = fields[:20]
		}
		return nil, apperr.Invalid("the statement has rows that cannot be read", fields...)
	}
	return out, nil
}
