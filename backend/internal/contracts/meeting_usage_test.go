package contracts

import "testing"

func TestMeetingUsageQueryRequiresOneExplicitValidDate(t *testing.T) {
	for _, raw := range []string{"", "date=", "date=2026-02-29", "date=2026-1-01", "date=2026-11-18&date=2026-11-19", "date=2026-11-18&emp_id=99", "date=2026-11-18&hospital_code=11447", "date=%zz", "date=1800-01-01"} {
		if _, err := MeetingUsageQuery(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if date, err := MeetingUsageQuery("date=2028-02-29"); err != nil || date != "2028-02-29" {
		t.Fatalf("valid leap day %q %v", date, err)
	}
}
func TestMeetingUsageValidationRejectsIrrelevantAndInvalidIntervals(t *testing.T) {
	good := MeetingUsage{Date: "2026-11-18", CheckedAt: "2026-11-18T00:00:00Z", Items: []MeetingUsageItem{{RoomID: "A", RoomName: "ห้อง A", StartDate: "2026-11-17", EndDate: "2026-11-19", StartTime: "23:00", EndTime: "01:00", Status: "pending", StatusLabel: "รออนุมัติ"}}}
	if !ValidMeetingUsage(&good) {
		t.Fatal("multi-day pending interval rejected")
	}
	for name, change := range map[string]func(*MeetingUsage){
		"invalid checked time": func(v *MeetingUsage) { v.CheckedAt = "bad" },
		"missing items":        func(v *MeetingUsage) { v.Items = nil },
		"outside date":         func(v *MeetingUsage) { v.Items[0].StartDate = "2026-11-19" },
		"ended midnight":       func(v *MeetingUsage) { v.Items[0].EndDate = v.Date; v.Items[0].EndTime = "00:00" },
		"cancelled":            func(v *MeetingUsage) { v.Items[0].Status = "cancelled" },
		"bad owned id":         func(v *MeetingUsage) { v.Items[0].BookingID = "other-user" },
	} {
		t.Run(name, func(t *testing.T) {
			v := good
			v.Items = append([]MeetingUsageItem{}, good.Items...)
			change(&v)
			if ValidMeetingUsage(&v) {
				t.Fatal("invalid usage accepted")
			}
		})
	}
}
