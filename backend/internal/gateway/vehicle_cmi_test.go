package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"testing"
)

func TestVehicleCMIOptionsRejectInvalidCountsAndDates(t *testing.T) {
	v := &contracts.VehicleOptions{Vehicles: []contracts.VehicleDirectoryItem{{ID: "1", CMIExpiryDate: "2026-12-20", CMIStatus: "expiring"}}, Drivers: []contracts.VehicleDirectoryItem{}, CMISummary: &contracts.VehicleCMISummary{ExpiringCount: 1}}
	if !validVehicleOutput(v) {
		t.Fatal("valid CMI rejected")
	}
	v.CMISummary.ExpiringCount = -1
	if validVehicleOutput(v) {
		t.Fatal("negative count accepted")
	}
	v.CMISummary.ExpiringCount = 2
	if validVehicleOutput(v) {
		t.Fatal("count exceeds fleet")
	}
	v.CMISummary.ExpiringCount = 1
	v.CMISummary.MissingCount = -1
	if validVehicleOutput(v) {
		t.Fatal("negative missing count accepted")
	}
	v.CMISummary.MissingCount = 1
	if validVehicleOutput(v) {
		t.Fatal("combined warning count exceeds fleet")
	}
	v.CMISummary.MissingCount = 0
	v.Vehicles[0].CMIExpiryDate = "2026-02-31"
	if validVehicleOutput(v) {
		t.Fatal("invalid date accepted")
	}
	v.Vehicles[0].CMIExpiryDate = ""
	v.Vehicles[0].CMIStatus = "missing"
	v.CMISummary.ExpiringCount = 0
	v.CMISummary.MissingCount = 1
	if !validVehicleOutput(v) {
		t.Fatal("missing policy warning rejected")
	}
}
