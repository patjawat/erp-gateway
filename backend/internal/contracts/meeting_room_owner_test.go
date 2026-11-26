package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMeetingRoomOwnerContractOptionalAndScopedAvatar(t *testing.T) {
	var old MeetingRoom
	if json.Unmarshal([]byte(`{"id":"A","name":"ห้องเดิม","equipment":[]}`), &old) != nil || old.Owner != nil {
		t.Fatal("legacy room owner is not optional")
	}
	raw, _ := json.Marshal(old)
	if !strings.Contains(string(raw), `"owner":null`) {
		t.Fatal("missing owner must remain null")
	}
	owner := MeetingRoomOwner{EmployeeID: "17", GivenName: "ผู้ดูแล", FullName: "นายผู้ดูแล ห้องประชุม", Department: "งานบริหาร", Position: "เจ้าหน้าที่", AvatarURL: MeetingRoomOwnerAvatarURL("1")}
	if !ValidMeetingRoomOwner(&owner) || !ValidMeetingRoomOwner(nil) {
		t.Fatal("valid owner rejected")
	}
	for _, bad := range []string{"https://external.invalid/avatar", "/api/personnel/17/avatar", "/api/meetings/rooms/0/owner-avatar", "/api/meetings/rooms/1/owner-avatar?employee_id=99", "/api/meetings/rooms/1/photo", "/api/meetings/rooms/1/owner-avatar/extra"} {
		owner.AvatarURL = bad
		if ValidMeetingRoomOwner(&owner) {
			t.Errorf("unsafe owner avatar accepted %q", bad)
		}
	}
	owner.AvatarURL = ""
	owner.EmployeeID = "person"
	if ValidMeetingRoomOwner(&owner) {
		t.Fatal("invalid employee identity accepted")
	}
	owner.EmployeeID = "17"
	owner.FullName = " "
	if ValidMeetingRoomOwner(&owner) {
		t.Fatal("empty owner name accepted")
	}
}
