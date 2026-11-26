package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var attendanceApprovalRoute = regexp.MustCompile(`^/api/attendance/approvals/([0-9]{1,20})(/decision)?$`)
var attendanceRevision = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *Server) attendanceApproval(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	list := path == "/api/attendance/approvals"
	match := attendanceApprovalRoute.FindStringSubmatch(path)
	if !list && match == nil {
		fail(w, 404, "NOT_FOUND", "ไม่พบข้อมูลที่ร้องขอ")
		return
	}
	method := "GET"
	if match != nil && match[2] != "" {
		method = "POST"
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
	page := 1
	if list {
		var err error
		page, err = contracts.AttendanceApprovalPage(r.URL.RawQuery)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกคำขอไม่ถูกต้อง")
			return
		}
		path += "?page=" + strconv.Itoa(page)
	} else if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกคำขอไม่ถูกต้อง")
		return
	}
	if !s.limit(w, r, "attendance-review:"+h.ID+":"+token, 60, time.Minute) {
		return
	}
	var body []byte
	if method == "POST" {
		var v contracts.AttendanceDecision
		if !decodeLimit(w, r, &v, 16384) {
			return
		}
		if contracts.ValidateAttendanceDecision(&v) != nil {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลการยืนยันไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(v)
	}
	resp, err := s.upstream(r.Context(), h, method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayAttendanceError(w, resp)
		return
	}
	var value any
	valid := false
	if list {
		var v contracts.AttendanceApprovalList
		valid = readEnvelopeLimit(resp, &v, 256*1024) == nil && v.Page == page && v.Items != nil && len(v.Items) <= contracts.AttendanceApprovalPageSize
		seen := map[string]bool{}
		for _, item := range v.Items {
			if !validAttendanceApproval(item) || seen[item.ID] || item.Status != "pending" {
				valid = false
			}
			seen[item.ID] = true
		}
		value = v
	} else {
		var v contracts.AttendanceApproval
		valid = readEnvelopeLimit(resp, &v, 32768) == nil && validAttendanceApproval(v) && v.ID == match[1]
		value = v
	}
	if !valid {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	writeData(w, value)
}
func validAttendanceApproval(v contracts.AttendanceApproval) bool {
	return attendanceNumericID.MatchString(v.ID) && attendanceNumericID.MatchString(v.RecordID) && v.EmployeeName != "" && len(v.EmployeeName) <= 2048 && len(v.Department) <= 2048 && validAttendanceTime(v.RecordedAt) && (v.CheckType == "in" || v.CheckType == "out" || v.CheckType == "scan") && attendanceStatus(v.Status) && len(v.Reason) <= 12000 && len(v.LocationName) <= 2048 && attendanceRevision.MatchString(v.Revision) && (!v.CanDecide || v.Status == "pending")
}

// Keep the approval prefix separate from the clock-in write dispatcher.
func isAttendanceApprovalPath(path string) bool {
	return path == "/api/attendance/approvals" || strings.HasPrefix(path, "/api/attendance/approvals/")
}
