package contracts

import (
	"strings"
	"testing"
)

func TestRepairHistorySearchQuery(t *testing.T) {
	p, size, q, err := RepairHistoryQuery("q=printer%25&status=success&page=2&per_page=10&fiscal_year=2570")
	if err != nil || p != 2 || size != 10 || q.Get("q") != "printer%" || q.Get("fiscal_year") != "2570" {
		t.Fatal(p, size, q, err)
	}
	for _, raw := range []string{"fiscal_year=", "fiscal_year=2570&fiscal_year=2569", "fiscal_year=2570.0", "fiscal_year=02570", "fiscal_year=2442", "fiscal_year=2745", "fiscal_year=all", "emp_id=1", "hospital_code=1", "q=a&q=b", "status=done", "q=%00", "q=" + strings.Repeat("a", 101), "page=0", "per_page=101", "q=%ff"} {
		if _, _, _, err := RepairHistoryQuery(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}
