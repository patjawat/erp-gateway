package gateway

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) leaveCalendar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	day := r.URL.Path == "/api/leaves/calendar/day"
	departments := r.URL.Path == "/api/leaves/calendar/departments"
	selection, err := contracts.ParseLeaveCalendarSelection(r.URL.RawQuery, day || departments)
	if departments {
		selection, err = contracts.ParseLeaveCalendarDepartmentsSelection(r.URL.RawQuery)
	}
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ช่วงวันที่ปฏิทินไม่ถูกต้อง")
		return
	}
	query := url.Values{"month": {selection.Date.Format("2006-01")}}
	if day || departments {
		query = url.Values{"date": {selection.Date.Format(time.DateOnly)}, "page": {strconv.Itoa(selection.Page)}, "per_page": {strconv.Itoa(selection.PerPage)}}
		if selection.DepartmentID != "" {
			query.Set("department_id", selection.DepartmentID)
		}
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path+"?"+query.Encode(), token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayLeaveError(w, response)
		return
	}
	var output any
	if departments {
		var value contracts.LeaveCalendarDepartmentsPage
		if readEnvelope(response, &value) != nil || !contracts.ValidLeaveCalendarDepartmentsPage(value, selection) {
			upstreamUnavailable(w)
			return
		}
		output = value
	} else if day {
		var value contracts.LeaveCalendarPage
		if readEnvelope(response, &value) != nil || !contracts.ValidLeaveCalendarPage(value, selection) {
			upstreamUnavailable(w)
			return
		}
		output = value
	} else {
		var value contracts.LeaveCalendar
		if readEnvelope(response, &value) != nil || !contracts.ValidLeaveCalendar(value, selection) {
			upstreamUnavailable(w)
			return
		}
		output = value
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, output)
}
