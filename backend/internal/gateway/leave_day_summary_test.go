package gateway

import (
	"math"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestSavedLeaveGatewayDaySummaryValidation(t *testing.T) {
	valid := func() contracts.Leave {
		return contracts.Leave{ID: "10", StartDate: "2026-11-20", EndDate: "2026-11-25", TotalDays: 2.5,
			DaySummary: &contracts.LeaveDaySummary{CalendarDays: 6, WeekendDays: 2, HolidayDays: 1, TotalDays: 2.5}}
	}
	value := valid()
	if !validLeaveOutput(&value) {
		t.Fatal("valid saved half-day summary was rejected")
	}
	value.DaySummary = nil
	if !validLeaveOutput(&value) {
		t.Fatal("legacy/upstream summary absence was rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*contracts.LeaveDaySummary)
	}{
		{"zero calendar", func(s *contracts.LeaveDaySummary) { s.CalendarDays = 0 }},
		{"excessive calendar", func(s *contracts.LeaveDaySummary) { s.CalendarDays = 3667 }},
		{"calendar does not match dates", func(s *contracts.LeaveDaySummary) { s.CalendarDays = 5 }},
		{"negative weekends", func(s *contracts.LeaveDaySummary) { s.WeekendDays = -1 }},
		{"negative holidays", func(s *contracts.LeaveDaySummary) { s.HolidayDays = -1 }},
		{"overlapping day counts", func(s *contracts.LeaveDaySummary) { s.HolidayDays = 5 }},
		{"recomputed total", func(s *contracts.LeaveDaySummary) { s.TotalDays = 3 }},
		{"negative total", func(s *contracts.LeaveDaySummary) { s.TotalDays = -1 }},
		{"nonfinite total", func(s *contracts.LeaveDaySummary) { s.TotalDays = math.Inf(1) }},
		{"NaN total", func(s *contracts.LeaveDaySummary) { s.TotalDays = math.NaN() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := valid()
			tc.change(value.DaySummary)
			if validLeaveOutput(&value) {
				t.Fatal("malformed saved summary accepted")
			}
		})
	}
}
