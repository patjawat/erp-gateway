package contracts

import "testing"

func TestAttendanceMonthOnlyAcceptsExplicitCalendarMonth(t *testing.T) {
	for _, query := range []string{"month=2026-11", "month=2024-02", "month=2000-01"} {
		if _, err := AttendanceMonth(query); err != nil {
			t.Fatalf("valid month rejected %q: %v", query, err)
		}
	}
	for _, query := range []string{"", "month=2026-1", "month=2026-00", "month=2026-13", "month=2026-11-01", "month=1899-01", "month=2201-01", "month=2026-11&emp_id=99", "month=2026-11&hospital_code=12448", "month=2026-11&month=2026-12", "month=%zz", "year=2026"} {
		if _, err := AttendanceMonth(query); err == nil {
			t.Errorf("invalid selection accepted %q", query)
		}
	}
}
