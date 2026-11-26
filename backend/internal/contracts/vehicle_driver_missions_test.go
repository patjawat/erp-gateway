package contracts

import (
	"math"
	"strings"
	"testing"
)

func missionNumber(value float64) *float64 { return &value }

func TestVehicleDriverMissionRoutesAndPaging(t *testing.T) {
	for _, path := range []string{"/api/vehicles/drivers/18/missions", "/api/vehicles/drivers/18/missions/9"} {
		driver, _, ok := VehicleDriverMissionRoute(path)
		if !ok || driver != "18" {
			t.Fatalf("route rejected %s", path)
		}
		if _, _, ok := VehicleRoute(path); !ok {
			t.Fatalf("vehicle route rejected %s", path)
		}
	}
	for _, path := range []string{"/api/vehicles/drivers/0/missions", "/api/vehicles/drivers/18/missions/0", "/api/vehicles/drivers/18/missions/", "/api/vehicles/drivers/18/missions/9/extra", "/api/vehicles/drivers/18/other", "/api/vehicles/drivers/../missions"} {
		if _, _, ok := VehicleDriverMissionRoute(path); ok {
			t.Fatalf("invalid route accepted %s", path)
		}
	}
	q, err := VehicleDriverMissionsQuery("page=2&per_page=100")
	if err != nil || q.Page != 2 || q.PerPage != 100 {
		t.Fatalf("page %#v %v", q, err)
	}
	for _, raw := range []string{"page=0", "page=1000001", "per_page=101", "page=1&page=2", "date=2026-11-19", "driver_id=19", "scope=all", "page=%zz"} {
		if _, err := VehicleDriverMissionsQuery(raw); err == nil {
			t.Fatalf("invalid selection accepted %s", raw)
		}
	}
}

func TestVehicleDriverMissionMileageValidation(t *testing.T) {
	for _, input := range []VehicleDriverMissionInput{
		{MileageStart: missionNumber(0), MileageEnd: missionNumber(1)}, {MileageStart: missionNumber(100.25), MileageEnd: missionNumber(100.25)}, {MileageStart: missionNumber(9999999), MileageEnd: missionNumber(9999999)},
	} {
		if fields := ValidateVehicleDriverMissionInput(&input); len(fields) != 0 {
			t.Fatalf("valid mileage rejected %#v", fields)
		}
	}
	for _, input := range []VehicleDriverMissionInput{
		{}, {MileageStart: nil, MileageEnd: missionNumber(100)}, {MileageStart: missionNumber(0), MileageEnd: nil}, {MileageStart: missionNumber(-1), MileageEnd: missionNumber(100)}, {MileageStart: missionNumber(1), MileageEnd: missionNumber(0)}, {MileageStart: missionNumber(101), MileageEnd: missionNumber(100)}, {MileageStart: missionNumber(0), MileageEnd: missionNumber(10000000)}, {MileageStart: missionNumber(math.NaN()), MileageEnd: missionNumber(100)}, {MileageStart: missionNumber(1), MileageEnd: missionNumber(math.Inf(1))},
	} {
		if fields := ValidateVehicleDriverMissionInput(&input); len(fields) == 0 {
			t.Fatalf("invalid mileage accepted %#v", input)
		}
	}
}

func TestVehicleMissionOptionalValidationAndPrivatePaths(t *testing.T) {
	status := "Custom"
	input := VehicleDriverMissionInput{MileageStart: missionNumber(0), MileageEnd: missionNumber(10), DistanceKM: missionNumber(0), OilPrice: missionNumber(0), OilLiter: missionNumber(0), Status: &status, RequestID: "request_with_underscore_0001", ReceiptIDs: []string{strings.Repeat("a", 64)}}
	if fields := ValidateVehicleDriverMissionInput(&input); len(fields) != 0 {
		t.Fatalf("valid optional zero values rejected %v", fields)
	}
	for _, value := range []float64{-1, 10000000, math.Inf(1), math.NaN()} {
		bad := input
		bad.OilPrice = missionNumber(value)
		if len(ValidateVehicleDriverMissionInput(&bad)) == 0 {
			t.Fatal("invalid optional numeric accepted")
		}
	}
	for _, path := range []string{"/api/vehicles/drivers/18/missions/1/receipts/../../secret", "/api/vehicles/drivers/18/missions/1/receipts/http://outside.invalid", "/api/vehicles/drivers/18/missions/1/receipts/ABC"} {
		if _, _, _, ok := VehicleMissionReceiptRoute(path); ok {
			t.Fatalf("unsafe path %s", path)
		}
	}
	if driver, mission, id, ok := VehicleMissionReceiptRoute("/api/vehicles/drivers/18/missions/1/receipts/" + strings.Repeat("a", 64)); !ok || driver != "18" || mission != "1" || len(id) != 64 {
		t.Fatal("canonical receipt route rejected")
	}
}
