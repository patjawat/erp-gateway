package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
)

func (s *Server) leaveAttachment(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/leaves/"), "/")
	if len(parts) != 3 || !contracts.ValidLeaveID(parts[0]) || parts[1] != "attachments" || len(parts[2]) != 32 || strings.Trim(parts[2], "0123456789abcdef") != "" || r.URL.RawQuery != "" {
		fail(w, 404, "NOT_FOUND", "ไม่พบไฟล์แนบ")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayLeaveError(w, response)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, contracts.MaxLeaveAttachmentBytes+1))
	typ := http.DetectContentType(body)
	if err != nil || len(body) == 0 || len(body) > contracts.MaxLeaveAttachmentBytes || (typ != "image/jpeg" && typ != "image/png" && typ != "application/pdf") || response.Header.Get("Content-Type") != typ {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Disposition", "attachment")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
