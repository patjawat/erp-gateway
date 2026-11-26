package contracts

import (
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// A mission is an assigned vehicle_detail row, not the parent booking. A
// multi-day booking can therefore have several mileage records to complete.
type VehicleDriverMission struct {
	ID              string                  `json:"id"`
	BookingID       string                  `json:"booking_id"`
	DriverID        string                  `json:"driver_id"`
	Code            string                  `json:"code"`
	Destination     string                  `json:"destination"`
	Purpose         string                  `json:"purpose"`
	StartDate       string                  `json:"start_date"`
	EndDate         string                  `json:"end_date"`
	StartTime       string                  `json:"start_time"`
	EndTime         string                  `json:"end_time"`
	LicensePlate    string                  `json:"license_plate"`
	VehicleName     string                  `json:"vehicle_name"`
	VehicleImageURL string                  `json:"vehicle_image_url"`
	MileageStart    *float64                `json:"mileage_start"`
	MileageEnd      *float64                `json:"mileage_end"`
	DistanceKM      *float64                `json:"distance_km"`
	OilPrice        *float64                `json:"oil_price"`
	OilLiter        *float64                `json:"oil_liter"`
	Status          string                  `json:"status"`
	StatusLabel     string                  `json:"status_label"`
	Receipts        []VehicleMissionReceipt `json:"receipts"`
	CanEdit         bool                    `json:"can_edit"`
}

type VehicleDriverMissions struct {
	Items             []VehicleDriverMission `json:"items"`
	Page              int                    `json:"page"`
	PerPage           int                    `json:"per_page"`
	TotalCount        int                    `json:"total_count"`
	HasMore           bool                   `json:"has_more"`
	StatusOptions     []VehicleMissionStatus `json:"status_options"`
	CanEditDistance   bool                   `json:"can_edit_distance"`
	CanEditFuel       bool                   `json:"can_edit_fuel"`
	CanEditStatus     bool                   `json:"can_edit_status"`
	CanUploadReceipts bool                   `json:"can_upload_receipts"`
	MaxReceipts       int                    `json:"max_receipts"`
	MaxReceiptBytes   int                    `json:"max_receipt_bytes"`
}

type VehicleDriverMissionInput struct {
	MileageStart *float64 `json:"mileage_start"`
	MileageEnd   *float64 `json:"mileage_end"`
	DistanceKM   *float64 `json:"distance_km,omitempty"`
	OilPrice     *float64 `json:"oil_price,omitempty"`
	OilLiter     *float64 `json:"oil_liter,omitempty"`
	Status       *string  `json:"status,omitempty"`
	RequestID    string   `json:"request_id,omitempty"`
	ReceiptIDs   []string `json:"receipt_ids,omitempty"`
}

const MaxVehicleMissionReceipts = 10
const MaxVehicleMissionReceiptBytes = MaxRepairUploadBytes

var vehicleReceiptIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var vehicleMissionRequestPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)

type VehicleMissionStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type VehicleMissionReceipt struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	Size     int    `json:"size"`
	URL      string `json:"url"`
}

func VehicleMissionReceiptURL(driver, mission, id string) string {
	return "/api/vehicles/drivers/" + driver + "/missions/" + mission + "/receipts/" + id
}

func ValidVehicleMissionReceipt(v VehicleMissionReceipt, driver, mission string) bool {
	return vehicleReceiptIDPattern.MatchString(v.ID) && v.FileName != "" && len(v.FileName) <= 400 && (v.MimeType == "image/jpeg" || v.MimeType == "image/png") && v.Size > 0 && v.Size <= MaxVehicleMissionReceiptBytes && v.URL == VehicleMissionReceiptURL(driver, mission, v.ID)
}

// Receipt paths deliberately use a separate parser so legacy mission routes
// keep their existing method and shape semantics.
func VehicleMissionReceiptRoute(path string) (driver, mission, receipt string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/vehicles/drivers/"), "/")
	if !strings.HasPrefix(path, "/api/vehicles/drivers/") || (len(parts) != 4 && len(parts) != 5) || parts[1] != "missions" || parts[3] != "receipts" || !ValidMeetingID(parts[0]) || !ValidMeetingID(parts[2]) {
		return
	}
	if len(parts) == 5 {
		if !vehicleReceiptIDPattern.MatchString(parts[4]) {
			return
		}
		receipt = parts[4]
	}
	return parts[0], parts[2], receipt, true
}

func VehicleDriverMissionRoute(path string) (driver, id string, ok bool) {
	const prefix = "/api/vehicles/drivers/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if (len(parts) != 2 && len(parts) != 3) || !ValidMeetingID(parts[0]) || parts[1] != "missions" {
		return "", "", false
	}
	if len(parts) == 3 {
		if !ValidMeetingID(parts[2]) {
			return "", "", false
		}
		id = parts[2]
	}
	return parts[0], id, true
}

func VehicleDriverMissionsQuery(raw string) (VehicleSelection, error) {
	out := VehicleSelection{Page: 1, PerPage: 50}
	q, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 2048 {
		return out, errors.New("invalid mission selection")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "page" && key != "per_page") {
			return out, errors.New("invalid mission selection")
		}
		n, err := strconv.Atoi(values[0])
		if err != nil || n < 1 || (key == "page" && n > 1000000) || (key == "per_page" && n > 100) {
			return out, errors.New("invalid mission page")
		}
		if key == "page" {
			out.Page = n
		} else {
			out.PerPage = n
		}
	}
	return out, nil
}

func ValidateVehicleDriverMissionInput(input *VehicleDriverMissionInput) map[string]string {
	fields := map[string]string{}
	valid := func(value *float64, positive bool) bool {
		return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 9999999 && (!positive || *value > 0)
	}
	if !valid(input.MileageStart, false) {
		fields["mileage_start"] = "กรุณาระบุเลขไมล์ก่อนออกระหว่าง 0 ถึง 9,999,999"
	}
	if !valid(input.MileageEnd, true) {
		fields["mileage_end"] = "กรุณาระบุเลขไมล์หลังกลับมากกว่า 0 และไม่เกิน 9,999,999"
	}
	if len(fields) == 0 && *input.MileageEnd < *input.MileageStart {
		fields["mileage_end"] = "เลขไมล์หลังกลับต้องไม่น้อยกว่าเลขไมล์ก่อนออก"
	}
	for key, value := range map[string]*float64{"distance_km": input.DistanceKM, "oil_price": input.OilPrice, "oil_liter": input.OilLiter} {
		if value != nil && !valid(value, false) {
			fields[key] = "กรุณาระบุตัวเลขระหว่าง 0 ถึง 9,999,999"
		}
	}
	if input.Status != nil && (*input.Status == "" || strings.TrimSpace(*input.Status) != *input.Status || len(*input.Status) > 100 || strings.ContainsAny(*input.Status, "\x00\r\n")) {
		fields["status"] = "กรุณาเลือกสถานะจากรายการ"
	}
	if input.RequestID != "" && !vehicleMissionRequestPattern.MatchString(input.RequestID) {
		fields["request_id"] = "รหัสรายการไม่ถูกต้อง"
	}
	if len(input.ReceiptIDs) > MaxVehicleMissionReceipts || (len(input.ReceiptIDs) > 0 && input.RequestID == "") {
		fields["receipt_ids"] = "กรุณาตรวจสอบเอกสารแนบ"
	}
	seen := map[string]bool{}
	for _, id := range input.ReceiptIDs {
		if !vehicleReceiptIDPattern.MatchString(id) || seen[id] {
			fields["receipt_ids"] = "กรุณาตรวจสอบเอกสารแนบ"
		}
		seen[id] = true
	}
	return fields
}

func ValidVehicleDriverMission(item *VehicleDriverMission) bool {
	if !ValidMeetingID(item.ID) || !ValidMeetingID(item.BookingID) || !ValidMeetingID(item.DriverID) {
		return false
	}
	if !ValidVehiclePhotoURL(item.VehicleImageURL, "vehicle") {
		return false
	}
	for _, value := range []*float64{item.MileageStart, item.MileageEnd, item.DistanceKM, item.OilPrice, item.OilLiter} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 9999999) {
			return false
		}
	}
	if len(item.Receipts) > MaxVehicleMissionReceipts {
		return false
	}
	seen := map[string]bool{}
	for _, v := range item.Receipts {
		if !ValidVehicleMissionReceipt(v, item.DriverID, item.ID) || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	return !item.CanEdit || item.MileageEnd == nil || *item.MileageEnd == 0
}
