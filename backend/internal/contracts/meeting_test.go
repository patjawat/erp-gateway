package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func validMeetingInput() MeetingInput {
	return MeetingInput{RoomID: "room-1", Title: "ประชุมทีม", StartDate: "2030-01-01", EndDate: "2030-01-01", StartTime: "09:00", EndTime: "10:00", Attendees: 4, Phone: "1234", UrgencyID: "normal", RequestID: "meeting-request-00001", EquipmentIDs: []string{"projector"}}
}
func TestMeetingValidationDateTimeAndRecovery(t *testing.T) {
	for name, change := range map[string]func(*MeetingInput){"impossible date": func(v *MeetingInput) { v.StartDate = "2030-02-29" }, "non ISO": func(v *MeetingInput) { v.StartDate = "01/01/2573" }, "bad hour": func(v *MeetingInput) { v.StartTime = "25:00" }, "bad minute": func(v *MeetingInput) { v.EndTime = "10:60" }, "zero attendees": func(v *MeetingInput) { v.Attendees = 0 }, "reversed": func(v *MeetingInput) { v.EndTime = "08:00" }, "empty phone": func(v *MeetingInput) { v.Phone = " " }, "duplicate equipment": func(v *MeetingInput) { v.EquipmentIDs = []string{"projector", "projector"} }, "private selector text": func(v *MeetingInput) { v.RoomID = "\x00" }, "invalid retry id": func(v *MeetingInput) { v.RequestID = "short" }} {
		t.Run(name, func(t *testing.T) {
			v := validMeetingInput()
			change(&v)
			if len(ValidateMeetingInput(&v)) == 0 {
				t.Fatal("invalid input accepted")
			}
		})
	}
	v := validMeetingInput()
	v.StartDate = "2000-01-01"
	v.EndDate = "2000-01-02"
	v.StartTime = "23:00"
	v.EndTime = "01:00"
	v.Title = "  ประชุมทีม  "
	v.EquipmentIDs = nil
	if f := ValidateMeetingInput(&v); len(f) > 0 || v.Title != "ประชุมทีม" || v.EquipmentIDs == nil {
		t.Fatalf("retry normalization: %#v %#v", v, f)
	}
	v = validMeetingInput()
	v.Title = strings.Repeat("ก", 255)
	if len(ValidateMeetingInput(&v)) > 0 {
		t.Fatal("Thai rune boundary rejected")
	}
	v.Title += "ก"
	if ValidateMeetingInput(&v)["title"] == "" {
		t.Fatal("excessive title accepted")
	}
}
func TestMeetingQueriesRejectTenantAndOwnershipSelectors(t *testing.T) {
	for _, raw := range []string{"employee_id=17", "hospital_code=11447", "page=1&page=2", "status=private", "q=%zz", "per_page=101", "q=" + strings.Repeat("x", 101)} {
		if _, e := MeetingHistoryQuery(raw); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	v, e := MeetingHistoryQuery("page=2&per_page=10&status=pending&q=%E0%B8%AB%E0%B9%89%E0%B8%AD%E0%B8%87")
	if e != nil || v.Page != 2 || v.PerPage != 10 || v.Q != "ห้อง" {
		t.Fatalf("selection %#v %v", v, e)
	}
	base := "start_date=2030-01-01&end_date=2030-01-01&start_time=09%3A00&end_time=10%3A00&attendees=4"
	for _, tail := range []string{"&emp_id=99", "&exclude_id=abc", "&attendees=5", "&start_time=08%3A00"} {
		if _, e := MeetingAvailabilityQuery(base + tail); e == nil {
			t.Fatalf("accepted %s", tail)
		}
	}
	if _, e := MeetingAvailabilityQuery(base + "&exclude_id=12"); e != nil {
		t.Fatal(e)
	}
}
func TestMeetingWireUnknownCapacity(t *testing.T) {
	raw, _ := json.Marshal(MeetingRoom{ID: "1", Name: "ห้อง1", Equipment: []string{}})
	if !strings.Contains(string(raw), `"capacity":null`) || !strings.Contains(string(raw), `"equipment":[]`) {
		t.Fatal(string(raw))
	}
}
