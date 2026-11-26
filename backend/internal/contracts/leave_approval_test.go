package contracts

import (
	"math"
	"strings"
	"testing"
)

func TestLeaveApprovalSelectionIsBoundedAndRejectsAuthoritySelectors(t *testing.T) {
	for _, query := range []string{"", "page=1", "page=10000"} {
		if _, err := ParseLeaveApprovalPage(query); err != nil {
			t.Fatalf("valid page rejected: %q %v", query, err)
		}
	}
	for _, query := range []string{"page=0", "page=10001", "page=01", "page=", "page=1&page=2", "employee_id=42", "page=1&hospital=other", "status=all", "%"} {
		if _, err := ParseLeaveApprovalPage(query); err == nil {
			t.Fatalf("unsafe selector accepted: %q", query)
		}
	}
}

func TestLeaveApprovalDecisionRequiresReviewedStepAndRejectReason(t *testing.T) {
	valid := LeaveApprovalDecision{ApprovalID: "201", Decision: "approve", Revision: strings.Repeat("a", 64)}
	if err := ValidateLeaveApprovalDecision(&valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*LeaveApprovalDecision){
		func(v *LeaveApprovalDecision) { v.ApprovalID = "../201" },
		func(v *LeaveApprovalDecision) { v.Revision = "stale" },
		func(v *LeaveApprovalDecision) { v.Decision = "Pass" },
		func(v *LeaveApprovalDecision) { v.Decision = "reject"; v.Comment = " \n " },
		func(v *LeaveApprovalDecision) { v.Comment = strings.Repeat("ก", 2001) },
		func(v *LeaveApprovalDecision) { v.Comment = "bad\x00comment" },
	} {
		input := valid
		change(&input)
		if ValidateLeaveApprovalDecision(&input) == nil {
			t.Fatalf("unsafe decision accepted: %+v", input)
		}
	}
	valid.Decision, valid.Comment = "reject", "  ข้อมูลไม่ครบ  "
	if err := ValidateLeaveApprovalDecision(&valid); err != nil || valid.Comment != "ข้อมูลไม่ครบ" {
		t.Fatal("valid rejection was not normalized", err)
	}
}

func TestLeaveApprovalOutputCannotInventActionability(t *testing.T) {
	valid := LeaveApprovalItem{ID: "101", ApprovalID: "201", RequesterName: "ผู้ขอลา", StartDate: "2026-11-24", EndDate: "2026-11-25", TotalDays: 2, Status: "Pending", ApprovalStatus: "Pending", Level: 1, Revision: strings.Repeat("a", 64), CanDecide: true}
	if !ValidLeaveApprovalItem(valid) {
		t.Fatal("valid current assignment rejected")
	}
	for _, change := range []func(*LeaveApprovalItem){
		func(v *LeaveApprovalItem) { v.Status = "Approve" },
		func(v *LeaveApprovalItem) { v.ApprovalStatus = "Pass" },
		func(v *LeaveApprovalItem) { v.Revision = "" },
		func(v *LeaveApprovalItem) { v.StartDate = "2026-11-30" },
		func(v *LeaveApprovalItem) { v.TotalDays = math.NaN() },
		func(v *LeaveApprovalItem) { v.RequesterName = " " },
	} {
		item := valid
		change(&item)
		if ValidLeaveApprovalItem(item) {
			t.Fatalf("malformed actionable assignment accepted: %+v", item)
		}
	}
}
