package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Employee-service queries deliberately have no employee or hospital selector.
// Both are resolved from the authenticated session by the two Go APIs.
type EmployeeServiceSelection struct {
	Page, PerPage int
	Kind, Section string
}

func EmployeeServiceQuery(raw string, housing bool) (EmployeeServiceSelection, error) {
	s := EmployeeServiceSelection{Page: 1, PerPage: 20, Kind: "all", Section: "requests"}
	bad := errors.New("invalid employee service query")
	q, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 200 {
		return s, bad
	}
	for key, values := range q {
		if len(values) != 1 {
			return s, bad
		}
		switch key {
		case "page", "per_page":
			n, err := strconv.Atoi(values[0])
			if err != nil {
				return s, bad
			}
			if key == "page" {
				s.Page = n
			} else {
				s.PerPage = n
			}
		case "kind":
			if housing {
				return s, bad
			}
			s.Kind = values[0]
		case "section":
			if !housing {
				return s, bad
			}
			s.Section = values[0]
		default:
			return s, bad
		}
	}
	if s.Page < 1 || s.Page > 100000 || s.PerPage < 1 || s.PerPage > 100 ||
		(s.Kind != "all" && s.Kind != "education" && s.Kind != "medical") ||
		(s.Section != "requests" && s.Section != "expenses") {
		return s, bad
	}
	return s, nil
}

func (s EmployeeServiceSelection) Query(housing bool) string {
	q := url.Values{"page": {strconv.Itoa(s.Page)}, "per_page": {strconv.Itoa(s.PerPage)}}
	if housing {
		q.Set("section", s.Section)
	} else {
		q.Set("kind", s.Kind)
	}
	return q.Encode()
}

type WelfareClaim struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Number          string  `json:"number"`
	Subject         string  `json:"subject"`
	Date            string  `json:"date"`
	Status          string  `json:"status"`
	StatusLabel     string  `json:"status_label"`
	RequestedAmount string  `json:"requested_amount"`
	ApprovedAmount  *string `json:"approved_amount"`
	AcademicYear    int     `json:"academic_year"`
	Term            int     `json:"term"`
}

type WelfareHistory struct {
	Items      []WelfareClaim `json:"items"`
	TotalCount int            `json:"total_count"`
	Page       int            `json:"page"`
	PerPage    int            `json:"per_page"`
}

type HousingOccupancy struct {
	ID          string `json:"id"`
	Building    string `json:"building"`
	Unit        string `json:"unit"`
	Room        string `json:"room"`
	StartDate   string `json:"start_date"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
}

type HousingRecord struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Number        string  `json:"number"`
	Date          string  `json:"date"`
	Status        string  `json:"status"`
	StatusLabel   string  `json:"status_label"`
	Building      string  `json:"building"`
	Unit          string  `json:"unit"`
	Room          string  `json:"room"`
	TotalAmount   *string `json:"total_amount"`
	PaidAmount    *string `json:"paid_amount"`
	BalanceAmount *string `json:"balance_amount"`
}

type HousingHistory struct {
	Occupancy  *HousingOccupancy `json:"occupancy"`
	Items      []HousingRecord   `json:"items"`
	TotalCount int               `json:"total_count"`
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	Section    string            `json:"section"`
}

var employeeServiceMoney = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})\.[0-9]{2}$`)
var employeeServiceID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// NormalizeEmployeeServiceMoney preserves DECIMAL values without a float round trip.
func NormalizeEmployeeServiceMoney(raw string) (string, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) == 1 {
		raw += ".00"
	} else if len(parts) == 2 && len(parts[1]) == 1 {
		raw += "0"
	}
	return raw, employeeServiceMoney.MatchString(raw)
}

func WelfareStatusLabel(status string) string {
	return map[string]string{"submitted": "รอการเงินตรวจ", "returned": "ส่งกลับแก้ไข", "checked": "ตรวจแล้ว รออนุมัติ", "approved": "อนุมัติแล้ว รอจ่าย", "paid": "จ่ายแล้ว", "rejected": "ไม่อนุมัติ", "cancelled": "ยกเลิก"}[status]
}

func HousingStatusLabel(status, kind string) string {
	if kind == "expense" {
		return map[string]string{"unpaid": "ยังไม่ชำระ", "partial": "ชำระบางส่วน", "paid": "ชำระแล้ว"}[status]
	}
	return map[string]string{"draft": "ร่าง", "submitted": "ส่งคำขอแล้ว", "staff_review": "เจ้าหน้าที่ตรวจสอบ", "committee_review": "รอพิจารณา", "approved": "อนุมัติ", "rejected": "ไม่อนุมัติ", "allocated": "รอรับมอบ", "active": "เข้าอยู่แล้ว", "completed": "ดำเนินการเสร็จ", "cancelled": "ยกเลิก"}[status]
}

func employeeServiceDate(date string) bool {
	if date == "" {
		return true
	}
	d, err := time.Parse(time.DateOnly, date)
	return err == nil && d.Year() >= 1900 && d.Year() <= 2200
}

func employeeServicePage(count, page, perPage, length int, s EmployeeServiceSelection) bool {
	if count < 0 || page != s.Page || perPage != s.PerPage || length > perPage {
		return false
	}
	want := count - (page-1)*perPage
	if want < 0 {
		want = 0
	}
	if want > perPage {
		want = perPage
	}
	return length == want
}

func ValidWelfareHistory(v WelfareHistory, s EmployeeServiceSelection) bool {
	if v.Items == nil || !employeeServicePage(v.TotalCount, v.Page, v.PerPage, len(v.Items), s) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if (item.Kind != "education" && item.Kind != "medical") || (s.Kind != "all" && item.Kind != s.Kind) ||
			!strings.HasPrefix(item.ID, item.Kind+"-") || !employeeServiceID.MatchString(strings.TrimPrefix(item.ID, item.Kind+"-")) || seen[item.ID] ||
			len(item.Number) > 200 || len(item.Subject) > 2000 || !employeeServiceDate(item.Date) || WelfareStatusLabel(item.Status) == "" || item.StatusLabel != WelfareStatusLabel(item.Status) ||
			!employeeServiceMoney.MatchString(item.RequestedAmount) || (item.ApprovedAmount != nil && (!employeeServiceMoney.MatchString(*item.ApprovedAmount) || (item.Status != "approved" && item.Status != "paid"))) ||
			(item.Kind == "medical" && (item.AcademicYear != 0 || item.Term != 0)) ||
			(item.Kind == "education" && (item.AcademicYear < 2400 || item.AcademicYear > 2800 || item.Term < 1 || item.Term > 2)) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

func ValidHousingHistory(v HousingHistory, s EmployeeServiceSelection) bool {
	if v.Items == nil || v.Section != s.Section || !employeeServicePage(v.TotalCount, v.Page, v.PerPage, len(v.Items), s) {
		return false
	}
	if o := v.Occupancy; o != nil {
		if !employeeServiceID.MatchString(o.ID) || (o.Status != "allocated" && o.Status != "active") || o.StatusLabel != HousingStatusLabel(o.Status, "request") || !employeeServiceDate(o.StartDate) ||
			len(o.Building) > 2000 || len(o.Unit) > 1000 || len(o.Room) > 1000 {
			return false
		}
	}
	wantKind := "request"
	if s.Section == "expenses" {
		wantKind = "expense"
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if item.Kind != wantKind || !employeeServiceID.MatchString(item.ID) || seen[item.ID] || len(item.Number) > 1000 || !employeeServiceDate(item.Date) ||
			HousingStatusLabel(item.Status, item.Kind) == "" || item.StatusLabel != HousingStatusLabel(item.Status, item.Kind) || len(item.Building) > 2000 || len(item.Unit) > 1000 || len(item.Room) > 1000 {
			return false
		}
		for _, value := range []*string{item.TotalAmount, item.PaidAmount, item.BalanceAmount} {
			if (item.Kind == "request" && value != nil) || (item.Kind == "expense" && (value == nil || !employeeServiceMoney.MatchString(*value))) {
				return false
			}
		}
		seen[item.ID] = true
	}
	return true
}
