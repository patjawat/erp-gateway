package contracts

import (
	"fmt"
	"testing"
)

func TestLeaveHistoryMonthSummariesValidateFullCountsForPageMonths(t *testing.T) {
	items := []Leave{{StartDate: "2026-12-10"}, {StartDate: "2026-11-30"}}
	valid := func() []LeaveHistoryMonthSummary {
		return []LeaveHistoryMonthSummary{
			{Year: 2026, Month: 12, Total: 8, LeaveTypes: []LeaveHistoryTypeCount{{LeaveTypeID: "LT3", LeaveType: "ลากิจส่วนตัว", Count: 3}, {LeaveTypeID: "CUSTOM", LeaveType: "ประเภทเพิ่มเติม", Count: 5}}},
			{Year: 2026, Month: 11, Total: 2, LeaveTypes: []LeaveHistoryTypeCount{{LeaveTypeID: "LT4", LeaveType: "ลาพักผ่อน", Count: 2}}},
		}
	}
	if !ValidLeaveHistoryMonthSummaries(valid(), items, 10) || !ValidLeaveHistoryMonthSummaries(nil, items, 10) || !ValidLeaveHistoryMonthSummaries([]LeaveHistoryMonthSummary{}, nil, 0) {
		t.Fatal("valid full-scope or legacy metadata rejected")
	}
	for _, scenario := range []struct {
		name   string
		change func([]LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary
	}{
		{"missing page month", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { return v[:1] }},
		{"extra unrelated month", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].Month = 10; return v }},
		{"duplicate month", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[1] = v[0]; return v }},
		{"invalid month", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].Month = 13; return v }},
		{"partial unknown key", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].Year = 0; return v }},
		{"negative count", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].LeaveTypes[0].Count = -1; return v }},
		{"partial counts", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].LeaveTypes[0].Count = 2; return v }},
		{"duplicate type", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary {
			v[0].LeaveTypes[1].LeaveTypeID = "LT3"
			return v
		}},
		{"missing label", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary {
			v[0].LeaveTypes[0].LeaveType = " "
			return v
		}},
		{"count over scope", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].Total = 11; return v }},
		{"missing type counts", func(v []LeaveHistoryMonthSummary) []LeaveHistoryMonthSummary { v[0].LeaveTypes = nil; return v }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if ValidLeaveHistoryMonthSummaries(scenario.change(valid()), items, 10) {
				t.Fatal("malformed or partial metadata accepted")
			}
		})
	}
}

func TestLeaveHistoryMonthSummariesKeepUnknownDatesAndBoundTypeGroups(t *testing.T) {
	for _, date := range []string{"", "0000-00-00", "2026-02-30", "unknown"} {
		if LeaveHistoryMonthKey(date) != "" {
			t.Fatalf("unknown date acquired a month: %q", date)
		}
	}
	unknown := []LeaveHistoryMonthSummary{{Total: 2, LeaveTypes: []LeaveHistoryTypeCount{{LeaveType: "ไม่ระบุประเภทการลา", Count: 2}}}}
	if !ValidLeaveHistoryMonthSummaries(unknown, []Leave{{StartDate: ""}}, 2) {
		t.Fatal("unknown date/type bucket rejected")
	}
	groups := make([]LeaveHistoryTypeCount, 1001)
	for index := range groups {
		groups[index] = LeaveHistoryTypeCount{LeaveTypeID: fmt.Sprintf("TYPE%d", index), LeaveType: "ประเภทการลา", Count: 1}
	}
	if ValidLeaveHistoryMonthSummaries([]LeaveHistoryMonthSummary{{Year: 2026, Month: 11, Total: 1001, LeaveTypes: groups}}, []Leave{{StartDate: "2026-11-01"}}, 1001) {
		t.Fatal("unbounded type metadata accepted")
	}
}
