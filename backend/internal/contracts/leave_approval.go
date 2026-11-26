package contracts

import (
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const LeaveApprovalPageSize = 20

// ID is the leave ID; ApprovalID identifies the exact saved workflow step.
// A notification's leave target can therefore open the current authorized view.
type LeaveApprovalItem struct {
	ID             string  `json:"id"`
	ApprovalID     string  `json:"approval_id"`
	RequesterName  string  `json:"requester_name"`
	Department     string  `json:"department"`
	LeaveType      string  `json:"leave_type"`
	StartDate      string  `json:"start_date"`
	EndDate        string  `json:"end_date"`
	StartTime      string  `json:"start_time"`
	EndTime        string  `json:"end_time"`
	TotalDays      float64 `json:"total_days"`
	Status         string  `json:"status"`
	StatusLabel    string  `json:"status_label"`
	ApprovalStatus string  `json:"approval_status"`
	Level          int     `json:"level"`
	Title          string  `json:"title"`
	Label          string  `json:"label"`
	Revision       string  `json:"revision"`
	CanDecide      bool    `json:"can_decide"`
}

type LeaveApprovalDetail struct {
	LeaveApprovalItem
	Leave Leave `json:"leave"`
}

type LeaveApprovalList struct {
	Items      []LeaveApprovalItem `json:"items"`
	Page       int                 `json:"page"`
	PerPage    int                 `json:"per_page"`
	TotalCount int                 `json:"total_count"`
	HasMore    bool                `json:"has_more"`
}

type LeaveApprovalSummary struct {
	PendingCount int  `json:"pending_count"`
	WriteEnabled bool `json:"write_enabled"`
}

type LeaveApprovalDecision struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Comment    string `json:"comment"`
	Revision   string `json:"revision"`
}

var leaveApprovalRevision = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ParseLeaveApprovalPage(raw string) (int, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return 0, errors.New("invalid approval query")
	}
	for key, values := range q {
		if key != "page" || len(values) != 1 {
			return 0, errors.New("invalid approval query")
		}
	}
	if len(q) == 0 {
		return 1, nil
	}
	p, err := strconv.Atoi(q.Get("page"))
	if err != nil || p < 1 || p > 10000 || strconv.Itoa(p) != q.Get("page") {
		return 0, errors.New("invalid approval page")
	}
	return p, nil
}

func ValidateLeaveApprovalDecision(v *LeaveApprovalDecision) error {
	if v == nil || !ValidLeaveID(v.ApprovalID) || (v.Decision != "approve" && v.Decision != "reject") || !leaveApprovalRevision.MatchString(v.Revision) || !utf8.ValidString(v.Comment) || utf8.RuneCountInString(v.Comment) > 2000 || strings.ContainsRune(v.Comment, 0) {
		return errors.New("invalid approval decision")
	}
	v.Comment = strings.TrimSpace(v.Comment)
	if v.Decision == "reject" && v.Comment == "" {
		return errors.New("rejection comment required")
	}
	return nil
}

func ValidLeaveApprovalItem(v LeaveApprovalItem) bool {
	if !ValidLeaveID(v.ID) || !ValidLeaveID(v.ApprovalID) || v.Level < 1 || v.Level > 100 || !leaveApprovalRevision.MatchString(v.Revision) || v.TotalDays < 0 || math.IsNaN(v.TotalDays) || math.IsInf(v.TotalDays, 0) {
		return false
	}
	switch v.ApprovalStatus {
	case "Pending", "Pass", "Reject", "None":
	default:
		return false
	}
	if v.CanDecide && (v.ApprovalStatus != "Pending" || !PendingLeaveApprovalStatus(v.Status)) {
		return false
	}
	start, startErr := time.Parse(time.DateOnly, v.StartDate)
	end, endErr := time.Parse(time.DateOnly, v.EndDate)
	if startErr != nil || endErr != nil || end.Before(start) {
		return false
	}
	for _, value := range []string{v.RequesterName, v.Department, v.LeaveType, v.Status, v.StatusLabel, v.ApprovalStatus, v.Title, v.Label} {
		if !utf8.ValidString(value) || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return strings.TrimSpace(v.RequesterName) != ""
}

func PendingLeaveApprovalStatus(status string) bool {
	switch status {
	case "Pending", "Checking1_pass", "Checking2_pass", "Checkup_pass":
		return true
	}
	return false
}
