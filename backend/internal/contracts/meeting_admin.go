package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Administrator resources are deliberately separate from personal booking
// history. Their room/booking/image selectors are re-authorized by the API.
type MeetingAdminRoom struct {
	MeetingRoom
	PendingCount int `json:"pending_count"`
}

type MeetingAdminRooms struct {
	Items      []MeetingAdminRoom `json:"items"`
	Page       int                `json:"page"`
	PerPage    int                `json:"per_page"`
	TotalCount int                `json:"total_count"`
	HasMore    bool               `json:"has_more"`
	CanWrite   bool               `json:"can_write"`
}

type MeetingRequester struct {
	EmployeeID string `json:"employee_id"`
	GivenName  string `json:"given_name"`
	FullName   string `json:"full_name"`
	Department string `json:"department"`
	Position   string `json:"position"`
	AvatarURL  string `json:"avatar_url"`
}

type MeetingAdminBooking struct {
	Meeting
	Requester      MeetingRequester `json:"requester"`
	CanApprove     bool             `json:"can_approve"`
	CanAdminCancel bool             `json:"can_admin_cancel"`
}

type MeetingAdminBookings struct {
	Items      []MeetingAdminBooking `json:"items"`
	Page       int                   `json:"page"`
	PerPage    int                   `json:"per_page"`
	TotalCount int                   `json:"total_count"`
	HasMore    bool                  `json:"has_more"`
}

type MeetingAdminSelection struct {
	Page    int
	PerPage int
	RoomID  string
	Status  string
}

type MeetingAdminDecision struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
}

func ValidMeetingAdminDecision(v MeetingAdminDecision) bool {
	return ValidMeetingRequestID(v.RequestID) && (v.Decision == "approve" || v.Decision == "cancel")
}

func MeetingAdminQuery(raw string, bookings bool) (MeetingAdminSelection, error) {
	v := MeetingAdminSelection{Page: 1, PerPage: 20}
	q, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 2048 {
		return v, errors.New("invalid meeting admin selection")
	}
	for key, values := range q {
		if len(values) != 1 {
			return v, errors.New("invalid meeting admin selection")
		}
		switch key {
		case "page", "per_page":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || key == "page" && n > 1000000 || key == "per_page" && n > 100 {
				return v, errors.New("invalid meeting admin page")
			}
			if key == "page" {
				v.Page = n
			} else {
				v.PerPage = n
			}
		case "room_id":
			if !bookings {
				return v, errors.New("invalid meeting admin field")
			}
			v.RoomID = strings.TrimSpace(values[0])
			if !meetingText(v.RoomID, 255, true) {
				return v, errors.New("invalid meeting admin room")
			}
		case "status":
			if !bookings {
				return v, errors.New("invalid meeting admin field")
			}
			v.Status = values[0]
			switch v.Status {
			case "", "all", "pending", "approved", "cancelled", "rejected", "unknown":
			default:
				return v, errors.New("invalid meeting admin status")
			}
		default:
			return v, errors.New("invalid meeting admin field")
		}
	}
	if bookings && v.RoomID == "" {
		return v, errors.New("meeting admin room required")
	}
	return v, nil
}

func MeetingAdminRoomPhotoURL(id string) string {
	if !ValidMeetingID(id) {
		return ""
	}
	return "/api/meetings/admin/rooms/" + id + "/photo"
}
func MeetingAdminRoomPhotoID(path string) (string, bool) {
	const prefix, suffix = "/api/meetings/admin/rooms/", "/photo"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, ValidMeetingID(id)
}
func MeetingAdminRequesterAvatarURL(id string) string {
	if !ValidMeetingID(id) {
		return ""
	}
	return "/api/meetings/admin/bookings/" + id + "/requester-avatar"
}
func MeetingAdminRequesterAvatarID(path string) (string, bool) {
	const prefix, suffix = "/api/meetings/admin/bookings/", "/requester-avatar"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, ValidMeetingID(id)
}

func ValidMeetingAdminRoomPhotoURL(path string) bool {
	if path == "" {
		return true
	}
	_, ok := MeetingAdminRoomPhotoID(path)
	return ok
}
func ValidMeetingAdminRequesterAvatarURL(path string) bool {
	if path == "" {
		return true
	}
	_, ok := MeetingAdminRequesterAvatarID(path)
	return ok
}

func MeetingAdminRoute(path string) (id, kind, method string) {
	if id, ok := MeetingAdminRoomPhotoID(path); ok {
		return id, "room_photo", "GET"
	}
	if id, ok := MeetingAdminRequesterAvatarID(path); ok {
		return id, "requester_avatar", "GET"
	}
	switch path {
	case "/api/meetings/admin/rooms":
		return "", "rooms", "GET"
	case "/api/meetings/admin/bookings":
		return "", "bookings", "GET"
	}
	const prefix = "/api/meetings/admin/bookings/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", ""
	}
	suffix := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(suffix, "/decision")
	if !ValidMeetingID(id) {
		return "", "", ""
	}
	if suffix == id+"/decision" {
		return id, "decision", "POST"
	}
	if suffix == id {
		return id, "detail", "GET"
	}
	return "", "", ""
}

func ValidMeetingAdminBooking(v *MeetingAdminBooking) bool {
	if v == nil || !ValidMeetingID(v.ID) || !meetingText(v.Code, 255, true) || !meetingText(v.Title, 255, true) || !meetingText(v.RoomID, 255, true) || !ValidMeetingAdminRequesterAvatarURL(v.Requester.AvatarURL) {
		return false
	}
	if v.Requester.EmployeeID != "" && !ValidMeetingID(v.Requester.EmployeeID) {
		return false
	}
	for _, s := range []string{v.Requester.GivenName, v.Requester.FullName, v.Requester.Department, v.Requester.Position} {
		if !meetingText(s, 1000, false) {
			return false
		}
	}
	if v.Requester.AvatarURL != "" && v.Requester.AvatarURL != MeetingAdminRequesterAvatarURL(v.ID) {
		return false
	}
	return !(v.CanApprove || v.CanAdminCancel) || v.Status == "pending"
}

func ValidMeetingAdminRooms(v *MeetingAdminRooms) bool {
	if v == nil || v.Items == nil || v.Page < 1 || v.PerPage < 1 || v.PerPage > 100 || len(v.Items) > v.PerPage || v.TotalCount < 0 || v.HasMore != (v.Page*v.PerPage < v.TotalCount) {
		return false
	}
	seen := map[string]bool{}
	for _, room := range v.Items {
		if !meetingText(room.ID, 255, true) || !meetingText(room.Name, 1000, true) || room.PendingCount < 0 || seen[room.ID] || !ValidMeetingAdminRoomPhotoURL(room.ImageURL) || !ValidMeetingRoomOwner(room.Owner) || room.Equipment == nil {
			return false
		}
		seen[room.ID] = true
	}
	return true
}

func ValidMeetingAdminBookings(v *MeetingAdminBookings) bool {
	if v == nil || v.Items == nil || v.Page < 1 || v.PerPage < 1 || v.PerPage > 100 || len(v.Items) > v.PerPage || v.TotalCount < 0 || v.HasMore != (v.Page*v.PerPage < v.TotalCount) {
		return false
	}
	seen := map[string]bool{}
	for i := range v.Items {
		item := &v.Items[i]
		if seen[item.ID] || !ValidMeetingAdminBooking(item) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}
