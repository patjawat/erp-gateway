package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) attendanceTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	kind := "summary"
	switch r.URL.Path {
	case "/api/attendance/team":
	case "/api/attendance/team/departments":
		kind = "departments"
	case "/api/attendance/team/members":
		kind = "members"
	default:
		fail(w, 404, "NOT_FOUND", "ไม่พบข้อมูลที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	selection, err := contracts.ParseAttendanceTeamSelection(r.URL.RawQuery, kind)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกเพื่อนร่วมทีมไม่ถูกต้อง")
		return
	}
	path := r.URL.Path
	if kind != "summary" {
		path += "?" + selection.Query()
	}
	resp, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayAttendanceTeamError(w, resp)
		return
	}
	var out any
	switch kind {
	case "summary":
		var v contracts.AttendanceTeamSummary
		if readEnvelope(resp, &v) != nil || !contracts.ValidAttendanceTeamSummary(v) {
			upstreamUnavailable(w)
			return
		}
		out = v
	case "departments":
		var v contracts.AttendanceTeamDepartmentsPage
		if readEnvelope(resp, &v) != nil || !contracts.ValidAttendanceTeamDepartmentsPage(v, selection) {
			upstreamUnavailable(w)
			return
		}
		out = v
	case "members":
		var v contracts.AttendanceTeamMembersPage
		if readEnvelope(resp, &v) != nil || !contracts.ValidAttendanceTeamMembersPage(v, selection) {
			upstreamUnavailable(w)
			return
		}
		out = v
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	writeData(w, out)
}

func (s *Server) relayAttendanceTeamError(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	var env struct {
		Error contracts.Failure `json:"error"`
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &env) != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode == 401 && env.Error.Code != "GATEWAY_UNAUTHORIZED" {
		expired(w)
		return
	}
	known := map[string]struct {
		status  int
		message string
	}{
		"ATTENDANCE_TEAM_FORBIDDEN":   {403, "ไม่มีสิทธิ์ดูเพื่อนร่วมทีมในหน่วยงานนี้"},
		"ATTENDANCE_TEAM_CHANGED":     {409, "ขอบเขตทีมหรือวันที่เปลี่ยนแล้ว กรุณาโหลดใหม่"},
		"ATTENDANCE_TEAM_UNAVAILABLE": {503, "โหลดเพื่อนร่วมทีมไม่ได้ กรุณาลองใหม่"},
		"INVALID_REQUEST":             {400, "ตัวเลือกเพื่อนร่วมทีมไม่ถูกต้อง"},
	}
	if v, ok := known[env.Error.Code]; ok && v.status == resp.StatusCode {
		fail(w, v.status, env.Error.Code, v.message)
		return
	}
	upstreamUnavailable(w)
}
