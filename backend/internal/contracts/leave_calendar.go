package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type LeaveCalendarSelection struct {
	Date         time.Time
	Page         int
	PerPage      int
	DepartmentID string
}

// The authenticated local profile alone supplies the department/employee scope.
func ParseLeaveCalendarSelection(raw string, day bool) (LeaveCalendarSelection, error) {
	s := LeaveCalendarSelection{Page: 1, PerPage: 30}
	invalid := errors.New("invalid leave calendar selection")
	if len(raw) > 256 {
		return s, invalid
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return s, invalid
	}
	dateKey, layout := "month", "2006-01"
	if day {
		dateKey, layout = "date", time.DateOnly
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return s, invalid
		}
		switch {
		case key == dateKey:
			s.Date, err = time.Parse(layout, values[0])
			if err != nil || s.Date.Format(layout) != values[0] || s.Date.Year() < 1900 || s.Date.Year() > 2200 {
				return s, invalid
			}
		case day && (key == "page" || key == "per_page"):
			if strings.Trim(values[0], "0123456789") != "" {
				return s, invalid
			}
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 || (key == "page" && n > 1000000) || (key == "per_page" && n > 50) {
				return s, invalid
			}
			if key == "page" {
				s.Page = n
			} else {
				s.PerPage = n
			}
		case day && key == "department_id":
			if !ValidLeaveID(values[0]) {
				return s, invalid
			}
			s.DepartmentID = values[0]
		default:
			return s, invalid
		}
	}
	if s.Date.IsZero() {
		return s, invalid
	}
	return s, nil
}

// Department groups use the same bounded date/page selection without a filter.
func ParseLeaveCalendarDepartmentsSelection(raw string) (LeaveCalendarSelection, error) {
	s, err := ParseLeaveCalendarSelection(raw, true)
	if err != nil || s.DepartmentID != "" {
		return s, errors.New("invalid leave calendar department selection")
	}
	return s, nil
}

type LeaveCalendarDay struct {
	Date     string   `json:"date"`
	Total    int      `json:"total"`
	Approved int      `json:"approved"`
	Pending  int      `json:"pending"`
	Holidays []string `json:"holidays"`
}

type LeaveCalendar struct {
	Month                  string             `json:"month"`
	DepartmentID           string             `json:"department_id"`
	Department             string             `json:"department"`
	ScopeKey               string             `json:"scope_key,omitempty"`
	IncludesSubdepartments bool               `json:"includes_subdepartments"`
	Days                   []LeaveCalendarDay `json:"days"`
}

// Deliberately excludes reasons, medical details, contacts and approval records.
type LeaveCalendarEntry struct {
	ID           string `json:"id"`
	EmployeeID   string `json:"employee_id"`
	EmployeeName string `json:"employee_name"`
	DepartmentID string `json:"department_id,omitempty"`
	Department   string `json:"department,omitempty"`
	LeaveTypeID  string `json:"leave_type_id,omitempty"`
	LeaveType    string `json:"leave_type,omitempty"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Period       string `json:"period"`
	Status       string `json:"status"`
	Approved     bool   `json:"approved"`
}

type LeaveCalendarPage struct {
	Date                   string               `json:"date"`
	DepartmentID           string               `json:"department_id"`
	Department             string               `json:"department"`
	SelectedDepartmentID   string               `json:"selected_department_id,omitempty"`
	ScopeKey               string               `json:"scope_key,omitempty"`
	IncludesSubdepartments bool                 `json:"includes_subdepartments"`
	Items                  []LeaveCalendarEntry `json:"items"`
	Page                   int                  `json:"page"`
	PerPage                int                  `json:"per_page"`
	TotalCount             int                  `json:"total_count"`
	HasMore                bool                 `json:"has_more"`
}

type LeaveCalendarDepartment struct {
	DepartmentID   string `json:"department_id"`
	Department     string `json:"department"`
	TotalPeople    int    `json:"total_people"`
	TotalRecords   int    `json:"total_records"`
	CanViewDetails bool   `json:"can_view_details"`
}

type LeaveCalendarDepartmentsPage struct {
	Date                   string                    `json:"date"`
	DepartmentID           string                    `json:"department_id"`
	Department             string                    `json:"department"`
	ScopeKey               string                    `json:"scope_key"`
	IncludesSubdepartments bool                      `json:"includes_subdepartments"`
	TotalPeople            int                       `json:"total_people"`
	Items                  []LeaveCalendarDepartment `json:"items"`
	Page                   int                       `json:"page"`
	PerPage                int                       `json:"per_page"`
	TotalCount             int                       `json:"total_count"`
	HasMore                bool                      `json:"has_more"`
}

func CalendarLeaveStatus(status string) bool {
	switch status {
	case "Approve", "Pending", "Checking1_pass", "Checking2_pass", "Checkup_pass", "ReqCancel":
		return true
	}
	return false
}

func ValidLeaveCalendar(v LeaveCalendar, selection LeaveCalendarSelection) bool {
	if !validLeaveCalendarScope(v.ScopeKey, v.IncludesSubdepartments) {
		return false
	}
	if v.Month != selection.Date.Format("2006-01") || !ValidLeaveID(v.DepartmentID) || strings.TrimSpace(v.Department) == "" || len(v.Department) > 1024 || len(v.Days) != selection.Date.AddDate(0, 1, -1).Day() {
		return false
	}
	for i, d := range v.Days {
		if d.Date != selection.Date.AddDate(0, 0, i).Format(time.DateOnly) || d.Total < 0 || d.Approved < 0 || d.Pending < 0 || d.Approved > d.Total || d.Pending > d.Total || d.Total > d.Approved+d.Pending || d.Holidays == nil || len(d.Holidays) > 64 {
			return false
		}
		seen := map[string]bool{}
		for _, title := range d.Holidays {
			if strings.TrimSpace(title) == "" || len(title) > 1024 || seen[title] {
				return false
			}
			seen[title] = true
		}
	}
	return true
}

func ValidLeaveCalendarPage(v LeaveCalendarPage, selection LeaveCalendarSelection) bool {
	if !validLeaveCalendarScope(v.ScopeKey, v.IncludesSubdepartments) {
		return false
	}
	if v.Date != selection.Date.Format(time.DateOnly) || !ValidLeaveID(v.DepartmentID) || strings.TrimSpace(v.Department) == "" || len(v.Department) > 1024 || v.Items == nil || len(v.Items) > selection.PerPage || v.Page != selection.Page || v.PerPage != selection.PerPage || v.TotalCount < 0 {
		return false
	}
	if v.SelectedDepartmentID != selection.DepartmentID || (v.SelectedDepartmentID != "" && !ValidLeaveID(v.SelectedDepartmentID)) {
		return false
	}
	offset := (v.Page - 1) * v.PerPage
	expected := max(0, min(v.PerPage, v.TotalCount-offset))
	if len(v.Items) != expected || v.HasMore != (offset+len(v.Items) < v.TotalCount) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if selection.DepartmentID != "" && item.DepartmentID != selection.DepartmentID {
			return false
		}
		if (item.DepartmentID != "" || item.Department != "") && (!ValidLeaveID(item.DepartmentID) || strings.TrimSpace(item.Department) == "" || len(item.Department) > 1024) {
			return false
		}
		if (item.LeaveTypeID != "" && !ValidLeaveTypeID(item.LeaveTypeID)) || len(item.LeaveType) > 1024 || (item.LeaveTypeID != "" && strings.TrimSpace(item.LeaveType) == "") {
			return false
		}
		a, e1 := time.Parse(time.DateOnly, item.StartDate)
		b, e2 := time.Parse(time.DateOnly, item.EndDate)
		if !ValidLeaveID(item.ID) || !ValidLeaveID(item.EmployeeID) || seen[item.ID] || strings.TrimSpace(item.EmployeeName) == "" || len(item.EmployeeName) > 1024 || len(item.Period) > 512 || !CalendarLeaveStatus(item.Status) || item.Approved != (item.Status == "Approve") || e1 != nil || e2 != nil || b.Before(a) || a.After(selection.Date) || b.Before(selection.Date) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

func ValidLeaveCalendarDepartmentsPage(v LeaveCalendarDepartmentsPage, selection LeaveCalendarSelection) bool {
	if selection.DepartmentID != "" || selection.Page < 1 || selection.Page > 1000000 || selection.PerPage < 1 || selection.PerPage > 50 || len(v.ScopeKey) != 64 || !validLeaveCalendarScope(v.ScopeKey, v.IncludesSubdepartments) || v.Date != selection.Date.Format(time.DateOnly) || !ValidLeaveID(v.DepartmentID) || strings.TrimSpace(v.Department) == "" || len(v.Department) > 1024 || v.Items == nil || v.Page != selection.Page || v.PerPage != selection.PerPage || v.TotalCount < 0 || v.TotalCount > 10000 || v.TotalPeople < 0 || (!v.IncludesSubdepartments && v.TotalCount > 1) {
		return false
	}
	offset := (v.Page - 1) * v.PerPage
	expected := max(0, min(v.PerPage, v.TotalCount-offset))
	if len(v.Items) != expected || v.HasMore != (offset+len(v.Items) < v.TotalCount) || (v.TotalCount == 0) != (v.TotalPeople == 0) || v.TotalCount > v.TotalPeople {
		return false
	}
	seen := map[string]bool{}
	people := 0
	for _, item := range v.Items {
		if !ValidLeaveID(item.DepartmentID) || seen[item.DepartmentID] || strings.TrimSpace(item.Department) == "" || len(item.Department) > 1024 || item.TotalPeople < 1 || item.TotalRecords < item.TotalPeople || item.TotalPeople > v.TotalPeople || (!v.IncludesSubdepartments && item.DepartmentID != v.DepartmentID) {
			return false
		}
		seen[item.DepartmentID] = true
		if item.TotalPeople > v.TotalPeople-people {
			return false
		}
		people += item.TotalPeople
	}
	if v.TotalPeople-people < v.TotalCount-len(v.Items) {
		return false
	}
	return offset != 0 || len(v.Items) != v.TotalCount || people == v.TotalPeople
}

func validLeaveCalendarScope(key string, expanded bool) bool {
	if key == "" {
		return !expanded
	} // Older exact-department APIs omit metadata.
	return len(key) == 64 && strings.Trim(key, "0123456789abcdef") == ""
}
