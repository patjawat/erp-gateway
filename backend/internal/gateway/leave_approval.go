package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"erp-platform/gateway/internal/contracts"
)

var leaveApprovalRoute = regexp.MustCompile(`^/api/leaves/approvals/([1-9][0-9]{0,18})(?:/(decision|attachments/([a-f0-9]{32})))?$`)

func isLeaveApprovalPath(path string) bool {
	return path == "/api/leaves/approvals" || strings.HasPrefix(path, "/api/leaves/approvals/")
}

func (s *Server) leaveApproval(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	list := path == "/api/leaves/approvals"
	summary := path == "/api/leaves/approvals/summary"
	match := leaveApprovalRoute.FindStringSubmatch(path)
	if !list && !summary && match == nil {
		fail(w, 404, "NOT_FOUND", "ไม่พบรายการพิจารณาใบลา")
		return
	}
	method := http.MethodGet
	if match != nil && match[2] == "decision" {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับวิธีเรียกนี้")
		return
	}
	page := 1
	if list {
		var err error
		page, err = contracts.ParseLeaveApprovalPage(r.URL.RawQuery)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรายการไม่ถูกต้อง")
			return
		}
		path += "?page=" + strconv.Itoa(page)
	} else if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรายการไม่ถูกต้อง")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if !s.limit(w, r, "leave-review:"+h.ID+":"+tokenHash(token), 90, time.Minute) {
		return
	}
	var body []byte
	if method == http.MethodPost {
		var input contracts.LeaveApprovalDecision
		if !decodeLimit(w, r, &input, 16384) {
			return
		}
		if contracts.ValidateLeaveApprovalDecision(&input) != nil {
			fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบผลการพิจารณาและเหตุผล")
			return
		}
		body, _ = json.Marshal(input)
	}
	resp, err := s.upstream(r.Context(), h, method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayLeaveApprovalError(w, resp)
		return
	}
	if match != nil && match[3] != "" {
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, contracts.MaxLeaveAttachmentBytes+1))
		typ := http.DetectContentType(raw)
		if err != nil || len(raw) == 0 || len(raw) > contracts.MaxLeaveAttachmentBytes || (typ != "application/pdf" && typ != "image/png" && typ != "image/jpeg") || resp.Header.Get("Content-Type") != typ {
			upstreamUnavailable(w)
			return
		}
		if !s.stillActive(w, r, h) {
			return
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store, private")
		w.WriteHeader(200)
		_, _ = w.Write(raw)
		return
	}
	var value any
	valid := false
	switch {
	case summary:
		var result contracts.LeaveApprovalSummary
		valid = readEnvelopeLimit(resp, &result, 8192) == nil && result.PendingCount >= 0
		value = result
	case list:
		var result contracts.LeaveApprovalList
		valid = readEnvelopeLimit(resp, &result, 512*1024) == nil && result.Items != nil && result.Page == page && result.PerPage == contracts.LeaveApprovalPageSize && len(result.Items) <= result.PerPage && result.TotalCount >= len(result.Items) && result.HasMore == (result.Page*result.PerPage < result.TotalCount)
		seen := map[string]bool{}
		for _, item := range result.Items {
			if !contracts.ValidLeaveApprovalItem(item) || item.ApprovalStatus != "Pending" || !contracts.PendingLeaveApprovalStatus(item.Status) || seen[item.ID] {
				valid = false
			}
			seen[item.ID] = true
		}
		value = result
	default:
		var result contracts.LeaveApprovalDetail
		valid = readEnvelopeLimit(resp, &result, 1024*1024) == nil && contracts.ValidLeaveApprovalItem(result.LeaveApprovalItem) && result.ID == match[1] && result.Leave.ID == result.ID && result.Status == result.Leave.Status && result.TotalDays == result.Leave.TotalDays && result.StartDate == result.Leave.StartDate && result.EndDate == result.Leave.EndDate && validLeaveOutput(&result.Leave)
		if method == http.MethodPost {
			var input contracts.LeaveApprovalDecision
			_ = json.Unmarshal(body, &input)
			valid = valid && result.ApprovalID == input.ApprovalID && !result.CanDecide && result.ApprovalStatus == map[string]string{"approve": "Pass", "reject": "Reject"}[input.Decision]
		}
		value = result
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

func (s *Server) relayLeaveApprovalError(w http.ResponseWriter, resp *http.Response) {
	if resp.StatusCode == 401 || resp.StatusCode == 429 {
		s.relayError(w, resp, false)
		return
	}
	defer resp.Body.Close()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&envelope)
	known := map[string]struct {
		status int
		text   string
	}{
		"INVALID_REQUEST":            {400, "กรุณาตรวจสอบผลการพิจารณาและเหตุผล"},
		"FORBIDDEN":                  {403, "คุณไม่มีสิทธิ์พิจารณารายการนี้"},
		"LEAVE_APPROVAL_DISABLED":    {403, "หน่วยงานยังไม่เปิดการพิจารณาใบลาผ่านมือถือ"},
		"NOT_FOUND":                  {404, "ไม่พบรายการ หรือคุณไม่มีสิทธิ์พิจารณา"},
		"LEAVE_APPROVAL_CHANGED":     {409, "รายการเปลี่ยนแปลงแล้ว กรุณาตรวจสอบข้อมูลล่าสุดก่อนพิจารณา"},
		"LEAVE_APPROVAL_UNAVAILABLE": {503, "ระบบพิจารณาใบลายังไม่พร้อมใช้งาน กรุณาลองใหม่"},
	}
	if item, ok := known[envelope.Error.Code]; ok && resp.StatusCode == item.status {
		fail(w, item.status, envelope.Error.Code, item.text)
		return
	}
	upstreamUnavailable(w)
}
