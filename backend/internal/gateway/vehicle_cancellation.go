package gateway

import (
	"encoding/json"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicleCancellation(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	hospital, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการยกเลิกไม่ถูกต้อง")
		return
	}
	var input map[string]json.RawMessage
	if !decodeMeeting(w, r, &input) {
		return
	}
	if input == nil || len(input) != 0 {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการยกเลิกไม่ถูกต้อง")
		return
	}
	response, err := s.upstream(r.Context(), hospital, http.MethodPost, r.URL.Path, token, []byte("{}"))
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayVehicleError(w, response)
		return
	}
	var booking contracts.VehicleBooking
	if readEnvelope(response, &booking) != nil || booking.ID != id || booking.Status != "cancelled" || booking.CanCancel || !validVehicleBookingSummary(&booking) || len(booking.Travelers) != 0 || (booking.TravelersPage != nil && !contracts.ValidVehicleTravelers(booking.TravelersPage, id, 1, 20)) || (booking.RideSharing != nil && !contracts.ValidVehicleRideSharing(booking.RideSharing, id, 1, 20)) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, hospital) {
		return
	}
	writeData(w, &booking)
}
