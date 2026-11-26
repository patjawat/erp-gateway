package contracts

import "testing"

func TestVehicleHistoryPeriodQueryKeepsScopeAndPagination(t *testing.T) {
	q, err := VehicleQuery("scope=mine&vehicle_type=all&date_from=2026-11-01&date_to=2026-11-30&page=3&per_page=20", false)
	if err != nil || q.DateFrom != "2026-11-01" || q.DateTo != "2026-11-30" || q.Date != "" || q.Scope != "mine" || q.VehicleType != "all" || q.Page != 3 || q.PerPage != 20 {
		t.Fatalf("period selection %#v: %v", q, err)
	}
	for _, raw := range []string{
		"date_from=2026-11-01", "date_to=2026-11-30",
		"date_from=&date_to=2026-11-30",
		"date_from=2026-11-30&date_to=2026-11-01",
		"date_from=2026-02-30&date_to=2026-11-30",
		"date_from=2569-11-01&date_to=2569-11-30",
		"date_from=2026-11-1&date_to=2026-11-30",
		"date_from=2026-11-01&date_to=2026-11-30&date=2026-11-19",
		"date_from=2026-11-01&date_from=2026-10-01&date_to=2026-11-30",
		"date_from=2026-11-01&date_to=2026-11-30&emp_id=99",
		"exclude_cancelled=1", "exclude_cancelled=TRUE", "exclude_cancelled=",
	} {
		if _, err := VehicleQuery(raw, false); err == nil {
			t.Fatalf("accepted invalid period %q", raw)
		}
	}
	if _, err := VehicleQuery("date_from=2026-11-01&date_to=2026-11-30", true); err == nil {
		t.Fatal("directory accepted history period")
	}
	if _, err := VehicleQuery("exclude_cancelled=true", true); err == nil {
		t.Fatal("directory accepted history status filter")
	}
	for _, value := range []string{"true", "false"} {
		q, err := VehicleQuery("scope=all&date=2026-11-19&per_page=20&exclude_cancelled="+value, false)
		if err != nil || q.ExcludeCancelled != (value == "true") || q.Scope != "all" || q.PerPage != 20 {
			t.Fatalf("cancelled selection %#v: %v", q, err)
		}
	}
	q, err = VehicleQuery("date_from=2026-12-31&date_to=2026-12-31", false)
	if err != nil || q.DateFrom != q.DateTo || q.Scope != "mine" {
		t.Fatalf("single-day inclusive period rejected %#v: %v", q, err)
	}
}
