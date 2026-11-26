package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type LeaveHistorySelection struct {
	Page        int
	PerPage     int
	FiscalYear  int
	LeaveTypeID string
}

func (s LeaveHistorySelection) HasFilters() bool { return s.FiscalYear != 0 || s.LeaveTypeID != "" }

// ParseLeaveHistorySelection never accepts caller-supplied employee/hospital IDs.
func ParseLeaveHistorySelection(rawQuery string) (LeaveHistorySelection, error) {
	selection := LeaveHistorySelection{Page: 1, PerPage: 50}
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(rawQuery) > 1024 {
		return selection, errors.New("invalid leave history selection")
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return selection, errors.New("invalid leave history selection")
		}
		switch key {
		case "leave_type_id":
			if !ValidLeaveTypeID(values[0]) {
				return selection, errors.New("invalid leave history selection")
			}
			selection.LeaveTypeID = values[0]
		case "page", "per_page", "fiscal_year":
			if strings.Trim(values[0], "0123456789") != "" {
				return selection, errors.New("invalid leave history selection")
			}
			value, parseErr := strconv.Atoi(values[0])
			if parseErr != nil || value < 1 || key == "page" && value > 1000000 || key == "per_page" && value > 100 || key == "fiscal_year" && (len(values[0]) != 4 || value < 2443 || value > 2744) {
				return selection, errors.New("invalid leave history selection")
			}
			switch key {
			case "page":
				selection.Page = value
			case "per_page":
				selection.PerPage = value
			case "fiscal_year":
				selection.FiscalYear = value
			}
		default:
			return selection, errors.New("invalid leave history selection")
		}
	}
	return selection, nil
}

// Legacy paging-only callers must not silently discard requested filters.
func LeavePagination(rawQuery string) (page, perPage int, err error) {
	selection, err := ParseLeaveHistorySelection(rawQuery)
	if err != nil || selection.HasFilters() {
		return 0, 0, errors.New("invalid leave history selection")
	}
	return selection.Page, selection.PerPage, nil
}

type LeaveType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type LeaveBalance struct {
	LeaveTypeID     string   `json:"leave_type_id"`
	LeaveType       string   `json:"leave_type"`
	EntitlementDays *float64 `json:"entitlement_days"`
	UsedDays        *float64 `json:"used_days"`
	RemainingDays   *float64 `json:"remaining_days"`
}

type LeaveOptions struct {
	AttachmentsEnabled bool           `json:"attachments_enabled"`
	Types              []LeaveType    `json:"types"`
	Balances           []LeaveBalance `json:"balances"`
	CanSubmit          bool           `json:"can_submit"`
	UnavailableReason  string         `json:"unavailable_reason"`
	FiscalYear         int            `json:"fiscal_year"`
}
type LeaveInput struct {
	Attachments           []LeaveUpload `json:"attachments,omitempty"`
	RequestID             string        `json:"request_id"`
	LeaveTypeID           string        `json:"leave_type_id"`
	StartDate             string        `json:"start_date"`
	EndDate               string        `json:"end_date"`
	StartTime             string        `json:"start_time"`
	EndTime               string        `json:"end_time"`
	Reason                string        `json:"reason"`
	TravelScope           string        `json:"travel_scope"`
	Comments              string        `json:"comments"`
	ReplacementEmployeeID string        `json:"replacement_employee_id"`
}
type LeavePreview struct {
	TotalDays    float64 `json:"total_days"`
	Period       string  `json:"period"`
	FiscalYear   int     `json:"fiscal_year"`
	CalendarDays int     `json:"calendar_days"`
	WeekendDays  int     `json:"weekend_days"`
	// HolidayDays excludes holidays that fall on a weekend, so the two counts
	// can be subtracted without counting the same date twice.
	HolidayDays      int             `json:"holiday_days"`
	HolidaysDeducted bool            `json:"holidays_deducted"`
	Approvals        []LeaveApproval `json:"approvals"`
	AutoApproved     bool            `json:"auto_approved"`
}

type LeaveApproval struct {
	Level        int    `json:"level"`
	Title        string `json:"title"`
	Label        string `json:"label"`
	EmployeeID   string `json:"employee_id"`
	EmployeeName string `json:"employee_name"`
	Role         string `json:"role"`
}

var leaveTypePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func ValidLeaveTypeID(id string) bool { return leaveTypePattern.MatchString(id) }

func ValidLeaveID(id string) bool { return meetingIDPattern.MatchString(id) }
func LeaveFiscalYear(day time.Time) int {
	y := day.Year() + 543
	if day.Month() >= time.October {
		y++
	}
	return y
}
func ValidateLeaveInput(v *LeaveInput, preview bool) map[string]string {
	fields := map[string]string{}
	if len(v.Attachments) > MaxLeaveAttachments || preview && len(v.Attachments) > 0 {
		fields["attachments"] = "แนบเอกสารได้ไม่เกิน 3 ไฟล์"
	}
	for _, file := range v.Attachments {
		if !ValidLeaveUpload(file) {
			fields["attachments"] = "รองรับ JPG, PNG และ PDF ไฟล์ละไม่เกิน 5 MB"
			break
		}
	}
	for _, p := range []*string{&v.RequestID, &v.LeaveTypeID, &v.StartDate, &v.EndDate, &v.StartTime, &v.EndTime, &v.Reason, &v.TravelScope, &v.Comments, &v.ReplacementEmployeeID} {
		*p = strings.TrimSpace(*p)
	}
	if !leaveTypePattern.MatchString(v.LeaveTypeID) {
		fields["leave_type_id"] = "กรุณาเลือกประเภทการลา"
	}
	if !preview && !ValidMeetingRequestID(v.RequestID) {
		fields["request_id"] = "รหัสคำขอไม่ถูกต้อง"
	}
	start, e1 := time.Parse(time.DateOnly, v.StartDate)
	end, e2 := time.Parse(time.DateOnly, v.EndDate)
	if e1 != nil || start.Year() < 1900 || start.Year() > 2200 {
		fields["start_date"] = "วันที่เริ่มต้นไม่ถูกต้อง"
	}
	if e2 != nil || end.Year() < 1900 || end.Year() > 2200 {
		fields["end_date"] = "วันที่สิ้นสุดไม่ถูกต้อง"
	}
	if e1 == nil && e2 == nil && (end.Before(start) || end.Sub(start) > 366*24*time.Hour || LeaveFiscalYear(start) != LeaveFiscalYear(end)) {
		fields["end_date"] = "กรุณาเลือกช่วงวันที่ภายในปีงบประมาณเดียวกัน"
	}
	if v.StartTime != "08:30" && v.StartTime != "13:00" {
		fields["start_time"] = "เวลาเริ่มต้นไม่ถูกต้อง"
	}
	if v.EndTime != "12:00" && v.EndTime != "16:30" {
		fields["end_time"] = "เวลาสิ้นสุดไม่ถูกต้อง"
	}
	if v.StartDate == v.EndDate && v.StartTime >= v.EndTime {
		fields["end_time"] = "เวลาสิ้นสุดต้องหลังเวลาเริ่มต้น"
	}
	for _, x := range []struct {
		k, s     string
		max      int
		required bool
	}{{"reason", v.Reason, 4000, !preview}, {"travel_scope", v.TravelScope, 255, !preview}, {"comments", v.Comments, 4000, false}} {
		if !utf8.ValidString(x.s) || utf8.RuneCountInString(x.s) > x.max || (x.required && x.s == "") || strings.ContainsRune(x.s, 0) {
			fields[x.k] = "กรุณาตรวจสอบข้อมูลการลา"
		}
	}
	if v.ReplacementEmployeeID != "" && !ValidLeaveID(v.ReplacementEmployeeID) {
		fields["replacement_employee_id"] = "ผู้ปฏิบัติงานแทนไม่ถูกต้อง"
	}
	return fields
}
