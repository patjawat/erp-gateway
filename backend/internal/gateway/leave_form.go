package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

func (s *Server) leave(w http.ResponseWriter, r *http.Request) {
	if isLeaveApprovalPath(r.URL.Path) {
		s.leaveApproval(w, r)
		return
	}
	if strings.Contains(r.URL.Path, "/attachments/") {
		s.leaveAttachment(w, r)
		return
	}
	if r.URL.Path == "/api/leaves/calendar" || r.URL.Path == "/api/leaves/calendar/day" || r.URL.Path == "/api/leaves/calendar/departments" {
		s.leaveCalendar(w, r)
		return
	}
	if strings.Contains(r.URL.Path, "/verification/") {
		s.leaveVerificationAvatar(w, r)
		return
	}
	path := r.URL.Path
	id := ""
	method := http.MethodGet
	switch path {
	case "/api/leaves", "/api/leaves/preview":
		method = http.MethodPost
	case "/api/leaves/options":
	default:
		id = strings.TrimPrefix(path, "/api/leaves/")
		if !contracts.ValidLeaveID(id) {
			fail(w, 404, "NOT_FOUND", "ไม่พบใบลานี้")
			return
		}
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
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการลาไม่ถูกต้อง")
		return
	}
	var output any
	var body []byte
	switch path {
	case "/api/leaves/options":
		output = &contracts.LeaveOptions{}
	default:
		if id != "" {
			output = &contracts.Leave{}
		} else {
			var input contracts.LeaveInput
			if !decodeLimit(w, r, &input, contracts.MaxLeaveInputJSON) {
				return
			}
			preview := path == "/api/leaves/preview"
			if f := contracts.ValidateLeaveInput(&input, preview); len(f) > 0 {
				leaveValidation(w, f)
				return
			}
			body, _ = json.Marshal(input)
			if preview {
				output = &contracts.LeavePreview{}
			} else {
				output = &contracts.Leave{}
			}
		}
	}
	response, e := s.upstream(r.Context(), h, r.Method, path, token, body)
	if e != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayLeaveError(w, response)
		return
	}
	if readEnvelope(response, output) != nil || !validLeaveOutput(output) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}
func validLeaveOutput(v any) bool {
	switch x := v.(type) {
	case *contracts.LeaveOptions:
		if x.Types == nil || x.Balances == nil || len(x.Types) > 1000 || len(x.Balances) > 1000 || x.FiscalYear < 2443 || x.FiscalYear > 2744 {
			return false
		}
		ids := map[string]bool{}
		for _, t := range x.Types {
			if t.ID == "" || t.Name == "" || ids[t.ID] {
				return false
			}
			ids[t.ID] = true
		}
		for _, b := range x.Balances {
			if !ids[b.LeaveTypeID] || b.LeaveType == "" {
				return false
			}
			for _, days := range []*float64{b.EntitlementDays, b.UsedDays, b.RemainingDays} {
				if days != nil && (math.IsNaN(*days) || math.IsInf(*days, 0) || *days < 0) {
					return false
				}
			}
		}
		return true
	case *contracts.LeavePreview:
		if x.TotalDays <= 0 || math.IsNaN(x.TotalDays) || math.IsInf(x.TotalDays, 0) || x.FiscalYear < 2443 || x.FiscalYear > 2744 || x.Period == "" || x.CalendarDays < 1 || x.CalendarDays > 367 || x.WeekendDays < 0 || x.HolidayDays < 0 || x.WeekendDays+x.HolidayDays > x.CalendarDays || x.TotalDays > float64(x.CalendarDays) || x.Approvals == nil || len(x.Approvals) < 1 || len(x.Approvals) > 100 {
			return false
		}
		if x.HolidaysDeducted && x.TotalDays > float64(x.CalendarDays-x.WeekendDays-x.HolidayDays) {
			return false
		}
		previous := 0
		for _, approval := range x.Approvals {
			if approval.Level <= previous {
				return false
			}
			previous = approval.Level
			if approval.EmployeeID == "" {
				if approval.Role != "leave" || approval.EmployeeName != "" || x.AutoApproved {
					return false
				}
			} else if !contracts.ValidLeaveID(approval.EmployeeID) || strings.TrimSpace(approval.EmployeeName) == "" || approval.Role != "" {
				return false
			}
		}
		return true
	case *contracts.Leave:
		if len(x.Attachments) > contracts.MaxLeaveAttachments || x.AttachmentCount != len(x.Attachments) {
			return false
		}
		for _, file := range x.Attachments {
			if !contracts.ValidLeaveAttachment(file) {
				return false
			}
		}
		// Historical ERP rows may have no recorded type or a zero day total.
		// Keep those readable while validating the saved verification timeline.
		if !contracts.ValidLeaveID(x.ID) || x.StartDate == "" || x.EndDate == "" || x.TotalDays < 0 || math.IsNaN(x.TotalDays) || math.IsInf(x.TotalDays, 0) || len(x.VerificationSteps) > 100 || !validSavedLeaveFiscalYear(x.FiscalYear) {
			return false
		}
		previous := 0
		for _, step := range x.VerificationSteps {
			if !contracts.ValidLeaveID(step.ID) || step.Level < previous || step.Level < 0 || step.EmployeeID != "" && !contracts.ValidLeaveID(step.EmployeeID) {
				return false
			}
			previous = step.Level
		}
		if summary := x.DaySummary; summary != nil {
			start, startErr := time.Parse(time.DateOnly, x.StartDate)
			end, endErr := time.Parse(time.DateOnly, x.EndDate)
			if startErr != nil || endErr != nil || end.Before(start) || summary.CalendarDays < 1 || summary.CalendarDays > 3666 || summary.CalendarDays != int(end.Sub(start)/(24*time.Hour))+1 || summary.WeekendDays < 0 || summary.HolidayDays < 0 || summary.WeekendDays+summary.HolidayDays > summary.CalendarDays || summary.TotalDays < 0 || math.IsNaN(summary.TotalDays) || math.IsInf(summary.TotalDays, 0) || summary.TotalDays != x.TotalDays {
				return false
			}
		}
		return true
	}
	return false
}
func leaveValidation(w http.ResponseWriter, f map[string]string) {
	writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบข้อมูลการลา", "fields": f}})
}
func (s *Server) relayLeaveError(w http.ResponseWriter, response *http.Response) {
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
			case "leave_type_id":
				fields[k] = "กรุณาตรวจสอบประเภทและสิทธิ์การลา"
			case "start_date", "end_date", "start_time", "end_time":
				fields[k] = "กรุณาตรวจสอบช่วงวันที่ เวลา และสิทธิ์คงเหลือ"
			case "reason", "travel_scope", "comments":
				fields[k] = "กรุณาตรวจสอบรายละเอียดการลา"
			case "replacement_employee_id":
				fields[k] = "กรุณาเลือกผู้ปฏิบัติงานแทนใหม่"
			case "attachments":
				fields[k] = "กรุณาตรวจสอบไฟล์แนบ รองรับ JPG, PNG และ PDF ไฟล์ละไม่เกิน 5 MB สูงสุด 3 ไฟล์"
			case "request_id":
				fields[k] = "รหัสคำขอไม่ถูกต้อง"
			}
		}
		leaveValidation(w, fields)
		return
	}
	known := map[string]struct {
		status  int
		message string
	}{"INVALID_REQUEST": {400, "ตัวเลือกการลาไม่ถูกต้อง"}, "FORBIDDEN": {403, "บัญชีนี้ไม่มีข้อมูลบุคลากรหรือไม่มีสิทธิ์ดำเนินการ"}, "READ_ONLY": {403, "หน่วยงานยังไม่เปิดรับใบลาผ่านมือถือ"}, "NOT_FOUND": {404, "ไม่พบใบลานี้"}, "LEAVE_OVERLAP": {409, "ช่วงวันที่ลาซ้ำกับใบลาเดิม กรุณาตรวจสอบประวัติการลา"}, "REQUEST_ID_CONFLICT": {409, "รหัสคำขอนี้ถูกใช้กับข้อมูลอื่นแล้ว"}, "APPROVAL_UNAVAILABLE": {503, "กรุณาติดต่อผู้ดูแลเพื่อตรวจสอบลำดับอนุมัติใบลา"}, "LEAVE_UNAVAILABLE": {503, "เชื่อมต่อระบบการลาไม่ได้ กรุณาลองใหม่"}}
	if item, ok := known[envelope.Error.Code]; ok && response.StatusCode == item.status {
		fail(w, item.status, envelope.Error.Code, item.message)
		return
	}
	upstreamUnavailable(w)
}
