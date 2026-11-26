package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func ambulanceJourneyInput(immediate bool) VehicleInput {
	v := validVehiclePreferenceInput()
	v.VehicleType, v.RequestMode = "ambulance", "scheduled"
	v.ReferType, v.Urgency, v.Pickup = "EMS", "critical", "จุดเกิดเหตุ"
	if immediate {
		v.RequestMode, v.ContactName = "immediate", "ผู้แจ้ง"
		v.StartDate, v.EndDate, v.StartTime, v.EndTime = "", "", "", ""
		v.Location, v.Passengers, v.Travelers = "", 0, nil
	}
	return v
}

func TestVehicleJourneyValidation(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		v := ambulanceJourneyInput(immediate)
		if fields := ValidateVehicleInput(&v); len(fields) != 0 {
			t.Fatalf("valid ambulance immediate=%v: %#v", immediate, fields)
		}
	}
	for _, tc := range []struct {
		name, field string
		edit        func(*VehicleInput)
	}{
		{"invalid fleet", "vehicle_type", func(v *VehicleInput) { v.VehicleType = "invalid" }},
		{"invalid mode", "request_mode", func(v *VehicleInput) { v.RequestMode = "urgent" }},
		{"mission required", "refer_type", func(v *VehicleInput) { v.ReferType = "" }},
		{"urgency required", "urgency", func(v *VehicleInput) { v.Urgency = "" }},
		{"pickup required", "pickup", func(v *VehicleInput) { v.Pickup = "" }},
		{"contact required", "contact_name", func(v *VehicleInput) { v.ContactName = "" }},
		{"phone required", "phone", func(v *VehicleInput) { v.Phone = "" }},
		{"reason required", "reason", func(v *VehicleInput) { v.Reason = "" }},
		{"server intake date", "start_date", func(v *VehicleInput) { v.StartDate = "2099-01-01" }},
		{"server intake time", "end_time", func(v *VehicleInput) { v.EndTime = "10:00" }},
		{"no requested car", "requested_vehicle_id", func(v *VehicleInput) { v.RequestedVehicleID = "1" }},
		{"no requested driver", "requested_driver_id", func(v *VehicleInput) { v.RequestedDriverID = "18" }},
		{"negative passengers", "passengers", func(v *VehicleInput) { v.Passengers = -1 }},
		{"no overnight", "go_type", func(v *VehicleInput) { v.GoType = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := ambulanceJourneyInput(true)
			tc.edit(&v)
			if fields := ValidateVehicleInput(&v); fields[tc.field] == "" {
				t.Fatalf("missing %s: %#v", tc.field, fields)
			}
		})
	}
	v := validVehiclePreferenceInput()
	v.PatientHN = "patient-only"
	if fields := ValidateVehicleInput(&v); fields["vehicle_type"] == "" {
		t.Fatal("general request accepted patient data")
	}
	v = validVehiclePreferenceInput()
	v.RequestMode = "immediate"
	if fields := ValidateVehicleInput(&v); fields["request_mode"] == "" {
		t.Fatal("general request accepted immediate flow")
	}
}

func TestVehicleJourneyLegacyPayloadAndTypedQueries(t *testing.T) {
	raw, err := json.Marshal(validVehiclePreferenceInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"vehicle_type", "request_mode", "refer_type", "urgency", "contact_name", "patient_"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("blank journey field changed legacy payload: %s", raw)
		}
	}
	for _, options := range []bool{false, true} {
		for _, fleet := range []string{"official", "ambulance"} {
			q, err := VehicleQuery("vehicle_type="+fleet, options)
			if err != nil || q.VehicleType != fleet {
				t.Fatalf("typed query %s: %#v %v", fleet, q, err)
			}
		}
		for _, query := range []string{"vehicle_type=unknown", "vehicle_type=", "vehicle_type=official&vehicle_type=ambulance"} {
			if _, err := VehicleQuery(query, options); err == nil {
				t.Fatalf("invalid query accepted %s", query)
			}
		}
	}
	if _, err := VehicleQuery("vehicle_type=personal", false); err != nil {
		t.Fatal(err)
	}
	if _, err := VehicleQuery("vehicle_type=personal", true); err == nil {
		t.Fatal("personal vehicle accepted for fleet resource selection")
	}
	if _, err := VehicleQuery("vehicle_type=all", false); err != nil {
		t.Fatal(err)
	}
	if _, err := VehicleQuery("vehicle_type=all", true); err == nil {
		t.Fatal("all fleets accepted for resource selection")
	}
	if kind, _, ok := VehicleRoute("/api/vehicles/form-options"); !ok || kind != "form_options" {
		t.Fatal("form route missing")
	}
}

func TestVehicleFormOptionsRejectUnexpectedFleetAndMalformedChoices(t *testing.T) {
	valid := func() *VehicleFormOptions {
		return &VehicleFormOptions{VehicleTypes: []VehicleFormChoice{{"official", "ทั่วไป"}, {"personal", "รถยนต์ส่วนตัว"}, {"ambulance", "พยาบาล"}}, ReferTypes: []VehicleFormChoice{}, Urgencies: []VehicleFormChoice{}}
	}
	if !ValidVehicleFormOptions(valid()) {
		t.Fatal("valid empty catalogues rejected")
	}
	for _, edit := range []func(*VehicleFormOptions){
		func(v *VehicleFormOptions) { v.VehicleTypes[1].ID = "unknown" },
		func(v *VehicleFormOptions) { v.VehicleTypes[1].ID = "official" },
		func(v *VehicleFormOptions) { v.VehicleTypes = v.VehicleTypes[:1] },
		func(v *VehicleFormOptions) { v.ReferTypes = nil },
		func(v *VehicleFormOptions) { v.Urgencies = []VehicleFormChoice{{"critical", ""}} },
	} {
		v := valid()
		edit(v)
		if ValidVehicleFormOptions(v) {
			t.Fatalf("invalid catalogue accepted %#v", v)
		}
	}
}

func TestPersonalVehicleRequiresOwnPlateWithoutFleetPreferences(t *testing.T) {
	valid := func() VehicleInput {
		v := validVehiclePreferenceInput()
		v.VehicleType, v.PersonalLicensePlate = "personal", " กข 1234 "
		return v
	}
	for _, goType := range []int{1, 2} {
		v := valid()
		v.GoType = goType
		if fields := ValidateVehicleInput(&v); len(fields) != 0 || v.PersonalLicensePlate != "กข 1234" {
			t.Fatalf("valid trip type %d: %#v", goType, fields)
		}
	}
	for _, tc := range []struct {
		field string
		edit  func(*VehicleInput)
	}{
		{"personal_license_plate", func(v *VehicleInput) { v.PersonalLicensePlate = " " }},
		{"personal_license_plate", func(v *VehicleInput) { v.PersonalLicensePlate = "กข\n1234" }},
		{"personal_license_plate", func(v *VehicleInput) { v.VehicleType = "official" }},
		{"requested_vehicle_id", func(v *VehicleInput) { v.RequestedVehicleID = "1" }},
		{"requested_driver_id", func(v *VehicleInput) { v.RequestedDriverID = "18" }},
	} {
		v := valid()
		tc.edit(&v)
		if fields := ValidateVehicleInput(&v); fields[tc.field] == "" {
			t.Fatalf("missing %s: %#v", tc.field, fields)
		}
	}
}
