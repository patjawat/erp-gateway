package gateway

import (
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) profilePersonal(w http.ResponseWriter, r *http.Request) {
	s.readProfileRecords(w, r, true)
}
func (s *Server) profileRecordHistory(w http.ResponseWriter, r *http.Request) {
	s.readProfileRecords(w, r, false)
}
func (s *Server) readProfileRecords(w http.ResponseWriter, r *http.Request, personal bool) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	q, err := contracts.ProfileRecordsQuery(r.URL.RawQuery)
	if (personal && r.URL.RawQuery != "") || (!personal && err != nil) {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกข้อมูลไม่ถูกต้อง")
		return
	}
	path := "/api/profile/personal"
	if !personal {
		path = "/api/profile/records?" + q.Query()
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayError(w, response, false)
		return
	}
	var result any
	if personal {
		var value contracts.ProfilePersonal
		if readEnvelope(response, &value) != nil || len(value.Fields) > 20 {
			upstreamUnavailable(w)
			return
		}
		result = value
	} else {
		var value contracts.ProfileRecords
		if readEnvelope(response, &value) != nil || !contracts.ValidProfileRecords(value, q) {
			upstreamUnavailable(w)
			return
		}
		result = value
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, result)
}
