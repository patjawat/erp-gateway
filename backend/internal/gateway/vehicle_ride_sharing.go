package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"net/http"
)

func (s *Server) vehicleRideSharing(w http.ResponseWriter, r *http.Request, id, request string) {
	methods := "GET, POST"
	if request != "" {
		methods = "PUT"
	}
	if (request != "" && r.Method != http.MethodPut) || (request == "" && r.Method != http.MethodGet && r.Method != http.MethodPost) {
		w.Header().Set("Allow", methods)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var output any
	var body []byte
	var input contracts.VehicleRideInput
	var q contracts.VehicleSelection
	if r.Method == http.MethodGet {
		var err error
		q, err = contracts.VehicleTravelersQuery(r.URL.RawQuery)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกคำขอไปด้วยไม่ถูกต้อง")
			return
		}
		output = &contracts.VehicleRideSharing{}
	} else {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกคำขอไปด้วยไม่ถูกต้อง")
			return
		}
		if !decodeMeeting(w, r, &input) {
			return
		}
		_, _, valid := contracts.VehicleRideRoute("/api/vehicles/" + id + "/ride-sharing/" + input.RequestID)
		if (request == "" && (!valid || input.Decision != "")) || (request != "" && (input.RequestID != "" || (input.Decision != "approved" && input.Decision != "rejected"))) {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลคำขอไปด้วยไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(input)
		output = &contracts.VehicleRideRequest{}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.upstream(r.Context(), h, r.Method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayVehicleError(w, response)
		return
	}
	if readEnvelope(response, output) != nil {
		upstreamUnavailable(w)
		return
	}
	valid := false
	if r.Method == http.MethodGet {
		valid = contracts.ValidVehicleRideSharing(output.(*contracts.VehicleRideSharing), id, q.Page, q.PerPage)
		valid = valid && !output.(*contracts.VehicleRideSharing).SummaryOnly
	} else {
		item := output.(*contracts.VehicleRideRequest)
		valid = contracts.ValidVehicleRideRequest(item)
		if request != "" {
			valid = valid && item.ID == request && item.Status == input.Decision
		}
	}
	if !valid {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}
