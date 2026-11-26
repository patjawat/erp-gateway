package contracts

import "testing"

func TestVehicleHomeSelectionValidatesStatusAndOptionalTypeCounts(t *testing.T) {
	for _, status := range []string{"pending", "approved", "assigned", "in_progress", "completed", "rejected", "cancelled", "unknown"} {
		q, err := VehicleQuery("scope=all&vehicle_type=central&status="+status+"&include_type_counts=true&date=2026-11-22&page=2&per_page=20", false)
		if err != nil || q.Status != status || !q.IncludeTypeCounts || q.VehicleType != "central" || q.Page != 2 || q.PerPage != 20 {
			t.Fatalf("home status %q: %#v %v", status, q, err)
		}
	}
	q, err := VehicleQuery("vehicle_type=official&include_type_counts=false", false)
	if err != nil || q.IncludeTypeCounts || q.Status != "" {
		t.Fatalf("optional aggregate %#v: %v", q, err)
	}
	for _, raw := range []string{
		"status=Pending", "status=Pass", "status=all", "status=", "status=pending&status=approved",
		"include_type_counts=1", "include_type_counts=TRUE", "include_type_counts=true&include_type_counts=false",
	} {
		if _, err := VehicleQuery(raw, false); err == nil {
			t.Fatalf("accepted invalid home selection %q", raw)
		}
	}
	for _, raw := range []string{"vehicle_type=central", "status=pending", "include_type_counts=true", "include_type_counts=false"} {
		if _, err := VehicleQuery(raw, true); err == nil {
			t.Fatalf("options accepted list-only query %q", raw)
		}
	}
}

func TestVehicleTypeCountsRequireNonnegativeCentralPartition(t *testing.T) {
	for _, counts := range []*VehicleTypeCounts{nil, {All: -1}, {All: 2, Official: -1, Ambulance: 3}, {All: 2, Official: 1, Ambulance: -1}, {All: 2, Official: 3}, {All: 3, Official: 1, Ambulance: 1}} {
		if ValidVehicleTypeCounts(counts) {
			t.Fatalf("invalid partition accepted %#v", counts)
		}
	}
	for _, counts := range []*VehicleTypeCounts{{}, {All: 3, Official: 2, Ambulance: 1}} {
		if !ValidVehicleTypeCounts(counts) {
			t.Fatalf("valid central partition rejected %#v", counts)
		}
	}
}
