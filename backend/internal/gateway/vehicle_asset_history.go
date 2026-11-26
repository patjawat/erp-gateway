package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
)

func (s *Server) vehicleAssetHistory(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	section, q, err := contracts.VehicleAssetHistoryQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกประวัติรถไม่ถูกต้อง")
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
	var out contracts.VehicleAssetHistory
	if readEnvelope(response, &out) != nil || !contracts.ValidVehicleAssetHistory(&out, id, section, q.Page, q.PerPage) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, out)
}
