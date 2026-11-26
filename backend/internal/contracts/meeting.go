package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type MeetingOption struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	ImageURL string `json:"image_url,omitempty"`
}
type MeetingRoom struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Capacity  *int              `json:"capacity"`
	Location  string            `json:"location"`
	Equipment []string          `json:"equipment"`
	ImageURL  string            `json:"image_url,omitempty"`
	Owner     *MeetingRoomOwner `json:"owner"`
}
type MeetingOptions struct {
	CanWrite  bool            `json:"can_write"`
	Rooms     []MeetingRoom   `json:"rooms"`
	Layouts   []MeetingOption `json:"layouts"`
	Equipment []MeetingOption `json:"equipment"`
	Urgencies []MeetingOption `json:"urgencies"`
}
type MeetingAvailableRoom struct {
	MeetingRoom
	Available            bool   `json:"available"`
	UnavailabilityReason string `json:"unavailability_reason"`
}
type MeetingAvailability struct {
	CheckedAt string                 `json:"checked_at"`
	Rooms     []MeetingAvailableRoom `json:"rooms"`
}
type MeetingInput struct {
	RoomID       string   `json:"room_id"`
	Title        string   `json:"title"`
	StartDate    string   `json:"start_date"`
	EndDate      string   `json:"end_date"`
	StartTime    string   `json:"start_time"`
	EndTime      string   `json:"end_time"`
	Attendees    int      `json:"attendees"`
	Phone        string   `json:"phone"`
	Details      string   `json:"details"`
	LayoutID     string   `json:"layout_id"`
	UrgencyID    string   `json:"urgency_id"`
	EquipmentIDs []string `json:"equipment_ids"`
	RequestID    string   `json:"request_id"`
}
type Meeting struct {
	MeetingInput
	ID          string `json:"id"`
	Code        string `json:"code"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
	RoomName    string `json:"room_name"`
	CreatedAt   string `json:"created_at"`
	CanEdit     bool   `json:"can_edit"`
	CanCancel   bool   `json:"can_cancel"`
}
type MeetingHistory struct {
	Items      []Meeting `json:"items"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalCount int       `json:"total_count"`
	HasMore    bool      `json:"has_more"`
}
type MeetingSelection struct {
	Page    int
	PerPage int
	Status  string
	Q       string
}
type MeetingWindow struct {
	StartDate string
	EndDate   string
	StartTime string
	EndTime   string
	Attendees int
	ExcludeID string
}
type MeetingCancel struct {
	RequestID string `json:"request_id"`
}

var meetingRequestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
var meetingIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var meetingTimePattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func ValidMeetingID(s string) bool        { return meetingIDPattern.MatchString(s) }
func ValidMeetingRequestID(s string) bool { return meetingRequestPattern.MatchString(s) }

func MeetingRoomPhotoURL(id string) string {
	if !ValidMeetingID(id) {
		return ""
	}
	return "/api/meetings/rooms/" + id + "/photo"
}

func MeetingRoomPhotoID(path string) (string, bool) {
	const prefix, suffix = "/api/meetings/rooms/", "/photo"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, ValidMeetingID(id)
}

func ValidMeetingRoomPhotoURL(path string) bool {
	if path == "" {
		return true
	}
	_, valid := MeetingRoomPhotoID(path)
	return valid
}

func MeetingLayoutPhotoURL(id string) string {
	if !ValidMeetingID(id) {
		return ""
	}
	return "/api/meetings/layouts/" + id + "/photo"
}

func MeetingLayoutPhotoID(path string) (string, bool) {
	const prefix, suffix = "/api/meetings/layouts/", "/photo"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, ValidMeetingID(id)
}

func ValidMeetingLayoutPhotoURL(path string) bool {
	if path == "" {
		return true
	}
	_, valid := MeetingLayoutPhotoID(path)
	return valid
}
func meetingText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsRune(s, 0) && (!required || s != "")
}

// ValidateMeetingInput validates syntax only. Date freshness follows the durable
// idempotency lookup so retries after a lost response remain recoverable.
func ValidateMeetingInput(v *MeetingInput) map[string]string {
	f := map[string]string{}
	if v == nil {
		f["title"] = "กรุณาตรวจสอบข้อมูลการจอง"
		return f
	}
	for _, x := range []struct {
		p        *string
		key      string
		max      int
		required bool
	}{
		{&v.RoomID, "room_id", 255, true}, {&v.Title, "title", 255, true}, {&v.Phone, "phone", 100, true}, {&v.Details, "details", 4000, false}, {&v.LayoutID, "layout_id", 255, false}, {&v.UrgencyID, "urgency_id", 255, true}, {&v.RequestID, "request_id", 64, true},
	} {
		*x.p = strings.TrimSpace(*x.p)
		if !meetingText(*x.p, x.max, x.required) {
			f[x.key] = "กรุณาระบุข้อมูลให้ถูกต้อง"
		}
	}
	if !ValidMeetingRequestID(v.RequestID) {
		f["request_id"] = "รหัสคำขอไม่ถูกต้อง"
	}
	for k, msg := range ValidateMeetingWindow(MeetingWindow{StartDate: v.StartDate, EndDate: v.EndDate, StartTime: v.StartTime, EndTime: v.EndTime, Attendees: v.Attendees}) {
		f[k] = msg
	}
	if len(v.EquipmentIDs) > 100 {
		f["equipment_ids"] = "เลือกอุปกรณ์มากเกินไป"
	}
	seen := map[string]bool{}
	for i, s := range v.EquipmentIDs {
		s = strings.TrimSpace(s)
		v.EquipmentIDs[i] = s
		if !meetingText(s, 255, true) || seen[s] {
			f["equipment_ids"] = "รายการอุปกรณ์ไม่ถูกต้อง"
		}
		seen[s] = true
	}
	if v.EquipmentIDs == nil {
		v.EquipmentIDs = []string{}
	}
	sort.Strings(v.EquipmentIDs)
	return f
}
func ValidateMeetingWindow(v MeetingWindow) map[string]string {
	f := map[string]string{}
	for _, x := range []struct{ s, k string }{{v.StartDate, "start_date"}, {v.EndDate, "end_date"}} {
		if t, e := time.Parse(time.DateOnly, x.s); e != nil || t.Format(time.DateOnly) != x.s || t.Year() < 1900 || t.Year() > 2200 {
			f[x.k] = "กรุณาระบุวันที่ให้ถูกต้อง"
		}
	}
	if !meetingTimePattern.MatchString(v.StartTime) {
		f["start_time"] = "กรุณาระบุเวลาเริ่มให้ถูกต้อง"
	}
	if !meetingTimePattern.MatchString(v.EndTime) {
		f["end_time"] = "กรุณาระบุเวลาสิ้นสุดให้ถูกต้อง"
	}
	if len(f) == 0 && v.EndDate+" "+v.EndTime <= v.StartDate+" "+v.StartTime {
		f["end_time"] = "เวลาสิ้นสุดต้องหลังเวลาเริ่ม"
	}
	if v.Attendees < 1 || v.Attendees > 100000 {
		f["attendees"] = "จำนวนผู้เข้าร่วมต้องอยู่ระหว่าง 1 ถึง 100,000 คน"
	}
	if v.ExcludeID != "" && !ValidMeetingID(v.ExcludeID) {
		f["exclude_id"] = "รายการจองไม่ถูกต้อง"
	}
	return f
}
func MeetingHistoryQuery(raw string) (MeetingSelection, error) {
	s := MeetingSelection{Page: 1, PerPage: 50}
	q, e := url.ParseQuery(raw)
	if e != nil || len(raw) > 2048 {
		return s, errors.New("invalid meeting selection")
	}
	for k, v := range q {
		if len(v) != 1 {
			return s, errors.New("invalid meeting selection")
		}
		switch k {
		case "page", "per_page":
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || (k == "page" && n > 1000000) || (k == "per_page" && n > 100) {
				return s, errors.New("invalid meeting selection")
			}
			if k == "page" {
				s.Page = n
			} else {
				s.PerPage = n
			}
		case "status":
			s.Status = v[0]
			switch s.Status {
			case "", "all", "pending", "approved", "rejected", "cancelled", "unknown":
			default:
				return s, errors.New("invalid meeting status")
			}
		case "q":
			s.Q = strings.TrimSpace(v[0])
			if !meetingText(s.Q, 100, false) {
				return s, errors.New("invalid meeting search")
			}
		default:
			return s, errors.New("invalid meeting selection")
		}
	}
	return s, nil
}
func MeetingAvailabilityQuery(raw string) (MeetingWindow, error) {
	s := MeetingWindow{}
	q, e := url.ParseQuery(raw)
	if e != nil || len(raw) > 2048 {
		return s, errors.New("invalid meeting window")
	}
	for k, v := range q {
		if len(v) != 1 {
			return s, errors.New("invalid meeting window")
		}
		switch k {
		case "start_date":
			s.StartDate = v[0]
		case "end_date":
			s.EndDate = v[0]
		case "start_time":
			s.StartTime = v[0]
		case "end_time":
			s.EndTime = v[0]
		case "attendees":
			s.Attendees, e = strconv.Atoi(v[0])
			if e != nil {
				return s, e
			}
		case "exclude_id":
			s.ExcludeID = v[0]
		default:
			return s, errors.New("invalid meeting window")
		}
	}
	if len(ValidateMeetingWindow(s)) > 0 {
		return s, errors.New("invalid meeting window")
	}
	return s, nil
}
