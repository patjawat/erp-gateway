package contracts

import (
	"errors"
	"net/url"
	"time"
)

// AttendanceMonth deliberately has no employee or hospital selectors. Both are
// resolved from the authenticated session at the gateway and local API.
func AttendanceMonth(rawQuery string) (string, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(rawQuery) > 64 || len(query) != 1 || len(query["month"]) != 1 {
		return "", errors.New("invalid attendance month")
	}
	month := query.Get("month")
	parsed, err := time.Parse("2006-01", month)
	if err != nil || parsed.Year() < 1900 || parsed.Year() > 2200 || parsed.Format("2006-01") != month {
		return "", errors.New("invalid attendance month")
	}
	return month, nil
}

type AttendanceEvent struct {
	ID           string `json:"id"`
	CheckType    string `json:"check_type"`
	RecordedAt   string `json:"recorded_at"`
	Status       string `json:"status"`
	Method       string `json:"method"`
	Location     string `json:"location"`
	HasPhoto     bool   `json:"has_photo"`
	IsInLocation *bool  `json:"is_in_location"`
	Reason       string `json:"reason"`
}

type AttendanceDay struct {
	Date            string            `json:"date"`
	CheckIn         *string           `json:"check_in"`
	CheckOut        *string           `json:"check_out"`
	DurationMinutes *int              `json:"duration_minutes"`
	SourceLabel     string            `json:"source_label"`
	Status          string            `json:"status"`
	Events          []AttendanceEvent `json:"events"`
}

type AttendanceHistory struct {
	Month string          `json:"month"`
	Items []AttendanceDay `json:"items"`
}
