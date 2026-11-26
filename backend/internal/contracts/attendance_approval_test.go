package contracts

import (
	"strings"
	"testing"
)

func TestAttendanceApprovalContract(t *testing.T) {
	for _, s := range []string{"", "page=1", "page=10"} {
		if _, e := AttendanceApprovalPage(s); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{"page=0", "page=-1", "page=01", "emp_id=1", "page=1&page=2", "page=", "page=10001"} {
		if _, e := AttendanceApprovalPage(s); e == nil {
			t.Fatal(s)
		}
	}
	v := AttendanceDecision{Decision: "reject", Revision: strings.Repeat("a", 64), Comment: strings.Repeat("ก", 2000)}
	if e := ValidateAttendanceDecision(&v); e != nil {
		t.Fatal(e)
	}
	v.Comment += "ก"
	if ValidateAttendanceDecision(&v) == nil {
		t.Fatal("overlong comment accepted")
	}
}
