package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
)

func (s *Server) repairUpload(w http.ResponseWriter, r *http.Request, h Hospital, token string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	var v contracts.RepairUpload
	if !decodeLimit(w, r, &v, contracts.MaxRepairUploadJSON) {
		return
	}
	if contracts.ValidateRepairUpload(&v) != nil {
		fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบไฟล์ภาพ")
		return
	}
	body, _ := json.Marshal(v)
	response, err := s.upstream(r.Context(), h, http.MethodPost, r.URL.Path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 409 || response.StatusCode == 503 {
			defer response.Body.Close()
			var env struct {
				Error contracts.Failure `json:"error"`
			}
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 16384))
			_ = json.Unmarshal(raw, &env)
			for _, e := range []struct {
				status    int
				code, msg string
			}{
				{409, "UPLOAD_CONFLICT", "ไฟล์สำหรับคำขอนี้เปลี่ยนแปลง กรุณาเลือกใหม่"},
				{409, "UPLOAD_LIMIT", "แนบรูปได้ไม่เกิน 10 รูปต่อรายการ"},
				{409, "REPAIR_STATE_CHANGED", "สถานะหรือรูปแบบรายการไม่รองรับการแนบรูป"},
				{503, "REPAIR_UPLOAD_DISABLED", "ยังไม่เปิดการแนบรูปผ่านมือถือ"},
				{503, "REPAIR_DISABLED", "ยังไม่เปิดการแนบรูปผ่านมือถือ"},
			} {
				if response.StatusCode == e.status && env.Error.Code == e.code {
					fail(w, e.status, e.code, e.msg)
					return
				}
			}
			upstreamUnavailable(w)
			return
		}
		if response.StatusCode == 404 {
			defer response.Body.Close()
			fail(w, 404, "REPAIR_NOT_FOUND", "ไม่พบรายการแจ้งซ่อม")
			return
		}
		s.relayRepairError(w, response)
		return
	}
	var value contracts.RepairAttachment
	if readEnvelopeLimit(response, &value, 16384) != nil || !repairPathID.MatchString(value.ID) || value.FileName == "" || (value.MimeType != "image/jpeg" && value.MimeType != "image/png") || value.DownloadURL != strings.TrimSuffix(r.URL.Path, "/attachments")+"/attachments/"+value.ID {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}
