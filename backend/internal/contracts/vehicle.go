package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type VehicleTraveler struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Department string `json:"department"`
	IsExternal bool   `json:"is_external"`
}
type VehicleInput struct {
	RequestID    string            `json:"request_id"`
	Location     string            `json:"location"`
	Reason       string            `json:"reason"`
	StartDate    string            `json:"start_date"`
	EndDate      string            `json:"end_date"`
	StartTime    string            `json:"start_time"`
	EndTime      string            `json:"end_time"`
	Phone        string            `json:"phone"`
	Passengers   int               `json:"passengers"`
	GoType       int               `json:"go_type"`
	Purpose      string            `json:"purpose"`
	Details      string            `json:"details"`
	Pickup       string            `json:"pickup"`
	Destinations []string          `json:"destinations"`
	Wheelchair   bool              `json:"wheelchair"`
	Luggage      bool              `json:"luggage"`
	Travelers    []VehicleTraveler `json:"travelers"`
	// Requested resources are preferences; dispatch owns the actual allocation.
	// Omit blank fields so legacy retry payload hashes remain unchanged.
	RequestedVehicleID   string `json:"requested_vehicle_id,omitempty"`
	RequestedDriverID    string `json:"requested_driver_id,omitempty"`
	VehicleType          string `json:"vehicle_type,omitempty"`
	PersonalLicensePlate string `json:"personal_license_plate,omitempty"`
	RequestMode          string `json:"request_mode,omitempty"`
	ReferType            string `json:"refer_type,omitempty"`
	Urgency              string `json:"urgency,omitempty"`
	ContactName          string `json:"contact_name,omitempty"`
	PatientName          string `json:"patient_name,omitempty"`
	PatientHN            string `json:"patient_hn,omitempty"`
	PatientAge           string `json:"patient_age,omitempty"`
	PatientSymptom       string `json:"patient_symptom,omitempty"`
}
type VehicleBooking struct {
	VehicleInput
	ID                    string              `json:"id"`
	Code                  string              `json:"code"`
	Status                string              `json:"status"`
	StatusLabel           string              `json:"status_label"`
	CanCancel             bool                `json:"can_cancel"`
	RequesterID           string              `json:"requester_id"`
	RequesterName         string              `json:"requester_name"`
	ContactPhone          string              `json:"contact_phone,omitempty"`
	RequesterDepartment   string              `json:"requester_department,omitempty"`
	DriverPhone           string              `json:"driver_phone,omitempty"`
	DriverPosition        string              `json:"driver_position,omitempty"`
	LicensePlate          string              `json:"license_plate"`
	VehicleName           string              `json:"vehicle_name"`
	DriverID              string              `json:"driver_id"`
	DriverName            string              `json:"driver_name"`
	CreatedAt             string              `json:"created_at"`
	VehicleImageURL       string              `json:"vehicle_image_url"`
	DriverImageURL        string              `json:"driver_image_url"`
	TravelersPage         *VehicleTravelers   `json:"travelers_page,omitempty"`
	RideSharing           *VehicleRideSharing `json:"ride_sharing,omitempty"`
	RequestedVehicleName  string              `json:"requested_vehicle_name,omitempty"`
	RequestedLicensePlate string              `json:"requested_license_plate,omitempty"`
	RequestedDriverName   string              `json:"requested_driver_name,omitempty"`
	VehicleTypeLabel      string              `json:"vehicle_type_label,omitempty"`
	RequestModeLabel      string              `json:"request_mode_label,omitempty"`
	ReferTypeLabel        string              `json:"refer_type_label,omitempty"`
	UrgencyLabel          string              `json:"urgency_label,omitempty"`
}
type VehicleDirectoryItem struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	LicensePlate        string `json:"license_plate,omitempty"`
	Phone               string `json:"phone,omitempty"`
	Detail              string `json:"detail"`
	Status              string `json:"status"`
	StatusLabel         string `json:"status_label"`
	ImageURL            string `json:"image_url"`
	PendingMileageCount *int   `json:"pending_mileage_count,omitempty"`
	CMIExpiryDate       string `json:"cmi_expiry_date,omitempty"`
	CMIStatus           string `json:"cmi_status,omitempty"`
}
type VehicleCMISummary struct {
	ExpiringCount int `json:"expiring_count"`
	ExpiredCount  int `json:"expired_count"`
	MissingCount  int `json:"missing_count"`
}
type VehicleOptions struct {
	Vehicles   []VehicleDirectoryItem `json:"vehicles"`
	Drivers    []VehicleDirectoryItem `json:"drivers"`
	CanWrite   bool                   `json:"can_write"`
	CMISummary *VehicleCMISummary     `json:"cmi_summary,omitempty"`
}
type VehicleHistory struct {
	Items      []VehicleBooking   `json:"items"`
	Page       int                `json:"page"`
	PerPage    int                `json:"per_page"`
	TotalCount int                `json:"total_count"`
	HasMore    bool               `json:"has_more"`
	CanWrite   bool               `json:"can_write"`
	TypeCounts *VehicleTypeCounts `json:"type_counts,omitempty"`
}

// VehicleTypeCounts summarizes central requests before the selected fleet tab.
// Personal vehicles are deliberately excluded from all three totals.
type VehicleTypeCounts struct {
	All       int `json:"all"`
	Official  int `json:"official"`
	Ambulance int `json:"ambulance"`
}

func ValidVehicleTypeCounts(value *VehicleTypeCounts) bool {
	return value != nil && value.All >= 0 && value.Official >= 0 && value.Ambulance >= 0 && value.Official <= value.All && value.All-value.Official == value.Ambulance
}

type VehicleSelection struct {
	Page, PerPage     int
	Scope, Date       string
	DateFrom          string
	DateTo            string
	VehicleType       string
	ExcludeCancelled  bool
	Status            string
	IncludeTypeCounts bool
}

func VehicleRoute(path string) (kind, id string, ok bool) {
	if driver, mission, valid := VehicleDriverMissionRoute(path); valid {
		if mission == "" {
			return "driver_missions", driver, true
		}
		return "driver_mission", driver + "/" + mission, true
	}
	switch path {
	case "/api/vehicles":
		return "list", "", true
	case "/api/vehicles/options":
		return "options", "", true
	case "/api/vehicles/destinations":
		return "destinations", "", true
	case "/api/vehicles/form-options":
		return "form_options", "", true
	}
	const prefix = "/api/vehicles/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 1 && ValidMeetingID(parts[0]) {
		return "detail", parts[0], true
	}
	if len(parts) == 2 && ValidMeetingID(parts[0]) && parts[1] == "cancel" {
		return "cancel", parts[0], true
	}
	if len(parts) == 3 && parts[0] == "photos" && (parts[1] == "vehicle" || parts[1] == "driver") && ValidMeetingID(parts[2]) {
		return "photo_" + parts[1], parts[2], true
	}
	return "", "", false
}
func VehiclePhotoURL(kind, id string) string {
	if (kind != "vehicle" && kind != "driver") || !ValidMeetingID(id) {
		return ""
	}
	return "/api/vehicles/photos/" + kind + "/" + id
}
func ValidVehiclePhotoURL(value, kind string) bool {
	if value == "" {
		return true
	}
	got, _, ok := VehicleRoute(value)
	return ok && got == "photo_"+kind
}

func VehicleQuery(raw string, options bool) (VehicleSelection, error) {
	v := VehicleSelection{Page: 1, PerPage: 50, Scope: "mine"}
	q, e := url.ParseQuery(raw)
	if e != nil || len(raw) > 2048 {
		return v, errors.New("invalid vehicle selection")
	}
	for k, values := range q {
		if len(values) != 1 {
			return v, errors.New("invalid vehicle selection")
		}
		s := values[0]
		switch k {
		case "vehicle_type":
			if s != "official" && s != "ambulance" && (options || (s != "all" && s != "personal" && s != "central")) {
				return v, errors.New("invalid vehicle type")
			}
			v.VehicleType = s
		case "date", "date_from", "date_to":
			if options && k != "date" {
				return v, errors.New("invalid vehicle selection")
			}
			t, e := time.Parse(time.DateOnly, s)
			if e != nil || t.Format(time.DateOnly) != s || t.Year() < 1900 || t.Year() > 2200 {
				return v, errors.New("invalid vehicle date")
			}
			switch k {
			case "date":
				v.Date = s
			case "date_from":
				v.DateFrom = s
			case "date_to":
				v.DateTo = s
			}
		case "scope":
			if options || (s != "all" && s != "mine") {
				return v, errors.New("invalid vehicle scope")
			}
			v.Scope = s
		case "exclude_cancelled":
			if options || (s != "true" && s != "false") {
				return v, errors.New("invalid vehicle status selection")
			}
			v.ExcludeCancelled = s == "true"
		case "status":
			if options || !ValidVehicleStatus(s) {
				return v, errors.New("invalid vehicle status selection")
			}
			v.Status = s
		case "include_type_counts":
			if options || (s != "true" && s != "false") {
				return v, errors.New("invalid vehicle summary selection")
			}
			v.IncludeTypeCounts = s == "true"
		case "page", "per_page":
			n, e := strconv.Atoi(s)
			if options || e != nil || n < 1 || (k == "page" && n > 1000000) || (k == "per_page" && n > 100) {
				return v, errors.New("invalid vehicle page")
			}
			if k == "page" {
				v.Page = n
			} else {
				v.PerPage = n
			}
		default:
			return v, errors.New("invalid vehicle selection")
		}
	}
	if (v.DateFrom == "") != (v.DateTo == "") || v.DateFrom > v.DateTo || (v.Date != "" && v.DateFrom != "") {
		return v, errors.New("invalid vehicle date range")
	}
	return v, nil
}

func ValidVehicleStatus(value string) bool {
	switch value {
	case "pending", "approved", "assigned", "in_progress", "completed", "rejected", "cancelled", "unknown":
		return true
	default:
		return false
	}
}
func ValidateVehicleInput(v *VehicleInput) map[string]string {
	f := map[string]string{}
	if v == nil {
		f["location"] = "กรุณาตรวจสอบข้อมูลการจองรถ"
		return f
	}
	validateVehicleJourney(v, f)
	immediate := v.RequestMode == "immediate"
	for _, x := range []struct {
		p        *string
		key      string
		max      int
		required bool
	}{
		{&v.Location, "location", 255, !immediate}, {&v.Reason, "reason", 255, true}, {&v.Phone, "phone", 100, true}, {&v.Purpose, "purpose", 255, false}, {&v.Details, "details", 4000, false}, {&v.Pickup, "pickup", 255, v.VehicleType == "ambulance"},
	} {
		*x.p = strings.TrimSpace(*x.p)
		if !meetingText(*x.p, x.max, x.required) {
			f[x.key] = "กรุณาระบุข้อมูลให้ถูกต้อง"
		}
	}
	for _, preference := range []struct {
		value *string
		field string
	}{
		{&v.RequestedVehicleID, "requested_vehicle_id"},
		{&v.RequestedDriverID, "requested_driver_id"},
	} {
		*preference.value = strings.TrimSpace(*preference.value)
		if *preference.value != "" && !ValidVehiclePreferenceID(*preference.value) {
			f[preference.field] = "กรุณาเลือกจากรายการที่มีอยู่"
		}
	}
	if !ValidMeetingRequestID(v.RequestID) {
		f["request_id"] = "รหัสคำขอไม่ถูกต้อง"
	}
	if !immediate {
		for k, msg := range ValidateMeetingWindow(MeetingWindow{StartDate: v.StartDate, EndDate: v.EndDate, StartTime: v.StartTime, EndTime: v.EndTime, Attendees: v.Passengers}) {
			if k == "attendees" {
				k = "passengers"
			}
			f[k] = msg
		}
	}
	if v.Passengers > 1000 {
		f["passengers"] = "จำนวนผู้เดินทางมากเกินไป"
	}
	if v.GoType == 0 {
		v.GoType = 1
	}
	if v.GoType != 1 && v.GoType != 2 {
		f["go_type"] = "ประเภทการเดินทางไม่ถูกต้อง"
	}
	start, _ := time.Parse(time.DateOnly, v.StartDate)
	end, _ := time.Parse(time.DateOnly, v.EndDate)
	if !start.IsZero() && !end.IsZero() && end.Sub(start) > 31*24*time.Hour {
		f["end_date"] = "ระยะเวลาใช้รถต้องไม่เกิน 31 วัน"
	}
	if len(v.Destinations) > 20 {
		f["destinations"] = "จำนวนปลายทางมากเกินไป"
	}
	for i, s := range v.Destinations {
		v.Destinations[i] = strings.TrimSpace(s)
		if !meetingText(v.Destinations[i], 255, true) {
			f["destinations"] = "ปลายทางไม่ถูกต้อง"
		}
	}
	if (!immediate && (len(v.Travelers) < 1 || len(v.Travelers) != v.Passengers)) || len(v.Travelers) > 1000 {
		f["travelers"] = "กรุณาตรวจสอบรายชื่อและจำนวนผู้เดินทาง"
	}
	seen := map[string]bool{}
	for i := range v.Travelers {
		t := &v.Travelers[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Department = strings.TrimSpace(t.Department)
		key := t.EmployeeID
		if t.IsExternal {
			key = "external:" + t.Name
		}
		if !meetingText(t.Name, 255, true) || !meetingText(t.Department, 255, false) || (!t.IsExternal && !ValidMeetingID(t.EmployeeID)) || (t.IsExternal && t.EmployeeID != "") || seen[key] {
			f["travelers"] = "รายชื่อผู้เดินทางไม่ถูกต้อง"
		}
		seen[key] = true
	}
	if v.Travelers == nil {
		v.Travelers = []VehicleTraveler{}
	}
	if v.Destinations == nil {
		v.Destinations = []string{}
	}
	return f
}

func ValidVehiclePreferenceID(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}
