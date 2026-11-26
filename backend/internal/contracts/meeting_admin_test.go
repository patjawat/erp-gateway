package contracts

import "testing"

func TestMeetingAdminQueryBoundsAndClosedSelectors(t *testing.T) {
	for _, raw := range []string{"page=0", "page=1000001", "per_page=101", "per_page=-1", "page=1&page=2", "owner=17", "status=pending", "room_id=A"} {
		if _, err := MeetingAdminQuery(raw, false); err == nil {
			t.Errorf("room selector accepted %q", raw)
		}
	}
	for _, raw := range []string{"", "room_id=", "room_id=A&employee=17", "room_id=A&status=Pass", "room_id=A&status=pending&per_page=500"} {
		if _, err := MeetingAdminQuery(raw, true); err == nil {
			t.Errorf("booking selector accepted %q", raw)
		}
	}
	v, err := MeetingAdminQuery("room_id=A&status=pending&page=2&per_page=20", true)
	if err != nil || v.RoomID != "A" || v.Page != 2 || v.PerPage != 20 || v.Status != "pending" {
		t.Fatalf("selection %#v %v", v, err)
	}
}

func TestMeetingAdminRoutesAndImageSelectors(t *testing.T) {
	for _, v := range []struct{ path, kind, method string }{{"/api/meetings/admin/rooms", "rooms", "GET"}, {"/api/meetings/admin/bookings", "bookings", "GET"}, {"/api/meetings/admin/bookings/17", "detail", "GET"}, {"/api/meetings/admin/bookings/17/decision", "decision", "POST"}, {"/api/meetings/admin/rooms/1/photo", "room_photo", "GET"}, {"/api/meetings/admin/bookings/17/requester-avatar", "requester_avatar", "GET"}} {
		_, kind, method := MeetingAdminRoute(v.path)
		if kind != v.kind || method != v.method {
			t.Errorf("route %s =%s %s", v.path, kind, method)
		}
	}
	for _, path := range []string{"/api/meetings/admin/rooms/0/photo", "/api/meetings/admin/bookings/../17/decision", "/api/meetings/admin/bookings/17/requester-avatar/extra", "/api/meetings/admin/bookings/17/cancel", "/api/meetings/admin/rooms/1"} {
		if _, _, method := MeetingAdminRoute(path); method != "" {
			t.Errorf("route accepted %s", path)
		}
	}
	if !ValidMeetingAdminDecision(MeetingAdminDecision{RequestID: "meeting-admin-request-01", Decision: "approve"}) || ValidMeetingAdminDecision(MeetingAdminDecision{RequestID: "meeting-admin-request-01", Decision: "Pass"}) {
		t.Fatal("decision validation")
	}
	b := MeetingAdminBooking{Meeting: Meeting{MeetingInput: MeetingInput{RoomID: "A", Title: "ประชุม"}, ID: "17", Code: "MTG17", Status: "pending"}, Requester: MeetingRequester{EmployeeID: "9", AvatarURL: MeetingAdminRequesterAvatarURL("17")}, CanApprove: true}
	if !ValidMeetingAdminBooking(&b) {
		t.Fatal("valid booking rejected")
	}
	b.Requester.AvatarURL = MeetingAdminRequesterAvatarURL("18")
	if ValidMeetingAdminBooking(&b) {
		t.Fatal("cross-booking photo accepted")
	}
	b.Requester.AvatarURL = "https://outside.example/photo"
	if ValidMeetingAdminBooking(&b) {
		t.Fatal("external photo accepted")
	}
}
