package contracts

import "encoding/json"

const Version = "1"

type Hospital struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Province string `json:"province"`
}

type Employee struct {
	ID         json.RawMessage `json:"id"`
	Fullname   string          `json:"fullname"`
	Department string          `json:"department"`
	Position   *string         `json:"position"`
	Email      string          `json:"email"`
	Phone      string          `json:"phone"`
	Address    string          `json:"address"`
}

type Profile struct {
	ID                json.RawMessage `json:"id"`
	Username          string          `json:"username"`
	Email             string          `json:"email"`
	Employee          *Employee       `json:"employee"`
	AvatarURL         *string         `json:"avatar_url"`
	Hospital          *Hospital       `json:"hospital,omitempty"`
	ProfileEditable   bool            `json:"profile_editable"`
	SignatureEditable bool            `json:"signature_editable"`
	SignatureURL      *string         `json:"signature_url"`
}

type Login struct {
	HospitalCode string `json:"hospital_code,omitempty"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type Token struct {
	UserID      json.RawMessage `json:"user_id"`
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	ExpiresAt   int64           `json:"expires_at"`
	Scope       string          `json:"scope"`
}

type Status struct {
	HospitalCode string `json:"hospital_code"`
	APIVersion   string `json:"api_version"`
	ERPReachable bool   `json:"erp_reachable"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Leave contains only the fields needed to display the signed-in employee's
// leave history. ERP JSON documents, signatures and contact data stay private.
type Leave struct {
	Attachments             []LeaveAttachment       `json:"attachments,omitempty"`
	AttachmentCount         int                     `json:"attachment_count"`
	ID                      string                  `json:"id"`
	LeaveTypeID             string                  `json:"leave_type_id"`
	FiscalYear              *int                    `json:"fiscal_year"`
	LeaveType               string                  `json:"leave_type"`
	StartDate               string                  `json:"start_date"`
	EndDate                 string                  `json:"end_date"`
	StartTime               string                  `json:"start_time"`
	EndTime                 string                  `json:"end_time"`
	TotalDays               float64                 `json:"total_days"`
	Status                  string                  `json:"status"`
	StatusLabel             string                  `json:"status_label"`
	Reason                  string                  `json:"reason"`
	CreatedAt               string                  `json:"created_at"`
	Period                  string                  `json:"period"`
	TravelScope             string                  `json:"travel_scope"`
	Comments                string                  `json:"comments"`
	ReplacementEmployeeID   string                  `json:"replacement_employee_id"`
	ReplacementEmployeeName string                  `json:"replacement_employee_name"`
	VerificationSteps       []LeaveVerificationStep `json:"verification_steps"`
	DaySummary              *LeaveDaySummary        `json:"day_summary"`
}

// LeaveDaySummary describes the saved date range without recalculating the
// persisted leave total. HolidayDays excludes holidays on weekends.
type LeaveDaySummary struct {
	CalendarDays     int     `json:"calendar_days"`
	WeekendDays      int     `json:"weekend_days"`
	HolidayDays      int     `json:"holiday_days"`
	TotalDays        float64 `json:"total_days"`
	HolidaysDeducted *bool   `json:"holidays_deducted"`
}

// LeaveVerificationStep describes a saved approval record, rather than the
// approval settings that would apply to a newly submitted request.
type LeaveVerificationStep struct {
	ID           string `json:"id"`
	Level        int    `json:"level"`
	Title        string `json:"title"`
	Label        string `json:"label"`
	EmployeeID   string `json:"employee_id"`
	EmployeeName string `json:"employee_name"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	StatusLabel  string `json:"status_label"`
	ReviewedAt   string `json:"reviewed_at"`
	Comment      string `json:"comment"`
}

type LeaveHistory struct {
	Items          []Leave                    `json:"items"`
	Page           int                        `json:"page"`
	PerPage        int                        `json:"per_page"`
	TotalCount     int                        `json:"total_count"`
	HasMore        bool                       `json:"has_more"`
	FilterOptions  *LeaveHistoryFilterOptions `json:"filter_options,omitempty"`
	MonthSummaries []LeaveHistoryMonthSummary `json:"month_summaries,omitempty"`
}

type LeaveHistoryFilterOptions struct {
	FiscalYears []int       `json:"fiscal_years"`
	LeaveTypes  []LeaveType `json:"leave_types"`
}
