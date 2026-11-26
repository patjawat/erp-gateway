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

type SalarySelection struct{ Page, PageSize int }

func SalaryQuery(raw string) (SalarySelection, error) {
	s := SalarySelection{Page: 1, PageSize: 20}
	bad := errors.New("invalid salary query")
	q, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 100 {
		return s, bad
	}
	for key, values := range q {
		if len(values) != 1 || (key != "page" && key != "page_size") {
			return s, bad
		}
		n, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(n) != values[0] {
			return s, bad
		}
		if key == "page" {
			s.Page = n
		} else {
			s.PageSize = n
		}
	}
	if s.Page < 1 || s.Page > 10000 || s.PageSize < 1 || s.PageSize > 50 {
		return s, bad
	}
	return s, nil
}

type SalaryLine struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
}

type SalaryRecord struct {
	ID              string       `json:"id"`
	PeriodCode      string       `json:"period_code"`
	PeriodType      string       `json:"period_type"`
	PeriodTypeLabel string       `json:"period_type_label"`
	DateStart       string       `json:"date_start"`
	DateEnd         string       `json:"date_end"`
	PayDate         *string      `json:"pay_date"`
	GrossAmount     *string      `json:"gross_amount"`
	DeductionAmount *string      `json:"deduction_amount"`
	NetAmount       *string      `json:"net_amount"`
	Earnings        []SalaryLine `json:"earnings"`
	Deductions      []SalaryLine `json:"deductions"`
}

type SalaryHistory struct {
	Items    []SalaryRecord `json:"items"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
	HasMore  bool           `json:"has_more"`
}

var salaryDecimal = regexp.MustCompile(`^-?[0-9]{1,13}(?:\.[0-9]{1,2})?$`)
var salaryID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// SalaryDecimal keeps fixed-point values as strings throughout the API. It
// never routes payroll amounts through a binary floating-point conversion.
func SalaryDecimal(value string) (string, bool) {
	if !salaryDecimal.MatchString(value) {
		return "", false
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = (parts[1] + "00")[:2]
	}
	value = whole + "." + fraction
	if negative && value != "0.00" {
		value = "-" + value
	}
	return value, true
}

func SalaryTypeLabel(value string) string {
	switch value {
	case "salary":
		return "เงินเดือน"
	case "compensation":
		return "ค่าตอบแทน"
	case "overtime":
		return "OT"
	}
	return ""
}

func ValidSalaryHistory(value SalaryHistory, selection SalarySelection) bool {
	if value.Items == nil || value.Page != selection.Page || value.PageSize != selection.PageSize || value.Total < 0 ||
		len(value.Items) > selection.PageSize {
		return false
	}
	offset := (selection.Page - 1) * selection.PageSize
	want := value.Total - offset
	if want < 0 {
		want = 0
	}
	if want > selection.PageSize {
		want = selection.PageSize
	}
	if len(value.Items) != want || value.HasMore != (offset+len(value.Items) < value.Total) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range value.Items {
		month, err := time.Parse("2006-01", item.PeriodCode)
		if !salaryID.MatchString(item.ID) || seen[item.ID] || err != nil || month.Year() < 1900 || month.Year() > 2200 ||
			SalaryTypeLabel(item.PeriodType) == "" || item.PeriodTypeLabel != SalaryTypeLabel(item.PeriodType) ||
			!salaryDate(item.DateStart) || !salaryDate(item.DateEnd) || item.DateEnd < item.DateStart {
			return false
		}
		seen[item.ID] = true
		if item.PayDate != nil && !salaryDate(*item.PayDate) {
			return false
		}
		for _, amount := range []*string{item.GrossAmount, item.DeductionAmount, item.NetAmount} {
			if amount != nil {
				if canonical, ok := SalaryDecimal(*amount); !ok || canonical != *amount {
					return false
				}
			}
		}
		for _, lines := range [][]SalaryLine{item.Earnings, item.Deductions} {
			if lines == nil || len(lines) > 200 {
				return false
			}
			for _, line := range lines {
				canonical, ok := SalaryDecimal(line.Amount)
				if strings.TrimSpace(line.Name) == "" || utf8.RuneCountInString(line.Name) > 200 || !ok || canonical != line.Amount {
					return false
				}
			}
		}
	}
	return true
}

func salaryDate(value string) bool {
	t, err := time.Parse(time.DateOnly, value)
	return err == nil && t.Year() >= 1900 && t.Year() <= 2200 && t.Format(time.DateOnly) == value
}
