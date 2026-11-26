package contracts

import (
	"errors"
	"math"
	"regexp"
	"time"
	"unicode/utf8"
)

const MaxAttendancePhotoBytes = 8 * 1024 * 1024
const MaxAttendanceMultipartBytes = MaxAttendancePhotoBytes + 64*1024

// No tenant, employee, authoritative time or approval fields are accepted.
type AttendanceLocationRequest struct {
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
	AccuracyM  *float64 `json:"accuracy_m"`
	CapturedAt string   `json:"captured_at"`
}
type AttendancePunchRequest struct {
	AttendanceLocationRequest
	RequestID                 string `json:"request_id"`
	ExpectedAction            string `json:"expected_action"`
	Reason                    string `json:"reason"`
	PhotoID                   string `json:"photo_id,omitempty"`
	LocationType              string `json:"location_type,omitempty"`
	WorkMode                  string `json:"work_mode,omitempty"`
	CheckoutReason            string `json:"checkout_reason,omitempty"`
	CheckoutMetadataConfirmed bool   `json:"checkout_metadata_confirmed,omitempty"`
}
type AttendancePosition struct {
	Inside         bool    `json:"inside"`
	LocationID     string  `json:"location_id"`
	LocationName   string  `json:"location_name"`
	DistanceM      float64 `json:"distance_m"`
	RadiusM        float64 `json:"radius_m"`
	RequiresPhoto  bool    `json:"requires_photo"`
	RequiresReason bool    `json:"requires_reason"`
}
type AttendanceReceipt struct {
	ID           string `json:"id"`
	RecordedAt   string `json:"recorded_at"`
	CheckType    string `json:"check_type"`
	Status       string `json:"status"`
	LocationName string `json:"location_name"`
	Duplicate    bool   `json:"duplicate"`
	NextAction   string `json:"next_action"`
}
type AttendanceContext struct {
	ServerTime                string             `json:"server_time"`
	NextAction                string             `json:"next_action"`
	Latest                    *AttendanceReceipt `json:"latest"`
	CheckIn                   *string            `json:"check_in"`
	CheckOut                  *string            `json:"check_out"`
	WriteEnabled              bool               `json:"write_enabled"`
	ScheduledEnd              *string            `json:"scheduled_end"`
	CheckoutReasonRequired    bool               `json:"checkout_reason_required"`
	CheckoutReasonSupported   bool               `json:"checkout_reason_supported"`
	CheckoutMetadataSupported bool               `json:"checkout_metadata_supported"`
	LocationType              string             `json:"location_type,omitempty"`
	WorkMode                  string             `json:"work_mode,omitempty"`
}
type AttendancePhotoUploadResult struct {
	PhotoID string `json:"photo_id"`
}

var attendanceRequestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,100}$`)

// Photo IDs are opaque tokens, never filesystem paths.
var attendancePhotoID = regexp.MustCompile(`^[a-zA-Z0-9_-]{32,512}$`)

func ValidAttendancePhotoID(id string) bool { return attendancePhotoID.MatchString(id) }
func ValidateAttendanceLocation(v AttendanceLocationRequest) error {
	if v.Latitude == nil || v.Longitude == nil || v.AccuracyM == nil {
		return errors.New("missing coordinates")
	}
	for _, n := range []*float64{v.Latitude, v.Longitude, v.AccuracyM} {
		if math.IsNaN(*n) || math.IsInf(*n, 0) {
			return errors.New("invalid coordinates")
		}
	}
	if math.Abs(*v.Latitude) > 90 || math.Abs(*v.Longitude) > 180 || *v.AccuracyM < 0 || *v.AccuracyM > 100000 {
		return errors.New("invalid coordinates")
	}
	if _, err := time.Parse(time.RFC3339, v.CapturedAt); err != nil {
		return errors.New("invalid capture time")
	}
	return nil
}
func ValidateAttendancePunch(v AttendancePunchRequest) error {
	if err := ValidateAttendanceLocation(v.AttendanceLocationRequest); err != nil {
		return err
	}
	if !attendanceRequestID.MatchString(v.RequestID) || (v.ExpectedAction != "in" && v.ExpectedAction != "out") || !utf8.ValidString(v.Reason) || utf8.RuneCountInString(v.Reason) > 2000 {
		return errors.New("invalid punch")
	}
	if v.PhotoID != "" && !ValidAttendancePhotoID(v.PhotoID) {
		return errors.New("invalid photo")
	}
	if !oneOfAttendance(v.CheckoutReason, "", "night_shift", "afternoon_shift", "overtime", "forgot_checkout") || (v.CheckoutReason != "" && v.ExpectedAction != "out") {
		return errors.New("invalid checkout reason")
	}
	if v.CheckoutMetadataConfirmed && v.ExpectedAction != "out" {
		return errors.New("checkout metadata confirmation requires checkout")
	}
	// These labels are informational only, never permission to bypass location or approval.
	if !ValidAttendanceLocationType(v.LocationType) || !ValidAttendanceWorkMode(v.WorkMode) {
		return errors.New("invalid work metadata")
	}
	return nil
}

func ValidAttendanceLocationType(v string) bool {
	return oneOfAttendance(v, "", "ON SITE", "ON BUSINESS TRIP", "OFF SITE")
}

func ValidAttendanceWorkMode(v string) bool {
	return oneOfAttendance(v, "", "Office", "On-Business Trip", "On-Field", "Over-Time", "WFH")
}

func oneOfAttendance(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
