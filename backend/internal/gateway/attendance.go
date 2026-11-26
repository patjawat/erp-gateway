package gateway

import (
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) attendanceHistory(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	month, err := contracts.AttendanceMonth(r.URL.RawQuery)
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกประวัติการลงเวลาไม่ถูกต้อง")
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, "/api/attendance?month="+url.QueryEscape(month), token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayError(w, response, false)
		return
	}
	var value contracts.AttendanceHistory
	if err = readEnvelope(response, &value); err != nil || !validAttendanceHistory(value, month) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}

func validAttendanceHistory(value contracts.AttendanceHistory, month string) bool {
	if value.Month != month || value.Items == nil || len(value.Items) > 31 {
		return false
	}
	seen, events := map[string]bool{}, 0
	for _, day := range value.Items {
		date, err := time.Parse(time.DateOnly, day.Date)
		if err != nil || date.Format("2006-01") != month || seen[day.Date] || len(day.Events) == 0 || !attendanceStatus(day.Status) ||
			(day.DurationMinutes != nil && (*day.DurationMinutes < 0 || *day.DurationMinutes > 48*60 || day.CheckIn == nil || day.CheckOut == nil)) {
			return false
		}
		seen[day.Date] = true
		for _, at := range []*string{day.CheckIn, day.CheckOut} {
			if at != nil {
				if _, err := time.Parse(time.RFC3339, *at); err != nil {
					return false
				}
			}
		}
		events += len(day.Events)
		if events > 2000 {
			return false
		}
		for _, event := range day.Events {
			if len(event.Reason) > 12000 || !utf8.ValidString(event.Reason) || event.ID == "" || len(event.ID) > 20 || strings.Trim(event.ID, "0123456789") != "" || !attendanceStatus(event.Status) ||
				(event.CheckType != "in" && event.CheckType != "out" && event.CheckType != "scan") {
				return false
			}
			if _, err := time.Parse(time.RFC3339, event.RecordedAt); err != nil {
				return false
			}
		}
	}
	return true
}

func attendanceStatus(value string) bool {
	return value == "approved" || value == "pending" || value == "rejected"
}
