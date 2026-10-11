package budget

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// The CSV of a budget has the code of the account, its name (read only), the code of the department (optional) and the twelve months of the fiscal year, m1 to m12.
func csvHeader() []string {
	h := []string{"code", "name", "department"}
	for m := 1; m <= monthsInYear; m++ {
		h = append(h, "m"+strconv.Itoa(m))
	}
	return h
}

// ExportCSV is the grid of a budget as CSV rows (header first), in the layout ImportCSV reads (budget.view).
func (s *Service) ExportCSV(ctx context.Context, propertyID, id int64) (Budget, [][]string, error) {
	b, err := s.Get(ctx, propertyID, id)
	if err != nil {
		return Budget{}, nil, err
	}
	out := [][]string{csvHeader()}
	for _, r := range b.Rows {
		out = append(out, append([]string{r.Code, r.Name, r.DepartmentCode}, r.Amounts...))
	}
	return b, out, nil
}

type importRow struct {
	line       int
	code       string
	department string // the code of the department, empty for none
	in         RowInput
}

// parseCSV reads the rows of an import and returns every problem it finds, row by row. The amounts are checked against the currency here; the accounts
// are checked against the chart by the caller.
func parseCSV(text string, decimals int32) ([]importRow, []apperr.FieldError) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	head, err := r.Read()
	if err != nil {
		return nil, []apperr.FieldError{fieldErr("csv", "EMPTY", "a header row and the accounts")}
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["code"]; !ok {
		return nil, []apperr.FieldError{fieldErr("csv", "MISSING_COLUMN", "the header needs the column code")}
	}
	for m := 1; m <= monthsInYear; m++ {
		if _, ok := col["m"+strconv.Itoa(m)]; !ok {
			return nil, []apperr.FieldError{fieldErr("csv", "MISSING_COLUMN", "the header needs the columns m1 to m12, one per month of the fiscal year")}
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var rows []importRow
	var fields []apperr.FieldError
	seen := map[string]int{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fields = append(fields, fieldErr("rows["+strconv.Itoa(line)+"]", "INVALID_CSV", err.Error()))
			break
		}
		if len(rows) >= maxImportRows {
			fields = append(fields, fieldErr("csv", "TOO_MANY_ROWS", "at most 1000 accounts"))
			break
		}
		at := func(f string) string { return "rows[" + strconv.Itoa(line) + "]." + f }
		row := importRow{line: line, code: strings.ToUpper(get(rec, "code")), department: strings.ToUpper(get(rec, "department"))}
		if row.code == "" {
			fields = append(fields, fieldErr(at("code"), "REQUIRED", "the code of the account"))
		} else if first, dup := seen[row.code+"|"+row.department]; dup {
			fields = append(fields, fieldErr(at("code"), "DUPLICATE", "already on row "+strconv.Itoa(first)+" for the same department"))
		}
		seen[row.code+"|"+row.department] = line
		row.in.Amounts = make([]string, monthsInYear)
		for m := 1; m <= monthsInYear; m++ {
			name := "m" + strconv.Itoa(m)
			v := get(rec, name)
			if _, fe := parseAmount(at(name), v, decimals); fe != nil {
				fields = append(fields, *fe)
			}
			row.in.Amounts[m-1] = v
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 && len(fields) == 0 {
		fields = append(fields, fieldErr("csv", "EMPTY", "at least one account"))
	}
	return rows, fields
}

// errDryRun ends a dry run's transaction without committing it.
var errDryRun = errors.New("budget: dry run")

// ImportCSV replaces the grid of a draft with the accounts of a CSV file (budget.manage). Everything is checked and applied in one transaction, so a file with a
// mistake changes nothing and every wrong row is listed; dryRun checks and applies it without keeping it.
func (s *Service) ImportCSV(ctx context.Context, propertyID, id int64, text string, dryRun bool) (ImportResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return ImportResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ImportResult{}, err
	}
	parsed, fields := parseCSV(text, decimals)
	if len(fields) > 0 {
		return ImportResult{}, apperr.Invalid("the file is invalid", fields...)
	}
	out := ImportResult{DryRun: dryRun}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if _, err := s.lockDraft(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		list, _, err := s.budgetable(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		byCode := map[string]AccountRef{}
		for _, a := range list {
			byCode[a.Code] = a
		}
		names, err := s.departmentNames(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		deptByCode := map[string]int64{}
		for _, d := range names {
			deptByCode[d.code] = d.id
		}
		var problems []apperr.FieldError
		rows := make([]RowInput, 0, len(parsed))
		lineOf := make([]int, 0, len(parsed)) // the line of the file of each row, to report the department of a row where it is
		for _, r := range parsed {
			a, ok := byCode[r.code]
			if !ok {
				problems = append(problems, fieldErr("rows["+strconv.Itoa(r.line)+"].code", "NOT_FOUND", "no revenue or expense account that takes postings has the code "+r.code))
				continue
			}
			r.in.AccountID = a.ID
			if r.department != "" {
				id, found := deptByCode[r.department]
				if !found {
					problems = append(problems, fieldErr("rows["+strconv.Itoa(r.line)+"].department", "DEPARTMENT_NOT_FOUND", "no department of this property has the code "+r.department))
					continue
				}
				r.in.DepartmentID = &id
			}
			rows = append(rows, r.in)
			lineOf = append(lineOf, r.line)
		}
		if len(problems) > 0 {
			return apperr.Invalid("the file is invalid", problems...)
		}
		_, byID, err := s.budgetable(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		grid, fields := parseGrid(rows, byID, decimals)
		if len(fields) == 0 {
			checked, err := s.checkRowDepartments(ctx, p.TenantID, propertyID, rows)
			if err != nil {
				return err
			}
			fields = relabel(checked, lineOf)
		}
		if len(fields) > 0 {
			return apperr.Invalid("the file is invalid", fields...)
		}
		if err := s.replaceGrid(ctx, p.TenantID, propertyID, id, grid, decimals); err != nil {
			return err
		}
		out.Accounts = len(grid)
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.imported", id, auditlabel.Budget(ctx, propertyID, id), nil, map[string]any{"accounts": out.Accounts, "dry_run": dryRun})); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return out, nil
	}
	return out, err
}

// relabel names the department errors of the rows of a grid by the line of the file the row came from (rows[LINE].department).
func relabel(fields []apperr.FieldError, lineOf []int) []apperr.FieldError {
	for i, f := range fields {
		var idx int
		if _, err := fmt.Sscanf(f.Field, "rows[%d].department_id", &idx); err == nil && idx >= 0 && idx < len(lineOf) {
			fields[i].Field = "rows[" + strconv.Itoa(lineOf[idx]) + "].department"
		}
	}
	return fields
}
