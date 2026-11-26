package gateway

import (
	"net/http"
	"strings"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) leaveVerificationAvatar(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/leaves/"), "/")
	if len(parts) != 4 || parts[1] != "verification" || parts[3] != "avatar" ||
		!contracts.ValidLeaveID(parts[0]) || !contracts.ValidLeaveID(parts[2]) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบรูปผู้ตรวจสอบ")
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
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกรูปผู้ตรวจสอบไม่ถูกต้อง")
		return
	}
	path := "/api/leaves/" + parts[0] + "/verification/" + parts[2] + "/avatar"
	s.relayAvatarImage(w, r, h, token, path)
}
