package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
)

func (s *Server) vehicleCMIList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	q, err := contracts.VehicleCMIQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวกรอง พ.ร.บ. ไม่ถูกต้อง")
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path+"?"+r.URL.RawQuery, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayVehicleError(w, response)
		return
	}
	var out contracts.VehicleCMIPage
	if readEnvelope(response, &out) != nil || !contracts.ValidVehicleCMIPage(&out, q) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, out)
}
