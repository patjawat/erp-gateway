package contracts

import "testing"

func TestVehicleQueriesAndInputValidation(t *testing.T) {
	for _, raw := range []string{"scope=other", "date=2569-01-01", "date=2030-02-30", "scope=mine&scope=all", "per_page=101", "emp_id=99"} {
		if _, e := VehicleQuery(raw, false); e == nil {
			t.Fatalf("accepted invalid query %q", raw)
		}
	}
	if q, e := VehicleQuery("scope=all&date=2030-01-01&page=2&per_page=100", false); e != nil || q.Scope != "all" || q.Page != 2 || q.PerPage != 100 {
		t.Fatalf("valid query %#v %v", q, e)
	}
	if _, e := VehicleQuery("scope=all", true); e == nil {
		t.Fatal("options accepted history scope")
	}
	v := VehicleInput{RequestID: "vehicle-validation-01", Location: "ปลายทาง", Reason: "ประชุม", Phone: "1234", StartDate: "2030-01-01", EndDate: "2030-01-01", StartTime: "09:00", EndTime: "10:00", Passengers: 1, Travelers: []VehicleTraveler{{EmployeeID: "17", Name: "ผู้ขอ"}}}
	if fields := ValidateVehicleInput(&v); len(fields) != 0 || v.GoType != 1 {
		t.Fatalf("valid input %#v", fields)
	}
	v.EndTime = "08:00"
	v.Travelers = append(v.Travelers, v.Travelers[0])
	v.Passengers = 2
	f := ValidateVehicleInput(&v)
	if f["end_time"] == "" || f["travelers"] == "" {
		t.Fatalf("accepted reverse interval or duplicate traveler %#v", f)
	}
}

func TestVehicleDetailAndPhotoRoutes(t *testing.T) {
	for _, v := range []struct{ path, kind, id string }{
		{"/api/vehicles/12", "detail", "12"},
		{"/api/vehicles/12/cancel", "cancel", "12"},
		{"/api/vehicles/photos/vehicle/1", "photo_vehicle", "1"},
		{"/api/vehicles/photos/driver/18", "photo_driver", "18"},
	} {
		kind, id, ok := VehicleRoute(v.path)
		if !ok || kind != v.kind || id != v.id {
			t.Fatalf("route %q => %s %s %v", v.path, kind, id, ok)
		}
	}
	for _, path := range []string{"/api/vehicles/0", "/api/vehicles/0/cancel", "/api/vehicles/12/cancel/extra", "/api/vehicles/photos/other/1", "/api/vehicles/photos/driver/../1", "https://example.test/api/vehicles/photos/driver/18", "/api/vehicles/photos/driver/18?token=secret"} {
		if _, _, ok := VehicleRoute(path); ok {
			t.Fatalf("accepted unsupported route %q", path)
		}
	}
	if !ValidVehiclePhotoURL("/api/vehicles/photos/driver/18", "driver") || ValidVehiclePhotoURL("/api/vehicles/photos/driver/18", "vehicle") || ValidVehiclePhotoURL("//example.test/avatar.png", "driver") {
		t.Fatal("photo URL validation mismatch")
	}
}
