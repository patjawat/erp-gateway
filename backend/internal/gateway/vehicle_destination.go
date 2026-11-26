package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicleDestinations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	path := "/api/vehicles/destinations"
	var body []byte
	var output any
	var page contracts.ReadPageSelection
	if r.Method == http.MethodGet {
		var pageErr error
		var rest url.Values
		page, rest, pageErr = contracts.ReadPagination(r.URL.RawQuery)
		query, err := contracts.VehicleDestinationQuery(rest.Encode())
		if pageErr != nil || err != nil {
			fail(w, 400, "INVALID_REQUEST", "คำค้นหาจุดหมายปลายทางไม่ถูกต้อง")
			return
		}
		values := url.Values{"q": {query}}
		if page.Explicit {
			original, _ := url.ParseQuery(r.URL.RawQuery)
			for _, key := range []string{"page", "per_page"} {
				if original.Has(key) {
					values.Set(key, original.Get(key))
				}
			}
		}
		path += "?" + values.Encode()
		output = &contracts.VehicleDestinationList{}
	} else {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "คำขอเพิ่มจุดหมายปลายทางไม่ถูกต้อง")
			return
		}
		var input contracts.VehicleDestinationInput
		if !decodeMeeting(w, r, &input) {
			return
		}
		if fields := contracts.ValidateVehicleDestinationInput(&input); len(fields) > 0 {
			writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบชื่อจุดหมายปลายทาง", "fields": fields}})
			return
		}
		body, _ = json.Marshal(input)
		output = &contracts.VehicleDestination{}
	}
	response, err := s.upstream(r.Context(), h, r.Method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayVehicleError(w, response)
		return
	}
	if readEnvelope(response, output) != nil {
		upstreamUnavailable(w)
		return
	}
	valid := false
	switch value := output.(type) {
	case *contracts.VehicleDestinationList:
		valid = contracts.ValidVehicleDestinations(value.Items) && contracts.ValidReadPage(value.ReadPageMetadata, len(value.Items), page)
	case *contracts.VehicleDestination:
		valid = contracts.ValidVehicleDestination(*value)
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
