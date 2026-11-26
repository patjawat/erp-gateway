package contracts

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// MeetingUsage shares meeting subjects while omitting contacts and personnel.
// Only the requesting employee's reservations carry a link to owned details.
type MeetingUsage struct {
	Date      string             `json:"date"`
	CheckedAt string             `json:"checked_at"`
	Items     []MeetingUsageItem `json:"items"`
}
type MeetingUsageItem struct {
	RoomID      string `json:"room_id"`
	RoomName    string `json:"room_name"`
	Title       string `json:"title"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
	BookingID   string `json:"booking_id,omitempty"`
}

func ValidMeetingUsageDate(value string) bool {
	date, err := time.Parse(time.DateOnly, value)
	return err == nil && date.Format(time.DateOnly) == value && date.Year() >= 1900 && date.Year() <= 2200
}
func MeetingUsageQuery(raw string) (string, error) {
	query, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 128 || len(query) != 1 || len(query["date"]) != 1 || !ValidMeetingUsageDate(query.Get("date")) {
		return "", errors.New("invalid meeting usage date")
	}
	return query.Get("date"), nil
}
func ValidMeetingUsage(value *MeetingUsage) bool {
	if value == nil || !ValidMeetingUsageDate(value.Date) || value.Items == nil || len(value.Items) > 10000 {
		return false
	}
	if _, err := time.Parse(time.RFC3339, value.CheckedAt); err != nil {
		return false
	}
	for _, item := range value.Items {
		if !meetingText(item.Title, 255, false) || strings.TrimSpace(item.RoomID) == "" || strings.TrimSpace(item.RoomName) == "" || !ValidMeetingUsageDate(item.StartDate) || !ValidMeetingUsageDate(item.EndDate) || !meetingTimePattern.MatchString(item.StartTime) || !meetingTimePattern.MatchString(item.EndTime) || item.EndDate+" "+item.EndTime <= item.StartDate+" "+item.StartTime || item.StatusLabel == "" || (item.BookingID != "" && !ValidMeetingID(item.BookingID)) {
			return false
		}
		if item.StartDate > value.Date || item.EndDate < value.Date || (item.EndDate == value.Date && item.EndTime == "00:00") {
			return false
		}
		switch item.Status {
		case "pending", "approved", "unknown":
		default:
			return false
		}
	}
	return true
}
