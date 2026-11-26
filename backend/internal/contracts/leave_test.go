package contracts

import "testing"

func TestLeaveInputRejectsUntrustedDurationAndInvalidPeriods(t *testing.T) {
	valid := LeaveInput{RequestID: "leave-request-000001", LeaveTypeID: "LT4", StartDate: "2026-11-20", EndDate: "2026-11-20", StartTime: "08:30", EndTime: "16:30", Reason: "พักผ่อน", TravelScope: "ในประเทศ"}
	if fields := ValidateLeaveInput(&valid, false); len(fields) > 0 {
		t.Fatalf("valid input rejected %#v", fields)
	}
	tests := []struct {
		name string
		edit func(*LeaveInput)
	}{
		{"idempotency", func(v *LeaveInput) { v.RequestID = "x" }},
		{"impossible date", func(v *LeaveInput) { v.StartDate = "2026-02-30" }},
		{"fiscal year", func(v *LeaveInput) { v.StartDate = "2026-09-30"; v.EndDate = "2026-10-01" }},
		{"reverse date", func(v *LeaveInput) { v.EndDate = "2026-11-19" }},
		{"reverse time", func(v *LeaveInput) { v.StartTime = "13:00"; v.EndTime = "12:00" }},
		{"unsupported slot", func(v *LeaveInput) { v.StartTime = "09:00" }},
		{"missing reason", func(v *LeaveInput) { v.Reason = " " }},
		{"foreign employee expression", func(v *LeaveInput) { v.ReplacementEmployeeID = "17 OR 1=1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := valid
			test.edit(&v)
			if len(ValidateLeaveInput(&v, false)) == 0 {
				t.Fatal("invalid input accepted")
			}
		})
	}
	preview := valid
	preview.RequestID = ""
	preview.Reason = ""
	preview.TravelScope = ""
	if f := ValidateLeaveInput(&preview, true); len(f) > 0 {
		t.Fatalf("preview requires submission-only fields %#v", f)
	}
}
