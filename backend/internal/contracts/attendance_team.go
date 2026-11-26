package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type AttendanceTeamSelection struct {
	DepartmentID, Status, ExpectedDate, ScopeKey string
	DirectOnly                                   bool
	Page, PerPage                                int
}

func ParseAttendanceTeamSelection(raw, kind string) (AttendanceTeamSelection, error) {
	s := AttendanceTeamSelection{Page: 1, PerPage: 30}
	invalid := errors.New("invalid attendance team selection")
	if len(raw) > 512 || (kind != "summary" && kind != "departments" && kind != "members") {
		return s, invalid
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return s, invalid
	}
	for k, v := range q {
		if kind == "summary" || len(v) != 1 || v[0] == "" {
			return s, invalid
		}
		switch k {
		case "department_id":
			if !ValidLeaveID(v[0]) {
				return s, invalid
			}
			s.DepartmentID = v[0]
		case "page", "per_page":
			if strings.Trim(v[0], "0123456789") != "" {
				return s, invalid
			}
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || (k == "page" && n > 1000000) || (k == "per_page" && n > 50) {
				return s, invalid
			}
			if k == "page" {
				s.Page = n
			} else {
				s.PerPage = n
			}
		case "status":
			if kind != "members" || (v[0] != "checked" && v[0] != "pending") {
				return s, invalid
			}
			s.Status = v[0]
		case "direct_only":
			if kind != "members" || (v[0] != "true" && v[0] != "false") {
				return s, invalid
			}
			s.DirectOnly = v[0] == "true"
		case "scope_key":
			if !teamScopeKey(v[0]) {
				return s, invalid
			}
			s.ScopeKey = v[0]
		case "expected_date":
			d, e := time.Parse(time.DateOnly, v[0])
			if e != nil || d.Format(time.DateOnly) != v[0] || d.Year() < 1900 || d.Year() > 2200 {
				return s, invalid
			}
			s.ExpectedDate = v[0]
		default:
			return s, invalid
		}
	}
	if kind == "members" && (s.DepartmentID == "" || s.Status == "") {
		return s, invalid
	}
	return s, nil
}

func (s AttendanceTeamSelection) Query() string {
	q := url.Values{"page": {strconv.Itoa(s.Page)}, "per_page": {strconv.Itoa(s.PerPage)}}
	if s.DepartmentID != "" {
		q.Set("department_id", s.DepartmentID)
	}
	if s.Status != "" {
		q.Set("status", s.Status)
		q.Set("direct_only", strconv.FormatBool(s.DirectOnly))
	}
	if s.ExpectedDate != "" {
		q.Set("expected_date", s.ExpectedDate)
	}
	if s.ScopeKey != "" {
		q.Set("scope_key", s.ScopeKey)
	}
	return q.Encode()
}

type AttendanceTeamSummary struct {
	Date           string `json:"date"`
	ScopeKey       string `json:"scope_key"`
	DepartmentID   string `json:"department_id"`
	Department     string `json:"department"`
	CanBrowseUnits bool   `json:"can_browse_units"`
	CheckedCount   int    `json:"checked_count"`
	TotalCount     int    `json:"total_count"`
	PendingCount   int    `json:"pending_count"`
}

type AttendanceTeamDepartment struct {
	DepartmentID       string `json:"department_id"`
	Department         string `json:"department"`
	ParentDepartmentID string `json:"parent_department_id"`
	CheckedCount       int    `json:"checked_count"`
	TotalCount         int    `json:"total_count"`
	PendingCount       int    `json:"pending_count"`
	DirectMemberCount  int    `json:"direct_member_count"`
	HasChildren        bool   `json:"has_children"`
}

type AttendanceTeamDepartmentsPage struct {
	Date                 string                     `json:"date"`
	ScopeKey             string                     `json:"scope_key"`
	DepartmentID         string                     `json:"department_id"`
	Department           string                     `json:"department"`
	CanBrowseUnits       bool                       `json:"can_browse_units"`
	SelectedDepartmentID string                     `json:"selected_department_id"`
	SelectedDepartment   string                     `json:"selected_department"`
	SelectedCheckedCount int                        `json:"selected_checked_count"`
	SelectedTotalCount   int                        `json:"selected_total_count"`
	SelectedPendingCount int                        `json:"selected_pending_count"`
	DirectCheckedCount   int                        `json:"direct_checked_count"`
	DirectTotalCount     int                        `json:"direct_total_count"`
	DirectPendingCount   int                        `json:"direct_pending_count"`
	Items                []AttendanceTeamDepartment `json:"items"`
	Page                 int                        `json:"page"`
	PerPage              int                        `json:"per_page"`
	TotalCount           int                        `json:"total_count"`
	HasMore              bool                       `json:"has_more"`
}

type AttendanceTeamMember struct {
	ID           string  `json:"id"`
	FullName     string  `json:"full_name"`
	GivenName    string  `json:"given_name"`
	Position     string  `json:"position"`
	DepartmentID string  `json:"department_id"`
	Department   string  `json:"department"`
	AvatarURL    string  `json:"avatar_url"`
	CheckInAt    *string `json:"check_in_at"`
}

type AttendanceTeamMembersPage struct {
	Date                 string                 `json:"date"`
	ScopeKey             string                 `json:"scope_key"`
	DepartmentID         string                 `json:"department_id"`
	Department           string                 `json:"department"`
	CanBrowseUnits       bool                   `json:"can_browse_units"`
	SelectedDepartmentID string                 `json:"selected_department_id"`
	SelectedDepartment   string                 `json:"selected_department"`
	DirectOnly           bool                   `json:"direct_only"`
	Status               string                 `json:"status"`
	CheckedCount         int                    `json:"checked_count"`
	TotalPeople          int                    `json:"total_people"`
	PendingCount         int                    `json:"pending_count"`
	Items                []AttendanceTeamMember `json:"items"`
	Page                 int                    `json:"page"`
	PerPage              int                    `json:"per_page"`
	TotalCount           int                    `json:"total_count"`
	HasMore              bool                   `json:"has_more"`
}

func teamScopeKey(v string) bool { return len(v) == 64 && strings.Trim(v, "0123456789abcdef") == "" }
func teamText(v string, required bool) bool {
	return utf8.ValidString(v) && len(v) <= 1024 && (!required || strings.TrimSpace(v) != "")
}
func teamCounts(checked, total, pending int) bool {
	return checked >= 0 && total >= 0 && pending >= 0 && checked <= total && pending == total-checked
}
func teamMeta(date, key, id, name string, s AttendanceTeamSelection) bool {
	d, e := time.Parse(time.DateOnly, date)
	return e == nil && d.Year() >= 1900 && d.Year() <= 2200 && teamScopeKey(key) && ValidLeaveID(id) && teamText(name, true) && (s.ExpectedDate == "" || s.ExpectedDate == date) && (s.ScopeKey == "" || s.ScopeKey == key)
}
func teamPage(page, perPage, total, size int, more bool, s AttendanceTeamSelection) bool {
	if page != s.Page || perPage != s.PerPage || page < 1 || perPage < 1 || perPage > 50 || total < 0 {
		return false
	}
	offset := (page - 1) * perPage
	return size == max(0, min(perPage, total-offset)) && more == (offset+size < total)
}
func ValidAttendanceTeamSummary(v AttendanceTeamSummary) bool {
	return teamMeta(v.Date, v.ScopeKey, v.DepartmentID, v.Department, AttendanceTeamSelection{}) && teamCounts(v.CheckedCount, v.TotalCount, v.PendingCount)
}
func ValidAttendanceTeamDepartmentsPage(v AttendanceTeamDepartmentsPage, s AttendanceTeamSelection) bool {
	if !v.CanBrowseUnits && (v.TotalCount > 1 || s.DepartmentID != "" && s.DepartmentID != v.DepartmentID) {
		return false
	}
	if !teamMeta(v.Date, v.ScopeKey, v.DepartmentID, v.Department, s) || v.SelectedDepartmentID != s.DepartmentID || (s.DepartmentID != "" && !teamText(v.SelectedDepartment, true)) || v.Items == nil || !teamPage(v.Page, v.PerPage, v.TotalCount, len(v.Items), v.HasMore, s) || !teamCounts(v.SelectedCheckedCount, v.SelectedTotalCount, v.SelectedPendingCount) || !teamCounts(v.DirectCheckedCount, v.DirectTotalCount, v.DirectPendingCount) || v.DirectTotalCount > v.SelectedTotalCount {
		return false
	}
	seen := map[string]bool{}
	for _, d := range v.Items {
		if !ValidLeaveID(d.DepartmentID) || seen[d.DepartmentID] || !teamText(d.Department, true) || (d.ParentDepartmentID != "" && !ValidLeaveID(d.ParentDepartmentID)) || !teamCounts(d.CheckedCount, d.TotalCount, d.PendingCount) || d.DirectMemberCount < 0 || d.DirectMemberCount > d.TotalCount || (s.DepartmentID != "" && d.ParentDepartmentID != s.DepartmentID) || (!v.CanBrowseUnits && d.DepartmentID != v.DepartmentID) {
			return false
		}
		seen[d.DepartmentID] = true
	}
	return true
}
func ValidAttendanceTeamMembersPage(v AttendanceTeamMembersPage, s AttendanceTeamSelection) bool {
	if !v.CanBrowseUnits && s.DepartmentID != v.DepartmentID {
		return false
	}
	if !teamMeta(v.Date, v.ScopeKey, v.DepartmentID, v.Department, s) || v.SelectedDepartmentID != s.DepartmentID || !teamText(v.SelectedDepartment, true) || v.DirectOnly != s.DirectOnly || v.Status != s.Status || v.Items == nil || !teamPage(v.Page, v.PerPage, v.TotalCount, len(v.Items), v.HasMore, s) || !teamCounts(v.CheckedCount, v.TotalPeople, v.PendingCount) {
		return false
	}
	want := v.PendingCount
	if s.Status == "checked" {
		want = v.CheckedCount
	}
	if v.TotalCount != want {
		return false
	}
	seen := map[string]bool{}
	for _, m := range v.Items {
		if !ValidLeaveID(m.ID) || seen[m.ID] || !ValidLeaveID(m.DepartmentID) || !teamText(m.FullName, true) || !teamText(m.GivenName, false) || !teamText(m.Position, false) || !teamText(m.Department, true) || m.AvatarURL != "" || (s.DirectOnly && m.DepartmentID != s.DepartmentID) || (!v.CanBrowseUnits && m.DepartmentID != v.DepartmentID) {
			return false
		}
		seen[m.ID] = true
		if (m.CheckInAt != nil) != (s.Status == "checked") {
			return false
		}
		if m.CheckInAt != nil {
			at, e := time.Parse(time.RFC3339, *m.CheckInAt)
			if e != nil || at.In(time.FixedZone("Asia/Bangkok", 7*60*60)).Format(time.DateOnly) != v.Date {
				return false
			}
		}
	}
	return true
}
