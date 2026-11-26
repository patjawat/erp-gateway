package contracts

import (
	"strings"
	"testing"
)

func TestLeaveCalendarDepartmentSelectorsRemainBoundedAndNonAuthoritative(t *testing.T) {
	s, err := ParseLeaveCalendarSelection("date=2026-11-02&department_id=12&page=2&per_page=10", true)
	if err != nil || s.DepartmentID != "12" || s.Page != 2 || s.PerPage != 10 {
		t.Fatal("valid bounded expansion selector rejected", s, err)
	}
	for _, query := range []string{"date=2026-11-02&department_id=0", "date=2026-11-02&department_id=01", "date=2026-11-02&department_id=12%20OR%201=1", "date=2026-11-02&department_id=12&department_id=13", "date=2026-11-02&department_id=", "date=2026-11-02&department_id=184467440737095516150"} {
		if _, err := ParseLeaveCalendarSelection(query, true); err == nil {
			t.Fatal("unsafe department ID accepted", query)
		}
	}
	for _, query := range []string{"date=2026-11-02&department_id=12", "date=2026-11-02&emp_id=17", "date=2026-11-02&includes_subdepartments=true", "date=2026-11-02&per_page=51", "date=2026-11-02&page=1000001", "month=2026-11"} {
		if _, err := ParseLeaveCalendarDepartmentsSelection(query); err == nil {
			t.Fatal("group summary accepted scope injection/unbounded selection", query)
		}
	}
	if _, err := ParseLeaveCalendarDepartmentsSelection("date=2026-11-02"); err != nil {
		t.Fatal(err)
	}
}

func validCalendarDepartmentsFixture() LeaveCalendarDepartmentsPage {
	return LeaveCalendarDepartmentsPage{
		Date: "2026-11-02", DepartmentID: "3", Department: "แผนกหลัก", ScopeKey: strings.Repeat("a", 64), IncludesSubdepartments: true,
		TotalPeople: 3, TotalCount: 2, Page: 1, PerPage: 30,
		Items: []LeaveCalendarDepartment{
			{DepartmentID: "3", Department: "งานธุรการ", TotalPeople: 2, TotalRecords: 3},
			{DepartmentID: "12", Department: "งานซักฟอก", TotalPeople: 1, TotalRecords: 1, CanViewDetails: true},
		},
	}
}

func TestLeaveCalendarDepartmentSummaryContractsRejectMalformedAggregates(t *testing.T) {
	selection, _ := ParseLeaveCalendarDepartmentsSelection("date=2026-11-02")
	if !ValidLeaveCalendarDepartmentsPage(validCalendarDepartmentsFixture(), selection) {
		t.Fatal("valid grouped summary rejected")
	}
	for _, tc := range []struct {
		name       string
		breakValue func(*LeaveCalendarDepartmentsPage)
	}{
		{"wrong date", func(v *LeaveCalendarDepartmentsPage) { v.Date = "2026-11-03" }},
		{"missing scope", func(v *LeaveCalendarDepartmentsPage) { v.ScopeKey = "" }},
		{"invalid identity", func(v *LeaveCalendarDepartmentsPage) { v.DepartmentID = "0" }},
		{"nil items", func(v *LeaveCalendarDepartmentsPage) { v.Items = nil }},
		{"wrong page", func(v *LeaveCalendarDepartmentsPage) { v.Page = 2 }},
		{"wrong bound", func(v *LeaveCalendarDepartmentsPage) { v.PerPage = 50 }},
		{"negative total", func(v *LeaveCalendarDepartmentsPage) { v.TotalPeople = -1 }},
		{"understated total", func(v *LeaveCalendarDepartmentsPage) { v.TotalPeople = 2 }},
		{"incomplete page", func(v *LeaveCalendarDepartmentsPage) { v.Items = v.Items[:1] }},
		{"wrong end flag", func(v *LeaveCalendarDepartmentsPage) { v.HasMore = true }},
		{"duplicate unit", func(v *LeaveCalendarDepartmentsPage) { v.Items[1].DepartmentID = "3" }},
		{"invalid unit", func(v *LeaveCalendarDepartmentsPage) { v.Items[0].DepartmentID = "3 OR 1=1" }},
		{"blank name", func(v *LeaveCalendarDepartmentsPage) { v.Items[0].Department = " " }},
		{"zero people", func(v *LeaveCalendarDepartmentsPage) { v.Items[0].TotalPeople = 0 }},
		{"fewer records than people", func(v *LeaveCalendarDepartmentsPage) { v.Items[0].TotalRecords = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := validCalendarDepartmentsFixture()
			tc.breakValue(&v)
			if ValidLeaveCalendarDepartmentsPage(v, selection) {
				t.Fatal("malformed upstream summary accepted")
			}
		})
	}
}

func TestLeaveCalendarExpandedRecordPageMustEchoAndMatchDepartment(t *testing.T) {
	selection, _ := ParseLeaveCalendarSelection("date=2026-11-02&department_id=12", true)
	valid := func() LeaveCalendarPage {
		return LeaveCalendarPage{Date: "2026-11-02", DepartmentID: "3", Department: "แผนกหลัก", ScopeKey: strings.Repeat("a", 64), IncludesSubdepartments: true, SelectedDepartmentID: "12", Page: 1, PerPage: 30, TotalCount: 1,
			Items: []LeaveCalendarEntry{{ID: "81", EmployeeID: "17", EmployeeName: "บุคลากรทดสอบ", DepartmentID: "12", Department: "งานซักฟอก", LeaveTypeID: "LT4", LeaveType: "ลาพักผ่อน", StartDate: "2026-11-02", EndDate: "2026-11-02", Period: "ลาเต็มวัน", Status: "Approve", Approved: true}}}
	}
	if !ValidLeaveCalendarPage(valid(), selection) {
		t.Fatal("valid expansion page rejected")
	}
	for _, tc := range []struct {
		name       string
		breakValue func(*LeaveCalendarPage)
	}{
		{"missing selector echo", func(v *LeaveCalendarPage) { v.SelectedDepartmentID = "" }},
		{"wrong selector echo", func(v *LeaveCalendarPage) { v.SelectedDepartmentID = "13" }},
		{"wrong row department", func(v *LeaveCalendarPage) { v.Items[0].DepartmentID = "13" }},
		{"invalid type", func(v *LeaveCalendarPage) { v.Items[0].LeaveTypeID = strings.Repeat("x", 300) }},
		{"oversized type title", func(v *LeaveCalendarPage) { v.Items[0].LeaveType = strings.Repeat("x", 1025) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := valid()
			tc.breakValue(&v)
			if ValidLeaveCalendarPage(v, selection) {
				t.Fatal("malformed upstream expansion accepted")
			}
		})
	}
}
