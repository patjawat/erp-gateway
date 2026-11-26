package gateway

import (
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicleFormOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกแบบฟอร์มไม่ถูกต้อง")
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, "/api/vehicles/form-options", token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayVehicleError(w, response)
		return
	}
	var value contracts.VehicleFormOptions
	if readEnvelope(response, &value) != nil || !contracts.ValidVehicleFormOptions(&value) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, &value)
}
