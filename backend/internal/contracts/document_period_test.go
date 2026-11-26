package contracts

import (
	"strings"
	"testing"
)

func TestDocumentQueryRecognizesOptionalRelativePeriods(t *testing.T) {
	for _, period := range []string{"this_week", "this_month", "last_month", "this_year"} {
		selection, err := DocumentQuery("q=report&year=2569&period=" + period + "&status=unread&page=2&per_page=20")
		if err != nil || selection.Period != period || selection.Year != 2569 || selection.Query != "report" || selection.Status != "unread" || selection.Page != 2 || selection.PerPage != 20 {
			t.Fatal(selection, err)
		}
	}
	legacy, err := DocumentQuery("year=2568&status=read")
	if err != nil || legacy.Period != "" || legacy.Year != 2568 {
		t.Fatal("absent period changed the legacy year selection", legacy, err)
	}
	for _, query := range []string{
		"period=", "period=all", "period=this_month%20", "period=THIS_WEEK",
		"period=this_week&period=this_month", "period=" + strings.Repeat("x", 2100),
		"period=this_week&page=0", "period=this_year&per_page=101",
		"period=this_month&year=2026", "period=this_year&employee_id=3",
	} {
		if _, err := DocumentQuery(query); err == nil {
			t.Fatalf("invalid period/query accepted: %s", query)
		}
	}
}
