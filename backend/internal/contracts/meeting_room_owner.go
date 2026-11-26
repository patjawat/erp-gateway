package contracts

import "strings"

// Room-owner identity is organization-visible metadata, never a personal
// contact/profile response. Missing or unresolved owners remain null.
type MeetingRoomOwner struct {
	EmployeeID string `json:"employee_id"`
	GivenName  string `json:"given_name"`
	FullName   string `json:"full_name"`
	Position   string `json:"position"`
	Department string `json:"department"`
	AvatarURL  string `json:"avatar_url"`
}

func MeetingRoomOwnerAvatarURL(id string) string {
	if !ValidMeetingID(id) {
		return ""
	}
	return "/api/meetings/rooms/" + id + "/owner-avatar"
}

func MeetingRoomOwnerAvatarID(path string) (string, bool) {
	const prefix, suffix = "/api/meetings/rooms/", "/owner-avatar"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, ValidMeetingID(id)
}

func ValidMeetingRoomOwnerAvatarURL(path string) bool {
	if path == "" {
		return true
	}
	_, ok := MeetingRoomOwnerAvatarID(path)
	return ok
}

func ValidMeetingRoomOwner(v *MeetingRoomOwner) bool {
	if v == nil {
		return true
	}
	if !ValidMeetingID(v.EmployeeID) || !meetingText(v.FullName, 1000, true) || strings.TrimSpace(v.FullName) == "" || !ValidMeetingRoomOwnerAvatarURL(v.AvatarURL) {
		return false
	}
	for _, s := range []string{v.GivenName, v.Position, v.Department} {
		if !meetingText(s, 1000, false) {
			return false
		}
	}
	return true
}
