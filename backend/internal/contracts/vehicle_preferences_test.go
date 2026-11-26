package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func validVehiclePreferenceInput() VehicleInput {
	return VehicleInput{RequestID: "vehicle-preference-01", Location: "ปลายทาง", Reason: "ประชุม", Phone: "1234", StartDate: "2099-01-01", EndDate: "2099-01-01", StartTime: "09:00", EndTime: "10:00", Passengers: 1, Travelers: []VehicleTraveler{{EmployeeID: "17", Name: "ผู้ขอ"}}}
}

func TestVehiclePreferencesOptionalValidationAndLegacyPayload(t *testing.T) {
	for _, ids := range [][2]string{{"", ""}, {"1", ""}, {"", "18"}, {" 1 ", " 18 "}} {
		input := validVehiclePreferenceInput()
		input.RequestedVehicleID, input.RequestedDriverID = ids[0], ids[1]
		if fields := ValidateVehicleInput(&input); len(fields) != 0 {
			t.Fatalf("optional preferences rejected %v: %#v", ids, fields)
		}
		if input.RequestedVehicleID != strings.TrimSpace(ids[0]) || input.RequestedDriverID != strings.TrimSpace(ids[1]) {
			t.Fatal("preferences not trimmed")
		}
	}
	for _, invalid := range []string{"0", "00", "01", "-1", "+1", "1.0", "1e3", "name", "1/2", "1\n2", "18446744073709551616"} {
		input := validVehiclePreferenceInput()
		input.RequestedVehicleID, input.RequestedDriverID = invalid, invalid
		fields := ValidateVehicleInput(&input)
		if fields["requested_vehicle_id"] == "" || fields["requested_driver_id"] == "" {
			t.Fatalf("invalid preference accepted %q: %#v", invalid, fields)
		}
	}
	input := validVehiclePreferenceInput()
	raw, err := json.Marshal(input)
	if err != nil || strings.Contains(string(raw), "requested_") {
		t.Fatal("blank preferences changed legacy retry payload")
	}
	input.RequestedVehicleID = "1"
	input.RequestedDriverID = "18"
	raw, err = json.Marshal(input)
	if err != nil || !strings.Contains(string(raw), `"requested_vehicle_id":"1"`) || !strings.Contains(string(raw), `"requested_driver_id":"18"`) {
		t.Fatal("preferences missing from request payload")
	}
}
