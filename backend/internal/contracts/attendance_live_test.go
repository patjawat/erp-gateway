package contracts

import (
	"encoding/json"
	"testing"
)

func TestAttendancePunchValidation(t *testing.T) {
	raw := `{"latitude":0,"longitude":0,"accuracy_m":5,"captured_at":"2026-11-18T12:00:00+07:00","request_id":"1234567890123456","expected_action":"in","work_mode":"WFH","location_type":"OFF SITE"}`
	var good AttendancePunchRequest
	if json.Unmarshal([]byte(raw), &good) != nil || ValidateAttendancePunch(good) != nil {
		t.Fatal("valid rejected")
	}
	for _, change := range []func(*AttendancePunchRequest){func(v *AttendancePunchRequest) { v.Latitude = nil }, func(v *AttendancePunchRequest) { n := 91.; v.Latitude = &n }, func(v *AttendancePunchRequest) { v.RequestID = "x" }, func(v *AttendancePunchRequest) { v.ExpectedAction = "approved" }, func(v *AttendancePunchRequest) { v.PhotoID = "../../other" }, func(v *AttendancePunchRequest) { v.WorkMode = "override" }, func(v *AttendancePunchRequest) { v.CapturedAt = "yesterday" }} {
		v := good
		change(&v)
		if ValidateAttendancePunch(v) == nil {
			t.Fatal("invalid accepted", v)
		}
	}
}

func TestAttendanceCheckoutReasonValidation(t *testing.T) {
	coordinate, accuracy := 16.0, 5.0
	base := AttendancePunchRequest{
		AttendanceLocationRequest: AttendanceLocationRequest{Latitude: &coordinate, Longitude: &coordinate, AccuracyM: &accuracy, CapturedAt: "2026-11-19T00:00:00+07:00"},
		RequestID:                 "checkoutreason01",
		ExpectedAction:            "out",
	}
	for _, reason := range []string{"", "night_shift", "afternoon_shift", "overtime", "forgot_checkout"} {
		t.Run("out/"+reason, func(t *testing.T) {
			v := base
			v.CheckoutReason = reason
			if err := ValidateAttendancePunch(v); err != nil {
				t.Fatalf("valid checkout reason rejected: %v", err)
			}
			data, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if reason != "" && string(fields["checkout_reason"]) != `"`+reason+`"` {
				t.Fatalf("checkout reason has wrong wire name: %s", data)
			}
			var restored AttendancePunchRequest
			if err := json.Unmarshal(data, &restored); err != nil || restored.CheckoutReason != reason {
				t.Fatalf("checkout reason lost on JSON round trip: %s, %v", data, err)
			}
			if reason != "" {
				v.ExpectedAction = "in"
				if ValidateAttendancePunch(v) == nil {
					t.Fatal("checkout reason accepted for check-in")
				}
			}
		})
	}
	for _, reason := range []string{"other", "Over-Time", "OVERTIME", " overtime", "overtime ", "ทำโอที"} {
		t.Run("invalid/"+reason, func(t *testing.T) {
			v := base
			v.CheckoutReason = reason
			if ValidateAttendancePunch(v) == nil {
				t.Fatal("unsupported checkout reason accepted")
			}
		})
	}
}

func TestAttendanceContextCheckoutReasonJSONCompatibility(t *testing.T) {
	var legacy AttendanceContext
	if err := json.Unmarshal([]byte(`{"server_time":"2026-11-19T00:00:00+07:00","next_action":"out","write_enabled":true}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ScheduledEnd != nil || legacy.CheckoutReasonRequired || legacy.CheckoutReasonSupported {
		t.Fatalf("legacy context unexpectedly enables checkout reasons: %+v", legacy)
	}
	scheduledEnd := "2026-11-18T16:30:00+07:00"
	current := AttendanceContext{
		ServerTime: "2026-11-19T00:00:00+07:00", NextAction: "out", WriteEnabled: true,
		ScheduledEnd: &scheduledEnd, CheckoutReasonRequired: true, CheckoutReasonSupported: true,
	}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["scheduled_end"]) != `"`+scheduledEnd+`"` || string(fields["checkout_reason_required"]) != "true" || string(fields["checkout_reason_supported"]) != "true" {
		t.Fatalf("checkout capabilities have wrong wire names: %s", data)
	}
	var restored AttendanceContext
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ScheduledEnd == nil || *restored.ScheduledEnd != scheduledEnd || !restored.CheckoutReasonRequired || !restored.CheckoutReasonSupported {
		t.Fatalf("checkout capabilities lost on JSON round trip: %s", data)
	}
}
