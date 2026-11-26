package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
)

func (s *Server) vehicleTravelers(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	q, err := contracts.VehicleTravelersQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรายชื่อผู้เดินทางไม่ถูกต้อง")
		return
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayVehicleError(w, response)
		return
	}
	var out contracts.VehicleTravelers
	if readEnvelope(response, &out) != nil || !contracts.ValidVehicleTravelers(&out, id, q.Page, q.PerPage) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, out)
}
