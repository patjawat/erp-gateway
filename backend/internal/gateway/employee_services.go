package gateway

import (
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) welfareHistory(w http.ResponseWriter, r *http.Request) {
	s.employeeServiceHistory(w, r, false)
}

func (s *Server) housingHistory(w http.ResponseWriter, r *http.Request) {
	s.employeeServiceHistory(w, r, true)
}

func (s *Server) employeeServiceHistory(w http.ResponseWriter, r *http.Request, housing bool) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	selection, err := contracts.EmployeeServiceQuery(r.URL.RawQuery, housing)
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกข้อมูลไม่ถูกต้อง")
		return
	}
	path := "/api/welfare?"
	if housing {
		path = "/api/housing?"
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, path+selection.Query(housing), token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayError(w, response, false)
		return
	}
	var result any
	if housing {
		var value contracts.HousingHistory
		if readEnvelope(response, &value) != nil || !contracts.ValidHousingHistory(value, selection) {
			upstreamUnavailable(w)
			return
		}
		result = value
	} else {
		var value contracts.WelfareHistory
		if readEnvelope(response, &value) != nil || !contracts.ValidWelfareHistory(value, selection) {
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
