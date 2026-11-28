package gateway

import (
	"bytes"
	"net/http"
	"time"
)

// backendControl exposes only authenticated connection identity. Business
// capability schemas continue to belong to the selected ERP/Go upstream.
func (s *Server) backendControl(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "คำขอไม่ถูกต้อง")
		return
	}
	if !s.limit(w, r, "backend-control:"+h.ID+":"+tokenHash(token), 120, time.Minute) {
		return
	}
	path, err := s.directoryUpstreamPath(r.Context(), h)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	backend := "go"
	if path == erpDirectoryPath {
		backend = "erp_api_module"
	} else if path != goDirectoryPath {
		upstreamUnavailable(w)
		return
	}
	current, currentToken, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if current.ID != h.ID || current.APIURL != h.APIURL || !bytes.Equal(current.key, h.key) || currentToken != token {
		upstreamUnavailable(w)
		return
	}
	writeData(w, map[string]string{"backend": backend, "hospital_id": h.ID, "hospital_code": h.Code})
}
