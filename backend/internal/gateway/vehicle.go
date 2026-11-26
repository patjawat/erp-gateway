package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) vehicle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/vehicles/cmi" {
		s.vehicleCMIList(w, r)
		return
	}
	if id, valid := contracts.VehicleAssetHistoryRoute(r.URL.Path); valid {
		s.vehicleAssetHistory(w, r, id)
		return
	}
	if id, request, valid := contracts.VehicleRideRoute(r.URL.Path); valid {
		s.vehicleRideSharing(w, r, id, request)
		return
	}
	if id, valid := contracts.VehicleTravelersRoute(r.URL.Path); valid {
		s.vehicleTravelers(w, r, id)
		return
	}
	if driver, mission, receipt, valid := contracts.VehicleMissionReceiptRoute(r.URL.Path); valid {
		s.vehicleMissionReceipts(w, r, driver, mission, receipt)
		return
	}
	if driver, id, valid := contracts.VehicleDriverMissionRoute(r.URL.Path); valid {
		s.vehicleDriverMissions(w, r, driver, id)
		return
	}
	kind, id, valid := contracts.VehicleRoute(r.URL.Path)
	if !valid {
		fail(w, 404, "VEHICLE_NOT_FOUND", "ไม่พบคำขอใช้รถนี้")
		return
	}
	if kind == "destinations" {
		s.vehicleDestinations(w, r)
		return
	}
	if kind == "form_options" {
		s.vehicleFormOptions(w, r)
		return
	}
	if kind == "cancel" {
		s.vehicleCancellation(w, r, id)
		return
	}
	options := kind == "options"
	methods := "GET, POST"
	if kind != "list" {
		methods = "GET"
	}
	if r.Method != http.MethodGet && (kind != "list" || r.Method != http.MethodPost) {
		w.Header().Set("Allow", methods)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if kind == "detail" || kind == "photo_vehicle" || kind == "photo_driver" {
		_, queryErr := contracts.VehiclePagedTravelersDetail(r.URL.RawQuery)
		if queryErr != nil || (kind != "detail" && r.URL.RawQuery != "") {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองรถไม่ถูกต้อง")
			return
		}
		if kind != "detail" {
			s.vehiclePhoto(w, r, h, token)
			return
		}
	}
	var output any
	var body []byte
	var selection contracts.VehicleSelection
	var optionsPage contracts.ReadPageSelection
	if r.Method == http.MethodGet {
		var queryErr error
		query := r.URL.RawQuery
		if options {
			var rest url.Values
			optionsPage, rest, queryErr = contracts.ReadPagination(query)
			if queryErr != nil {
				fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองรถไม่ถูกต้อง")
				return
			}
			query = rest.Encode()
		}
		selection, queryErr = contracts.VehicleQuery(query, options)
		if queryErr != nil && kind != "detail" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองรถไม่ถูกต้อง")
			return
		}
		if kind == "detail" {
			output = &contracts.VehicleBooking{}
		} else if options {
			output = &contracts.VehicleOptions{}
		} else {
			output = &contracts.VehicleHistory{}
		}
	} else {
		if r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการจองรถไม่ถูกต้อง")
			return
		}
		var input contracts.VehicleInput
		if !decodeMeeting(w, r, &input) {
			return
		}
		if fields := contracts.ValidateVehicleInput(&input); len(fields) > 0 {
			writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบข้อมูลการจองรถ", "fields": fields}})
			return
		}
		body, _ = json.Marshal(input)
		output = &contracts.VehicleBooking{}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, e := s.upstream(r.Context(), h, r.Method, path, token, body)
	if e != nil {
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
	validOutput := validVehicleOutput(output)
	if options {
		validOutput = validOutput && validVehicleOptionPages(output.(*contracts.VehicleOptions), optionsPage)
	}
	if kind == "list" && r.Method == http.MethodGet {
		history := output.(*contracts.VehicleHistory)
		if selection.IncludeTypeCounts && history.TypeCounts == nil {
			validOutput = false
		}
		if history.TypeCounts != nil {
			var expected *int
			switch selection.VehicleType {
			case "central":
				expected = &history.TypeCounts.All
			case "", "official":
				expected = &history.TypeCounts.Official
			case "ambulance":
				expected = &history.TypeCounts.Ambulance
			}
			if expected != nil && history.TotalCount != *expected {
				validOutput = false
			}
		}
		for _, item := range history.Items {
			if (selection.Status != "" && item.Status != selection.Status) || (selection.ExcludeCancelled && item.Status == "cancelled") {
				validOutput = false
			}
		}
	}
	if kind == "detail" {
		validOutput = validVehicleBookingSummary(output.(*contracts.VehicleBooking))
		if paged, _ := contracts.VehiclePagedTravelersDetail(r.URL.RawQuery); paged {
			booking := output.(*contracts.VehicleBooking)
			validOutput = validOutput && booking.ID == id && len(booking.Travelers) == 0 && (booking.TravelersPage == nil || contracts.ValidVehicleTravelers(booking.TravelersPage, id, 1, 20))
			validOutput = validOutput && (booking.RideSharing == nil || contracts.ValidVehicleRideSharing(booking.RideSharing, id, 1, 20))
		}
	}
	if !validOutput {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}
func validVehicleOutput(v any) bool {
	switch x := v.(type) {
	case *contracts.VehicleOptions:
		if x.Vehicles == nil || x.Drivers == nil || len(x.Vehicles) > 1000 || len(x.Drivers) > 1000 {
			return false
		}
		for _, item := range x.Vehicles {
			if !contracts.ValidVehiclePhotoURL(item.ImageURL, "vehicle") || !contracts.ValidVehicleCMI(item) {
				return false
			}
		}
		vehicleTotal := len(x.Vehicles)
		if x.VehiclesPage != nil && x.VehiclesPage.ReadPageMetadata != nil && x.VehiclesPage.TotalCount != nil {
			vehicleTotal = *x.VehiclesPage.TotalCount
		}
		if x.CMISummary != nil {
			remaining := vehicleTotal
			for _, count := range []int{x.CMISummary.ExpiringCount, x.CMISummary.ExpiredCount, x.CMISummary.MissingCount} {
				if count < 0 || count > remaining {
					return false
				}
				remaining -= count
			}
		}
		for _, item := range x.Drivers {
			if !contracts.ValidVehiclePhotoURL(item.ImageURL, "driver") || (item.PendingMileageCount != nil && *item.PendingMileageCount < 0) {
				return false
			}
		}
		return true
	case *contracts.VehicleHistory:
		if x.Items == nil || x.Page < 1 || x.PerPage < 1 || x.PerPage > 100 || len(x.Items) > x.PerPage || x.TotalCount < 0 {
			return false
		}
		if x.TypeCounts != nil && !contracts.ValidVehicleTypeCounts(x.TypeCounts) {
			return false
		}
		for _, item := range x.Items {
			if !validVehicleBookingSummary(&item) {
				return false
			}
		}
		return true
	case *contracts.VehicleBooking:
		return contracts.ValidMeetingID(x.ID) && x.Code != "" && (x.Location != "" || (x.VehicleType == "ambulance" && x.RequestMode == "immediate")) && x.Reason != "" && contracts.ValidVehiclePhotoURL(x.VehicleImageURL, "vehicle") && contracts.ValidVehiclePhotoURL(x.DriverImageURL, "driver")
	}
	return false
}
func validVehicleBookingSummary(v *contracts.VehicleBooking) bool {
	return contracts.ValidMeetingID(v.ID) && contracts.ValidVehiclePhotoURL(v.VehicleImageURL, "vehicle") && contracts.ValidVehiclePhotoURL(v.DriverImageURL, "driver")
}

func validVehicleOptionPages(value *contracts.VehicleOptions, selection contracts.ReadPageSelection) bool {
	if value.VehiclesPage == nil && value.DriversPage == nil {
		return !selection.Explicit
	}
	if value.VehiclesPage == nil || value.DriversPage == nil {
		return false
	}
	return value.VehiclesPage.ReadPageMetadata != nil && value.DriversPage.ReadPageMetadata != nil && contracts.ValidReadPage(value.VehiclesPage.ReadPageMetadata, len(value.VehiclesPage.Items), selection) && contracts.ValidReadPage(value.DriversPage.ReadPageMetadata, len(value.DriversPage.Items), selection) && reflect.DeepEqual(value.Vehicles, value.VehiclesPage.Items) && reflect.DeepEqual(value.Drivers, value.DriversPage.Items) && validVehicleDirectoryPage(value.Vehicles) && validVehicleDirectoryPage(value.Drivers)
}

func validVehicleDirectoryPage(items []contracts.VehicleDirectoryItem) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if !contracts.ValidMeetingID(item.ID) || seen[item.ID] || item.Name == "" || len(item.Name) > 4000 || len(item.Detail) > 4000 {
			return false
		}
		switch item.Status {
		case "available", "busy", "unavailable":
		default:
			return false
		}
		seen[item.ID] = true
	}
	return true
}
func (s *Server) relayVehicleError(w http.ResponseWriter, r *http.Response) {
	if r.StatusCode == 401 || r.StatusCode == 429 {
		s.relayError(w, r, false)
		return
	}
	defer r.Body.Close()
	var env struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&env)
	if r.StatusCode == 422 && env.Error.Code == "VALIDATION_FAILED" {
		fields := map[string]string{}
		for k := range env.Error.Fields {
			switch k {
			case "mileage_start", "mileage_end", "request_id", "location", "reason", "start_date", "end_date", "start_time", "end_time", "phone", "passengers", "go_type", "purpose", "details", "pickup", "destinations", "travelers", "name", "requested_vehicle_id", "requested_driver_id", "vehicle_type", "personal_license_plate", "request_mode", "refer_type", "urgency", "contact_name", "patient_name", "patient_hn", "patient_age", "patient_symptom":
				fields[k] = "กรุณาตรวจสอบข้อมูลนี้"
			}
		}
		writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "กรุณาตรวจสอบข้อมูลการจองรถ", "fields": fields}})
		return
	}
	known := map[string]struct {
		status  int
		message string
	}{"INVALID_REQUEST": {400, "ตัวเลือกการจองรถไม่ถูกต้อง"}, "READ_ONLY": {403, "หน่วยงานยังไม่เปิดรับการจองรถผ่านมือถือ"}, "FORBIDDEN": {403, "บัญชีนี้ไม่มีข้อมูลบุคลากรหรือไม่มีสิทธิ์ดำเนินการ"}, "REQUEST_ID_CONFLICT": {409, "รหัสคำขอนี้ถูกใช้กับข้อมูลอื่นแล้ว"}, "VEHICLE_NOT_FOUND": {404, "ไม่พบคำขอใช้รถนี้"}, "VEHICLE_UNAVAILABLE": {503, "เชื่อมต่อระบบจองรถไม่ได้ กรุณาลองใหม่"}}
	known["VEHICLE_MISSION_CHANGED"] = struct {
		status  int
		message string
	}{409, "ภารกิจนี้มีการบันทึกหรือเปลี่ยนแปลงแล้ว กรุณาโหลดรายการใหม่"}
	known["VEHICLE_RIDE_CHANGED"] = struct {
		status  int
		message string
	}{409, "ทริปหรือคำขอเปลี่ยนแล้ว กรุณารีเฟรชรายละเอียด"}
	known["VEHICLE_CANCELLATION_CHANGED"] = struct {
		status  int
		message string
	}{409, "รายการนี้เริ่มดำเนินการหรือเปลี่ยนสถานะแล้ว กรุณาโหลดรายละเอียดใหม่"}
	if item, ok := known[env.Error.Code]; ok && item.status == r.StatusCode {
		fail(w, item.status, env.Error.Code, item.message)
		return
	}
	upstreamUnavailable(w)
}
