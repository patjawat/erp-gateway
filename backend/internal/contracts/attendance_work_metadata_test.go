package contracts

import (
	"encoding/json"
	"testing"
)

func TestAttendanceWorkMetadataEnumsAndCheckoutConfirmation(t *testing.T) {
	for _, value := range []string{"", "ON SITE", "ON BUSINESS TRIP", "OFF SITE"} {
		if !ValidAttendanceLocationType(value) {
			t.Fatalf("valid location type %q rejected", value)
		}
	}
	for _, value := range []string{"", "Office", "On-Business Trip", "On-Field", "Over-Time", "WFH"} {
		if !ValidAttendanceWorkMode(value) {
			t.Fatalf("valid work mode %q rejected", value)
		}
	}
	if ValidAttendanceLocationType("anywhere") || ValidAttendanceWorkMode("anything") {
		t.Fatal("invalid metadata enum accepted")
	}
	var input AttendancePunchRequest
	raw := `{"latitude":16,"longitude":102,"accuracy_m":5,"captured_at":"2026-11-18T16:30:00+07:00","request_id":"metadata-contract-request-001","expected_action":"out","location_type":"OFF SITE","work_mode":"WFH","checkout_metadata_confirmed":true}`
	if err := json.Unmarshal([]byte(raw), &input); err != nil || ValidateAttendancePunch(input) != nil || !input.CheckoutMetadataConfirmed {
		t.Fatalf("confirmed checkout rejected %#v %v", input, err)
	}
	input.ExpectedAction = "in"
	if ValidateAttendancePunch(input) == nil {
		t.Fatal("checkout metadata confirmation accepted on check-in")
	}
	input.ExpectedAction = "out"
	input.LocationType, input.WorkMode = "", ""
	if ValidateAttendancePunch(input) != nil {
		t.Fatal("explicit unknown labels rejected")
	}
	input.CheckoutMetadataConfirmed = false
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["checkout_metadata_confirmed"]; exists {
		t.Fatal("false confirmation changed legacy request/hash JSON")
	}
}

func TestAttendanceWorkMetadataContextCompatibility(t *testing.T) {
	var legacy AttendanceContext
	if err := json.Unmarshal([]byte(`{"server_time":"2026-11-18T16:30:00+07:00","next_action":"out"}`), &legacy); err != nil || legacy.CheckoutMetadataSupported || legacy.LocationType != "" || legacy.WorkMode != "" {
		t.Fatalf("legacy metadata no longer unknown %#v %v", legacy, err)
	}
	current := AttendanceContext{ServerTime: legacy.ServerTime, NextAction: "out", CheckoutMetadataSupported: true, LocationType: "OFF SITE", WorkMode: "WFH"}
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var restored AttendanceContext
	if err := json.Unmarshal(raw, &restored); err != nil || !restored.CheckoutMetadataSupported || restored.LocationType != "OFF SITE" || restored.WorkMode != "WFH" {
		t.Fatalf("metadata context wire fields lost %s %v", raw, err)
	}
}
