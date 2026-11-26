package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const AttendanceApprovalPageSize = 25

type AttendanceApproval struct {
	ID           string `json:"id"`
	RecordID     string `json:"record_id"`
	EmployeeName string `json:"employee_name"`
	Department   string `json:"department"`
	RecordedAt   string `json:"recorded_at"`
	CheckType    string `json:"check_type"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	LocationName string `json:"location_name"`
	Inside       bool   `json:"inside"`
	HasPhoto     bool   `json:"has_photo"`
	Revision     string `json:"revision"`
	CanDecide    bool   `json:"can_decide"`
}
type AttendanceApprovalList struct {
	Items   []AttendanceApproval `json:"items"`
	Page    int                  `json:"page"`
	HasMore bool                 `json:"has_more"`
}
type AttendanceDecision struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment"`
	Revision string `json:"revision"`
}

func AttendanceApprovalPage(raw string) (int, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return 0, errors.New("invalid query")
	}
	for k, v := range q {
		if k != "page" || len(v) != 1 {
			return 0, errors.New("invalid query")
		}
	}
	if q.Get("page") == "" {
		if len(q) > 0 {
			return 0, errors.New("invalid page")
		}
		return 1, nil
	}
	p, err := strconv.Atoi(q.Get("page"))
	if err != nil || p < 1 || p > 10000 || strconv.Itoa(p) != q.Get("page") {
		return 0, errors.New("invalid page")
	}
	return p, nil
}
func ValidateAttendanceDecision(v *AttendanceDecision) error {
	if v == nil || (v.Decision != "approve" && v.Decision != "reject") || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(v.Revision) || !utf8.ValidString(v.Comment) || utf8.RuneCountInString(v.Comment) > 2000 {
		return errors.New("invalid decision")
	}
	v.Comment = strings.TrimSpace(v.Comment)
	return nil
}
