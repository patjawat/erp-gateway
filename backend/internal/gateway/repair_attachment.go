package gateway

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) repairAttachment(w http.ResponseWriter, r *http.Request, h Hospital, token string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 404 || response.StatusCode == 503 {
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
			_ = json.Unmarshal(raw, &env)
			if response.StatusCode == 404 && env.Error.Code == "REPAIR_NOT_FOUND" {
				fail(w, 404, "REPAIR_NOT_FOUND", "ไม่พบไฟล์แนบ")
				return
			}
			if response.StatusCode == 503 && env.Error.Code == "REPAIR_ATTACHMENTS_DISABLED" {
				fail(w, 503, "REPAIR_ATTACHMENTS_DISABLED", "ยังไม่เปิดการดูไฟล์ผ่านมือถือ")
				return
			}
			upstreamUnavailable(w)
			return
		}
		s.relayRepairError(w, response)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 5*1024*1024+1))
	if err != nil || len(body) == 0 || len(body) > 5*1024*1024 {
		upstreamUnavailable(w)
		return
	}
	mime := http.DetectContentType(body)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
