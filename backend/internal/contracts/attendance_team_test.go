package contracts

import (
	"strings"
	"testing"
)

func TestAttendanceTeamStrictSelections(t *testing.T) {
	for _, test := range []struct{ query, kind string }{{"role=head", "summary"}, {"employee_id=99", "departments"}, {"hospital_code=11447", "members"}, {"department_id=2&status=checked&per_page=51", "members"}, {"department_id=2&status=checked&page=0", "members"}, {"department_id=2&status=checked&page=1000001", "members"}, {"department_id=2&status=checked&status=pending", "members"}, {"department_id=2&status=approved", "members"}, {"department_id=2&status=checked&direct_only=1", "members"}, {"department_id=2&status=checked&expected_date=2026-02-30", "members"}, {"department_id=2&status=checked&scope_key=wrong", "members"}, {"per_page=30&per_page=30", "departments"}, {"status=pending", "departments"}, {"department_id=2", "members"}, {"%zz", "departments"}} {
		if _, e := ParseAttendanceTeamSelection(test.query, test.kind); e == nil {
			t.Fatal("selection accepted", test)
		}
	}
	q := "department_id=2&status=pending&direct_only=true&page=2&per_page=1&scope_key=" + strings.Repeat("a", 64) + "&expected_date=2026-11-22"
	s, e := ParseAttendanceTeamSelection(q, "members")
	if e != nil || s.Page != 2 || s.PerPage != 1 || !s.DirectOnly {
		t.Fatal(s, e)
	}
	again, e := ParseAttendanceTeamSelection(s.Query(), "members")
	if e != nil || again != s {
		t.Fatal("canonical query changed", again, e)
	}
}

func TestAttendanceTeamMemberValidationScopeCountsAndDate(t *testing.T) {
	s, _ := ParseAttendanceTeamSelection("department_id=2&status=checked&scope_key="+strings.Repeat("a", 64)+"&expected_date=2026-11-22", "members")
	at := "2026-11-22T07:42:00+07:00"
	valid := AttendanceTeamMembersPage{Date: "2026-11-22", ScopeKey: strings.Repeat("a", 64), DepartmentID: "2", Department: "IT", SelectedDepartmentID: "2", SelectedDepartment: "IT", Status: "checked", CheckedCount: 1, TotalPeople: 2, PendingCount: 1, Items: []AttendanceTeamMember{{ID: "17", FullName: "Test Member", GivenName: "Test", DepartmentID: "2", Department: "IT", CheckInAt: &at}}, Page: 1, PerPage: 30, TotalCount: 1}
	if !ValidAttendanceTeamMembersPage(valid, s) {
		t.Fatal("valid page rejected")
	}
	for _, change := range []func(*AttendanceTeamMembersPage){func(v *AttendanceTeamMembersPage) { v.ScopeKey = strings.Repeat("b", 64) }, func(v *AttendanceTeamMembersPage) { v.Date = "2026-11-23" }, func(v *AttendanceTeamMembersPage) { v.PendingCount = 2 }, func(v *AttendanceTeamMembersPage) { v.TotalCount = 2 }, func(v *AttendanceTeamMembersPage) { v.Items = nil }, func(v *AttendanceTeamMembersPage) { v.Items[0].DepartmentID = "3" }, func(v *AttendanceTeamMembersPage) { v.Items[0].CheckInAt = nil }, func(v *AttendanceTeamMembersPage) { v.Items[0].AvatarURL = "https://private.invalid/photo" }} {
		v := valid
		v.Items = append([]AttendanceTeamMember{}, valid.Items...)
		change(&v)
		if ValidAttendanceTeamMembersPage(v, s) {
			t.Fatal("malformed or escaped page accepted", v)
		}
	}
}
