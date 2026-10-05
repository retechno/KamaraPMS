// Package departments holds the departments and sub-departments of a property: an accounting dimension that journal lines, folio items, charge codes, supplier bill lines
// and the budget can carry, in two levels at most (design: docs/architecture/12-departments.md). The code and the parent of a department never change, so what was
// posted to it keeps its place in the hierarchy.
package departments

import (
	"regexp"
	"strings"
)

const (
	maxName = 100
	maxSort = 100000
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)

// Department is a department (no parent) or a sub-department (a parent).
type Department struct {
	ID         int64  `json:"id"`
	ParentID   *int64 `json:"parent_id"`
	ParentCode string `json:"parent_code,omitempty"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	SortOrder  int    `json:"sort_order"`
	Active     bool   `json:"is_active"`
	Level      int    `json:"level"`
	Children   int    `json:"child_count"`
	InUse      bool   `json:"in_use"`
}

// Input creates a department: a sub-department when it has a parent.
type Input struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ParentID  *int64 `json:"parent_id"`
	SortOrder int    `json:"sort_order"`
}

// Patch changes what may change: the name, the order and whether it is in use. The code and the parent never change.
type Patch struct {
	Name      *string `json:"name"`
	SortOrder *int    `json:"sort_order"`
	Active    *bool   `json:"is_active"`
}

// Filter narrows the list.
type Filter struct{ Active *bool }

func normalCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }
