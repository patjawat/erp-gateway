package contracts

import (
	"strings"
	"testing"
)

func TestLeaveHistorySelectionSupportsOptionalFilters(t *testing.T) {
	v, err := ParseLeaveHistorySelection("fiscal_year=2569&leave_type_id=OLD-type_4&page=2&per_page=1")
	if err != nil || v.FiscalYear != 2569 || v.LeaveTypeID != "OLD-type_4" || v.Page != 2 || v.PerPage != 1 || !v.HasFilters() {
		t.Fatalf("history filters lost: %#v %v", v, err)
	}
	v, err = ParseLeaveHistorySelection("")
	if err != nil || v.Page != 1 || v.PerPage != 50 || v.HasFilters() {
		t.Fatalf("unfiltered defaults changed: %#v %v", v, err)
	}
	if _, _, err := LeavePagination("fiscal_year=2570"); err == nil {
		t.Fatal("paging-only caller silently discarded filters")
	}
}

func TestLeaveHistorySelectionRejectsUntrustedOrMalformedQueries(t *testing.T) {
	for _, query := range []string{
		"emp_id=99", "hospital_code=99999", "user_id=99", "sort=asc", "fiscal_year=", "leave_type_id=",
		"fiscal_year=2026", "fiscal_year=2442", "fiscal_year=2745", "fiscal_year=02570", "fiscal_year=%2B2570", "fiscal_year=๒๕๗๐",
		"fiscal_year=2570&fiscal_year=2569", "leave_type_id=LT4&leave_type_id=LT1", "leave_type_id=LT4%20OR%201=1",
		"leave_type_id=" + strings.Repeat("A", 33), "page=0", "page=1000001", "page=%2B1", "per_page=101", "page=1&page=2", "fiscal_year=%zz",
		strings.Repeat("x", 1025),
	} {
		if _, err := ParseLeaveHistorySelection(query); err == nil {
			t.Errorf("invalid history selection accepted: %q", query)
		}
	}
}
