package gateway

import (
	"encoding/json"
	"math"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicleDriverMissions(w http.ResponseWriter, r *http.Request, driver, id string) {
	method := http.MethodGet
	if id != "" {
		method = http.MethodPut
	}
	if id != "" && r.Method == http.MethodGet {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var body []byte
	var output any
	var input contracts.VehicleDriverMissionInput
	var selection contracts.VehicleSelection
	if id == "" {
		var err error
		selection, err = contracts.VehicleDriverMissionsQuery(r.URL.RawQuery)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรายการภารกิจไม่ถูกต้อง")
			return
		}
		output = &contracts.VehicleDriverMissions{}
	} else if method == http.MethodGet {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกภารกิจไม่ถูกต้อง")
			return
		}
		output = &contracts.VehicleDriverMission{}
	} else {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกรายการภารกิจไม่ถูกต้อง")
			return
		}
		if !decodeMeeting(w, r, &input) {
			return
		}
		if fields := contracts.ValidateVehicleDriverMissionInput(&input); len(fields) != 0 {
			writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบรายละเอียดภารกิจ", "fields": fields}})
			return
		}
		body, _ = json.Marshal(input)
		output = &contracts.VehicleDriverMission{}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.upstream(r.Context(), h, method, path, token, body)
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
	if id == "" {
		valid = validVehicleDriverMissions(output.(*contracts.VehicleDriverMissions), driver, selection)
	} else if method == http.MethodGet {
		item := output.(*contracts.VehicleDriverMission)
		valid = contracts.ValidVehicleDriverMission(item) && item.ID == id && item.DriverID == driver
	} else {
		item := output.(*contracts.VehicleDriverMission)
		valid = contracts.ValidVehicleDriverMission(item) && item.ID == id && item.DriverID == driver && !item.CanEdit && item.MileageStart != nil && item.MileageEnd != nil && math.Abs(*item.MileageStart-*input.MileageStart) < 0.000001 && math.Abs(*item.MileageEnd-*input.MileageEnd) < 0.000001
		for _, pair := range [][2]*float64{{input.DistanceKM, item.DistanceKM}, {input.OilPrice, item.OilPrice}, {input.OilLiter, item.OilLiter}} {
			if pair[0] != nil && (pair[1] == nil || math.Abs(*pair[0]-*pair[1]) > 0.000001) {
				valid = false
			}
		}
		if input.Status != nil && *input.Status != item.Status {
			valid = false
		}
		for _, id := range input.ReceiptIDs {
			found := false
			for _, receipt := range item.Receipts {
				if receipt.ID == id {
					found = true
					break
				}
			}
			if !found {
				valid = false
			}
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

func validVehicleDriverMissions(out *contracts.VehicleDriverMissions, driver string, selection contracts.VehicleSelection) bool {
	if len(out.StatusOptions) > 100 || (out.MaxReceipts != 0 && out.MaxReceipts != contracts.MaxVehicleMissionReceipts) || (out.MaxReceiptBytes != 0 && out.MaxReceiptBytes != contracts.MaxVehicleMissionReceiptBytes) {
		return false
	}
	statuses := map[string]bool{}
	for _, option := range out.StatusOptions {
		if option.ID == "" || len(option.ID) > 100 || len(option.Name) > 500 || statuses[option.ID] {
			return false
		}
		statuses[option.ID] = true
	}
	if out.Items == nil || out.Page != selection.Page || out.PerPage != selection.PerPage || out.TotalCount < 0 || len(out.Items) > selection.PerPage || out.HasMore != (out.Page*out.PerPage < out.TotalCount) {
		return false
	}
	expected := out.TotalCount - (out.Page-1)*out.PerPage
	if expected < 0 {
		expected = 0
	}
	if expected > out.PerPage {
		expected = out.PerPage
	}
	if len(out.Items) != expected {
		return false
	}
	seen := make(map[string]bool, len(out.Items))
	for _, item := range out.Items {
		if !contracts.ValidVehicleDriverMission(&item) || item.DriverID != driver || seen[item.ID] || (item.MileageEnd != nil && *item.MileageEnd != 0) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}
