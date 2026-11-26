package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var repairPathID = regexp.MustCompile("^[1-9][0-9]{0,18}$")

func (s *Server) repairDetail(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/repairs/"), "/")
	if len(parts) == 2 && parts[1] == "attachments" && repairPathID.MatchString(parts[0]) && r.URL.RawQuery == "" {
		s.repairUpload(w, r, h, token)
		return
	}
	if len(parts) == 3 && parts[1] == "attachments" && repairPathID.MatchString(parts[0]) && repairPathID.MatchString(parts[2]) && r.URL.RawQuery == "" {
		s.repairAttachment(w, r, h, token)
		return
	}

	if len(parts) < 1 || len(parts) > 2 || !repairPathID.MatchString(parts[0]) || r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ข้อมูลแจ้งซ่อมไม่ถูกต้อง")
		return
	}
	method := "GET"
	if len(parts) == 2 {
		if parts[1] != "edit" && parts[1] != "feedback" {
			fail(w, 404, "NOT_FOUND", "ไม่พบรายการ")
			return
		}
		method = "POST"
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	var body []byte
	if method == "POST" {
		if parts[1] == "edit" {
			var v contracts.RepairEdit
			if !decodeLimit(w, r, &v, 64*1024) {
				return
			}
			if contracts.ValidateRepairEdit(&v) != nil {
				fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบข้อมูล")
				return
			}
			body, _ = json.Marshal(v)
		} else {
			var v contracts.RepairFeedbackInput
			if !decodeLimit(w, r, &v, 64*1024) {
				return
			}
			if contracts.ValidateRepairFeedback(&v) != nil {
				fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบคะแนน")
				return
			}
			body, _ = json.Marshal(v)
		}
	}
	response, err := s.upstream(r.Context(), h, method, r.URL.Path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 404 || response.StatusCode == 409 {
			defer response.Body.Close()
			var env struct {
				Error contracts.Failure `json:"error"`
			}
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
			_ = json.Unmarshal(raw, &env)
			if response.StatusCode == 404 && env.Error.Code == "REPAIR_NOT_FOUND" {
				fail(w, 404, "REPAIR_NOT_FOUND", "ไม่พบรายการแจ้งซ่อม")
				return
			}
			if response.StatusCode == 409 && env.Error.Code == "REPAIR_STATE_CHANGED" {
				fail(w, 409, "REPAIR_STATE_CHANGED", "สถานะงานเปลี่ยนแล้ว กรุณาโหลดข้อมูลใหม่")
				return
			}
			upstreamUnavailable(w)
			return
		}
		s.relayRepairError(w, response)
		return
	}
	var value contracts.RepairDetail
	if readEnvelopeLimit(response, &value, 8*1024*1024) != nil || value.Record.ID != parts[0] || value.Timeline == nil || value.Attachments == nil || len(value.Timeline) > 200 || len(value.Attachments) > 100 {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}
