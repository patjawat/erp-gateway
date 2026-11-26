package gateway

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) meeting(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/meetings/admin/") {
		s.meetingAdmin(w, r)
		return
	}
	path := r.URL.Path
	id, methods := meetingRoute(path)
	if methods == "" {
		fail(w, 404, "NOT_FOUND", "ไม่พบรายการจอง")
		return
	}
	allowed := false
	for _, method := range strings.Split(methods, ", ") {
		allowed = allowed || method == r.Method
	}
	if !allowed {
		w.Header().Set("Allow", methods)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	_, roomPhoto := contracts.MeetingRoomPhotoID(path)
	_, layoutPhoto := contracts.MeetingLayoutPhotoID(path)
	_, ownerAvatar := contracts.MeetingRoomOwnerAvatarID(path)
	if roomPhoto || layoutPhoto || ownerAvatar {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรูปห้องประชุมไม่ถูกต้อง")
			return
		}
		if ownerAvatar {
			s.relayAvatarImage(w, r, h, token, path)
		} else if layoutPhoto {
			s.meetingPhoto(w, r, h, token, "MEETING_LAYOUT_PHOTO_NOT_FOUND", "MEETING_LAYOUT_PHOTOS_DISABLED")
		} else {
			s.meetingRoomPhoto(w, r, h, token)
		}
		return
	}
	var output any
	var body []byte
	var usagePage contracts.ReadPageSelection
	switch {
	case path == "/api/meetings/options":
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองไม่ถูกต้อง")
			return
		}
		output = &contracts.MeetingOptions{}
	case path == "/api/meetings/usage":
		var queryErr error
		var rest url.Values
		usagePage, rest, queryErr = contracts.ReadPagination(r.URL.RawQuery)
		if _, err := contracts.MeetingUsageQuery(rest.Encode()); queryErr != nil || err != nil {
			fail(w, 400, "INVALID_REQUEST", "กรุณาระบุวันที่ดูการใช้ห้องประชุมให้ถูกต้อง")
			return
		}
		output = &contracts.MeetingUsage{}
	case path == "/api/meetings/availability":
		if _, err := contracts.MeetingAvailabilityQuery(r.URL.RawQuery); err != nil {
			fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบวัน เวลา และจำนวนผู้เข้าร่วม")
			return
		}
		output = &contracts.MeetingAvailability{}
	case path == "/api/meetings" && r.Method == http.MethodGet:
		if _, err := contracts.MeetingHistoryQuery(r.URL.RawQuery); err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกประวัติการจองไม่ถูกต้อง")
			return
		}
		output = &contracts.MeetingHistory{}
	default:
		if path != "/api/meetings" && !contracts.ValidMeetingID(id) {
			fail(w, 404, "NOT_FOUND", "ไม่พบรายการจอง")
			return
		}
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองไม่ถูกต้อง")
			return
		}
		output = &contracts.Meeting{}
		if r.Method != http.MethodGet {
			if strings.HasSuffix(path, "/cancel") {
				var input contracts.MeetingCancel
				if !decodeMeeting(w, r, &input) {
					return
				}
				if !contracts.ValidMeetingRequestID(input.RequestID) {
					meetingValidation(w, map[string]string{"request_id": "รหัสคำขอไม่ถูกต้อง"})
					return
				}
				body, _ = json.Marshal(input)
			} else {
				var input contracts.MeetingInput
				if !decodeMeeting(w, r, &input) {
					return
				}
				if fields := contracts.ValidateMeetingInput(&input); len(fields) > 0 {
					meetingValidation(w, fields)
					return
				}
				body, _ = json.Marshal(input)
			}
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
	if readEnvelope(response, output) != nil || !validMeetingOutput(output) {
		upstreamUnavailable(w)
		return
	}
	if usage, ok := output.(*contracts.MeetingUsage); ok && (usage.Date != r.URL.Query().Get("date") || !contracts.ValidReadPage(usage.ReadPageMetadata, len(usage.Items), usagePage)) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}

func meetingRoute(path string) (id, methods string) {
	if id, avatar := contracts.MeetingRoomOwnerAvatarID(path); avatar {
		return id, "GET"
	}
	if id, photo := contracts.MeetingLayoutPhotoID(path); photo {
		return id, "GET"
	}
	if id, photo := contracts.MeetingRoomPhotoID(path); photo {
		return id, "GET"
	}
	switch path {
	case "/api/meetings":
		return "", "GET, POST"
	case "/api/meetings/options", "/api/meetings/availability", "/api/meetings/usage":
		return "", "GET"
	}
	if !strings.HasPrefix(path, "/api/meetings/") {
		return "", ""
	}
	suffix := strings.TrimPrefix(path, "/api/meetings/")
	id = strings.TrimSuffix(suffix, "/cancel")
	if !contracts.ValidMeetingID(id) {
		return "", ""
	}
	if suffix == id+"/cancel" {
		return id, "POST"
	}
	if suffix == id {
		return id, "GET, PUT"
	}
	return "", ""
}

func validMeetingOutput(v any) bool {
	switch x := v.(type) {
	case *contracts.MeetingUsage:
		return contracts.ValidMeetingUsage(x)
	case *contracts.MeetingOptions:
		for _, layout := range x.Layouts {
			if !contracts.ValidMeetingLayoutPhotoURL(layout.ImageURL) {
				return false
			}
		}
		for _, room := range x.Rooms {
			if !contracts.ValidMeetingRoomPhotoURL(room.ImageURL) || !contracts.ValidMeetingRoomOwner(room.Owner) {
				return false
			}
		}
		return x.Rooms != nil && x.Layouts != nil && x.Equipment != nil && x.Urgencies != nil && len(x.Rooms) <= 1000 && len(x.Layouts) <= 1000 && len(x.Equipment) <= 1000 && len(x.Urgencies) <= 1000
	case *contracts.MeetingAvailability:
		for _, room := range x.Rooms {
			if !contracts.ValidMeetingRoomPhotoURL(room.ImageURL) || !contracts.ValidMeetingRoomOwner(room.Owner) {
				return false
			}
		}
		return x.Rooms != nil && len(x.Rooms) <= 1000 && x.CheckedAt != ""
	case *contracts.MeetingHistory:
		return x.Items != nil && x.Page > 0 && x.PerPage > 0 && x.PerPage <= 100 && len(x.Items) <= x.PerPage && x.TotalCount >= 0
	case *contracts.Meeting:
		return contracts.ValidMeetingID(x.ID) && x.Code != "" && x.Title != ""
	}
	return false
}
func decodeMeeting(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "JSON_REQUIRED", "กรุณาส่งข้อมูลเป็น application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "INVALID_REQUEST", "รูปแบบข้อมูลไม่ถูกต้อง")
		return false
	}
	return true
}
func meetingValidation(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบข้อมูลการจอง", "fields": fields}})
}
func (s *Server) relayMeetingError(w http.ResponseWriter, response *http.Response) {
	if response.StatusCode == 401 || response.StatusCode == 429 {
		s.relayError(w, response, false)
		return
	}
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&envelope)
	if response.StatusCode == 422 && envelope.Error.Code == "VALIDATION_FAILED" {
		fields := map[string]string{}
		for k := range envelope.Error.Fields {
			switch k {
			case "room_id", "title", "start_date", "end_date", "start_time", "end_time", "attendees", "phone", "details", "layout_id", "urgency_id", "equipment_ids", "request_id":
				fields[k] = meetingFieldMessage(k)
			}
		}
		meetingValidation(w, fields)
		return
	}
	known := map[string]struct {
		status  int
		message string
	}{"INVALID_REQUEST": {400, "ตัวเลือกการจองไม่ถูกต้อง"}, "FORBIDDEN": {403, "บัญชีนี้ไม่มีสิทธิ์ดำเนินการกับรายการจอง"}, "READ_ONLY": {403, "หน่วยงานยังไม่เปิดรับการจองผ่านมือถือ"}, "NOT_FOUND": {404, "ไม่พบรายการจองนี้"}, "SLOT_CONFLICT": {409, "ห้องนี้ถูกจองในช่วงเวลาที่เลือกแล้ว กรุณาเลือกห้องหรือเวลาใหม่"}, "REQUEST_ID_CONFLICT": {409, "รหัสคำขอนี้ถูกใช้กับข้อมูลอื่นแล้ว"}, "STATUS_CHANGED": {409, "สถานะรายการเปลี่ยนแล้ว กรุณาโหลดข้อมูลล่าสุด"}, "MEETING_UNAVAILABLE": {503, "เชื่อมต่อระบบจองห้องประชุมไม่ได้ กรุณาลองใหม่"}}
	if item, ok := known[envelope.Error.Code]; ok && response.StatusCode == item.status {
		fail(w, item.status, envelope.Error.Code, item.message)
		return
	}
	upstreamUnavailable(w)
}

func meetingFieldMessage(key string) string {
	switch key {
	case "room_id":
		return "ห้องนี้ไม่พร้อมให้จอง กรุณาเลือกห้องใหม่"
	case "attendees":
		return "จำนวนผู้เข้าร่วมต้องถูกต้องและไม่เกินความจุห้อง"
	case "start_date", "end_date", "start_time", "end_time":
		return "กรุณาตรวจสอบช่วงวันที่และเวลา โดยเวลาเริ่มต้องอยู่ในอนาคตและก่อนเวลาสิ้นสุด"
	case "phone":
		return "กรุณาระบุเบอร์ติดต่อ"
	case "equipment_ids":
		return "รายการอุปกรณ์เปลี่ยนแล้ว กรุณาเลือกใหม่"
	case "layout_id":
		return "กรุณาเลือกรูปแบบห้องใหม่"
	case "urgency_id":
		return "กรุณาเลือกระดับความเร่งด่วนใหม่"
	default:
		return "กรุณาตรวจสอบข้อมูลช่องนี้"
	}
}
