package contracts

import (
	"strings"
	"testing"
	"time"
)

func TestLeaveCalendarStrictBoundedSelection(t *testing.T) {
	for _, query := range []string{"", "month=2026-13", "month=2026-1", "month=1899-12", "month=2026-11&month=2026-12", "month=2026-11&department=1", "month=2026-11&page=1", "month=%zz", "month=2026-11&date=2026-11-02"} {
		if _, e := ParseLeaveCalendarSelection(query, false); e == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	for _, query := range []string{"date=2026-02-30", "date=2026-11-02&per_page=51", "date=2026-11-02&page=0", "date=2026-11-02&page=1&page=2", "date=2026-11-02&emp_id=17", "date=2026-11-02&page=1000001"} {
		if _, e := ParseLeaveCalendarSelection(query, true); e == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	if s, e := ParseLeaveCalendarSelection("date=2026-11-02", true); e != nil || s.Page != 1 || s.PerPage != 30 {
		t.Fatal(s, e)
	}
	if _, e := ParseLeaveCalendarSelection("month=2028-02", false); e != nil {
		t.Fatal(e)
	}
}

func calendarContractMonth() LeaveCalendar {
	v := LeaveCalendar{Month: "2026-11", DepartmentID: "3", Department: "แผนกทดสอบ", Days: []LeaveCalendarDay{}}
	for d := 1; d <= 30; d++ {
		v.Days = append(v.Days, LeaveCalendarDay{Date: time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Holidays: []string{}})
	}
	return v
}

func TestLeaveCalendarScopeMetadataValidationAndLegacyCompatibility(t *testing.T) {
	monthSelection, _ := ParseLeaveCalendarSelection("month=2026-11", false)
	daySelection, _ := ParseLeaveCalendarSelection("date=2026-11-02", true)
	for _, tc := range []struct {
		name, key      string
		expanded, want bool
	}{
		{"legacy exact", "", false, true},
		{"versioned exact", strings.Repeat("a", 64), false, true},
		{"versioned controlled", strings.Repeat("b", 64), true, true},
		{"expanded without identity", "", true, false},
		{"short identity", strings.Repeat("a", 63), true, false},
		{"nonhex identity", strings.Repeat("z", 64), true, false},
		{"uppercase identity", strings.Repeat("A", 64), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			month := calendarContractMonth()
			month.ScopeKey = tc.key
			month.IncludesSubdepartments = tc.expanded
			day := LeaveCalendarPage{Date: "2026-11-02", DepartmentID: "3", Department: "แผนกทดสอบ", ScopeKey: tc.key, IncludesSubdepartments: tc.expanded, Items: []LeaveCalendarEntry{}, Page: 1, PerPage: 30}
			if got := ValidLeaveCalendar(month, monthSelection); got != tc.want {
				t.Fatalf("month scope validity: %v", got)
			}
			if got := ValidLeaveCalendarPage(day, daySelection); got != tc.want {
				t.Fatalf("day scope validity: %v", got)
			}
		})
	}
}

func TestLeaveCalendarEntryDepartmentMustBePairedAndSafe(t *testing.T) {
	selection, _ := ParseLeaveCalendarSelection("date=2026-11-02", true)
	for _, tc := range []struct {
		name, id, department string
		want                 bool
	}{
		{"legacy absent", "", "", true}, {"current pair", "12", "หน่วยงานย่อย", true},
		{"ID only", "12", "", false}, {"name only", "", "หน่วยงานย่อย", false},
		{"invalid ID", "12 OR 1=1", "หน่วยงานย่อย", false}, {"blank name", "12", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := LeaveCalendarEntry{ID: "1", EmployeeID: "2", EmployeeName: "บุคลากรทดสอบ", DepartmentID: tc.id, Department: tc.department, StartDate: "2026-11-02", EndDate: "2026-11-02", Period: "ลาเต็มวัน", Status: "Approve", Approved: true}
			page := LeaveCalendarPage{Date: "2026-11-02", DepartmentID: "3", Department: "แผนกทดสอบ", Items: []LeaveCalendarEntry{item}, Page: 1, PerPage: 30, TotalCount: 1}
			if got := ValidLeaveCalendarPage(page, selection); got != tc.want {
				t.Fatalf("department metadata validity: %v", got)
			}
		})
	}
}
