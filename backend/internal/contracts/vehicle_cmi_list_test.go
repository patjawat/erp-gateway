package contracts

import "testing"

func TestVehicleCMIQueryBoundsAndRequiredFilter(t *testing.T) {
	for _, raw := range []string{"", "status=all", "status=expired&status=expiring", "status=expired&page=0", "status=expired&per_page=101", "status=expired&date=2026-02-31", "status=expired&scope=all"} {
		if _, err := VehicleCMIQuery(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	q, err := VehicleCMIQuery("status=expired&page=2&per_page=20&date=2026-11-20")
	if err != nil || q.Status != "expired" || q.Page != 2 || q.PerPage != 20 {
		t.Fatalf("%#v %v", q, err)
	}
	v := VehicleCMIPage{Items: []VehicleDirectoryItem{{ID: "1", Name: "car", CMIStatus: "expiring", CMIExpiryDate: "2026-12-01"}}, Page: 2, PerPage: 20, TotalCount: 21}
	if ValidVehicleCMIPage(&v, q) {
		t.Fatal("accepted another policy status")
	}
	v.Items[0].CMIStatus = "expired"
	if !ValidVehicleCMIPage(&v, q) {
		t.Fatal("rejected valid filtered page")
	}
	q.Status = "missing"
	v.Items[0].CMIStatus = "missing"
	if ValidVehicleCMIPage(&v, q) {
		t.Fatal("missing policy cannot carry a valid expiry")
	}
	v.Items[0].CMIExpiryDate = ""
	if !ValidVehicleCMIPage(&v, q) {
		t.Fatal("missing expiry rejected")
	}
	if _, err := VehicleCMIQuery("status=missing"); err != nil {
		t.Fatal(err)
	}
}
