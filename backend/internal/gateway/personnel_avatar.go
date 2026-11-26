package gateway

import (
	"net/http"
	"strings"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) personnelAvatar(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/personnel/"), "/")
	if len(parts) != 2 || parts[1] != "avatar" || !contracts.ValidLeaveID(parts[0]) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบรูปบุคลากร")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกรูปบุคลากรไม่ถูกต้อง")
		return
	}
	s.relayAvatarImage(w, r, h, token, "/api/personnel/"+parts[0]+"/avatar")
}
