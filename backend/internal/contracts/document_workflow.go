package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type DocumentRecipient struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	GivenName    string `json:"given_name,omitempty"`
	PositionName string `json:"position_name,omitempty"`
}
type DocumentComment struct {
	ID                   string              `json:"id"`
	Text                 string              `json:"text"`
	AuthorName           string              `json:"author_name"`
	AuthorEmployeeID     string              `json:"author_employee_id,omitempty"`
	AuthorGivenName      string              `json:"author_given_name,omitempty"`
	AuthorDepartmentName string              `json:"author_department_name,omitempty"`
	CreatedAt            string              `json:"created_at"`
	UpdatedAt            string              `json:"updated_at"`
	Revision             string              `json:"revision"`
	IsOwn                bool                `json:"is_own"`
	CanEdit              bool                `json:"can_edit"`
	CanDelete            bool                `json:"can_delete"`
	Employees            []DocumentRecipient `json:"employees"`
	Departments          []DocumentRecipient `json:"departments"`
}
type DocumentComments struct {
	Items                  []DocumentComment `json:"items"`
	Page                   int               `json:"page"`
	PerPage                int               `json:"per_page"`
	TotalCount             int               `json:"total_count"`
	HasMore                bool              `json:"has_more"`
	CanCreate              bool              `json:"can_create"`
	WorkflowDisabledReason string            `json:"workflow_disabled_reason"`
}
type DocumentCommentOptions struct {
	Items                      []DocumentRecipient `json:"items"`
	Page                       int                 `json:"page"`
	HasMore                    bool                `json:"has_more"`
	DirectorID                 *string             `json:"director_id"`
	DirectorName               string              `json:"director_name"`
	SuggestedEmployeeIDs       []string            `json:"suggested_employee_ids"`
	WorkflowEnabled            bool                `json:"workflow_enabled"`
	CurrentUserIsDirector      bool                `json:"current_user_is_director"`
	NotificationAudienceNotice string              `json:"notification_audience_notice"`
}
type DocumentTemplate struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type DocumentTemplates struct {
	Items []DocumentTemplate `json:"items"`
}
type DocumentTimelineItem struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Label           string `json:"label"`
	ActorName       string `json:"actor_name"`
	ActorEmployeeID string `json:"actor_employee_id,omitempty"`
	ActorGivenName  string `json:"actor_given_name,omitempty"`
	CreatedAt       string `json:"created_at"`
	RecipientName   string `json:"recipient_name"`
	Text            string `json:"text"`
}
type DocumentTimeline struct {
	Items      []DocumentTimelineItem `json:"items"`
	Page       int                    `json:"page"`
	PerPage    int                    `json:"per_page"`
	TotalCount int                    `json:"total_count"`
	HasMore    bool                   `json:"has_more"`
}

type DocumentWorkflowSelection struct{ Page, PerPage int }

func DocumentWorkflowQuery(raw string) (DocumentWorkflowSelection, error) {
	s := DocumentWorkflowSelection{Page: 1, PerPage: 20}
	q, err := url.ParseQuery(raw)
	bad := errors.New("invalid workflow page")
	if err != nil || len(raw) > 2000 {
		return s, bad
	}
	for key, values := range q {
		if len(values) != 1 {
			return s, bad
		}
		switch key {
		case "page":
			s.Page, err = strconv.Atoi(values[0])
		case "per_page":
			s.PerPage, err = strconv.Atoi(values[0])
		default:
			return s, bad
		}
		if err != nil {
			return s, bad
		}
	}
	if s.Page < 1 || s.Page > 100000 || s.PerPage < 1 || s.PerPage > 100 {
		return s, bad
	}
	return s, nil
}

type DocumentCommentInput struct {
	Text             string   `json:"text"`
	EmployeeIDs      []string `json:"employee_ids"`
	DepartmentIDs    []string `json:"department_ids"`
	RequestID        string   `json:"request_id"`
	Revision         string   `json:"revision,omitempty"`
	OperationVersion int      `json:"operation_version,omitempty"`
}
type DocumentCommentResult struct {
	ID                 string `json:"id"`
	Deleted            bool   `json:"deleted"`
	NotificationStatus string `json:"notification_status"`
}
type DocumentOptionSelection struct {
	Query, Kind, CommentID string
	Page                   int
}

func DocumentOptionsQuery(raw string) (DocumentOptionSelection, error) {
	s := DocumentOptionSelection{Kind: "employee", Page: 1}
	q, e := url.ParseQuery(raw)
	bad := errors.New("invalid options")
	if e != nil || len(raw) > 2000 {
		return s, bad
	}
	for k, v := range q {
		if len(v) != 1 {
			return s, bad
		}
		switch k {
		case "q":
			s.Query = strings.TrimSpace(v[0])
		case "kind":
			s.Kind = v[0]
		case "comment_id":
			s.CommentID = v[0]
		case "page":
			s.Page, e = strconv.Atoi(v[0])
			if e != nil {
				return s, bad
			}
		default:
			return s, bad
		}
	}
	if utf8.RuneCountInString(s.Query) > 100 || s.Page < 1 || s.Page > 10000 || (s.Kind != "employee" && s.Kind != "department") || (s.CommentID != "" && !ValidMeetingID(s.CommentID)) {
		return s, bad
	}
	return s, nil
}
func ValidateDocumentComment(in DocumentCommentInput) bool {
	if in.OperationVersion < 0 || in.OperationVersion > 1 {
		return false
	}
	if len(in.Revision) > 64 {
		return false
	}
	for _, c := range in.Revision {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	if strings.TrimSpace(in.Text) == "" && len(in.EmployeeIDs)+len(in.DepartmentIDs) == 0 {
		return false
	}
	if utf8.RuneCountInString(in.Text) > 5000 || len(in.EmployeeIDs) > 100 || len(in.DepartmentIDs) > 100 || len(in.RequestID) < 16 || len(in.RequestID) > 80 {
		return false
	}
	for _, id := range append(append([]string{}, in.EmployeeIDs...), in.DepartmentIDs...) {
		if !ValidMeetingID(id) {
			return false
		}
	}
	for _, c := range in.RequestID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
