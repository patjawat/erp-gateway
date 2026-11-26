package gateway

import (
	"encoding/json"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) meetingAdmin(w http.ResponseWriter, r *http.Request) {
	id, kind, method := contracts.MeetingAdminRoute(r.URL.Path)
	if method == "" {
		fail(w, 404, "NOT_FOUND", "ไม่พบรายการจัดการห้องประชุม")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if kind == "room_photo" || kind == "requester_avatar" {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรูปไม่ถูกต้อง")
			return
		}
		if kind == "room_photo" {
			s.meetingPhoto(w, r, h, token, "MEETING_ROOM_PHOTO_NOT_FOUND", "MEETING_ROOM_PHOTOS_DISABLED")
		} else {
			s.relayAvatarImage(w, r, h, token, r.URL.Path)
		}
		return
	}
	var output any
	var body []byte
	path := r.URL.Path
	var selection contracts.MeetingAdminSelection
	switch kind {
	case "rooms", "bookings":
		var err error
		selection, err = contracts.MeetingAdminQuery(r.URL.RawQuery, kind == "bookings")
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกห้องและคำขอไม่ถูกต้อง")
			return
		}
		if kind == "rooms" {
			output = &contracts.MeetingAdminRooms{}
		} else {
			output = &contracts.MeetingAdminBookings{}
		}
	default:
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกคำขอไม่ถูกต้อง")
			return
		}
		output = &contracts.MeetingAdminBooking{}
		if kind == "decision" {
			var input contracts.MeetingAdminDecision
			if !decodeMeeting(w, r, &input) {
				return
			}
			if !contracts.ValidMeetingAdminDecision(input) {
				meetingValidation(w, map[string]string{"request_id": "กรุณาตรวจสอบรหัสและผลการพิจารณาคำขอ"})
				return
			}
			body, _ = json.Marshal(input)
		}
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.upstream(r.Context(), h, r.Method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayMeetingError(w, response)
		return
	}
	if readEnvelope(response, output) != nil {
		upstreamUnavailable(w)
		return
	}
	valid := false
	switch v := output.(type) {
	case *contracts.MeetingAdminRooms:
		valid = contracts.ValidMeetingAdminRooms(v) && v.Page == selection.Page && v.PerPage == selection.PerPage
	case *contracts.MeetingAdminBookings:
		valid = contracts.ValidMeetingAdminBookings(v) && v.Page == selection.Page && v.PerPage == selection.PerPage
		if valid {
			for _, item := range v.Items {
				if item.RoomID != selection.RoomID {
					valid = false
					break
				}
			}
		}
	case *contracts.MeetingAdminBooking:
		valid = v.ID == id && contracts.ValidMeetingAdminBooking(v)
	}
	if !valid {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}
