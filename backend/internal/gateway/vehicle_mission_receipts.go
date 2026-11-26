package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicleMissionReceipts(w http.ResponseWriter, r *http.Request, driver, mission, id string) {
	method := http.MethodPost
	if id != "" {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกเอกสารไม่ถูกต้อง")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var body []byte
	if id == "" {
		var input contracts.RepairUpload
		if !decodeLimit(w, r, &input, contracts.MaxRepairUploadJSON) {
			return
		}
		if contracts.ValidateRepairUpload(&input) != nil {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลภาพไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(input)
	}
	response, err := s.upstream(r.Context(), h, method, r.URL.Path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayVehicleError(w, response)
		return
	}
	if id == "" {
		var value contracts.VehicleMissionReceipt
		if readEnvelopeLimit(response, &value, 16384) != nil || !contracts.ValidVehicleMissionReceipt(value, driver, mission) {
			upstreamUnavailable(w)
			return
		}
		if !s.stillActive(w, r, h) {
			return
		}
		writeData(w, value)
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, contracts.MaxVehicleMissionReceiptBytes+1))
	mime := http.DetectContentType(raw)
	if err != nil || len(raw) == 0 || len(raw) > contracts.MaxVehicleMissionReceiptBytes || (mime != "image/jpeg" && mime != "image/png") || response.Header.Get("Content-Type") != mime {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}
