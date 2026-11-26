package contracts

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Native document endpoints never expose ERP JSON, storage paths or tenant selectors.
type DocumentWorkspace struct {
	Status             string `json:"status"`
	Type               string `json:"document_type"`
	Speed              string `json:"speed"`
	Secret             string `json:"secret"`
	Bookmark           bool   `json:"bookmark"`
	CanBookmark        bool   `json:"can_bookmark"`
	CanWrite           bool   `json:"can_write"`
	CanRequestApproval bool   `json:"can_request_approval"`
	CanRegister        bool   `json:"can_register"`
	CanCreateTask      bool   `json:"can_create_task"`
	CommentCount       int    `json:"comment_count"`
	ReadCount          int    `json:"read_count"`
	RouteRead          bool   `json:"route_read"`
}

type DocumentNativeItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Text         string `json:"text,omitempty"`
	Kind         string `json:"kind,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	Revision     string `json:"revision,omitempty"`
	CanDelete    bool   `json:"can_delete"`
	CanAssign    bool   `json:"can_assign"`
	IsSuggested  bool   `json:"is_suggested,omitempty"`
	EmployeeID   string `json:"employee_id,omitempty"`
	EmployeeName string `json:"employee_name,omitempty"`
	GivenName    string `json:"given_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	IsPrimary    bool   `json:"is_primary"`
	HasOriginal  bool   `json:"has_original"`
	Number       int    `json:"number,omitempty"`
	Year         int    `json:"year,omitempty"`
}
type DocumentNativePage struct {
	Items      []DocumentNativeItem `json:"items"`
	Page       int                  `json:"page"`
	PerPage    int                  `json:"per_page"`
	TotalCount int                  `json:"total_count"`
	HasMore    bool                 `json:"has_more"`
}
type DocumentTaskTarget struct {
	UnitID     string `json:"unit_id"`
	EmployeeID string `json:"employee_id,omitempty"`
}
type DocumentNativeInput struct {
	Text      string               `json:"text,omitempty"`
	RequestID string               `json:"request_id,omitempty"`
	Revision  string               `json:"revision,omitempty"`
	TargetID  string               `json:"target_id,omitempty"`
	Bookmark  *bool                `json:"bookmark,omitempty"`
	Title     string               `json:"title,omitempty"`
	Detail    string               `json:"detail,omitempty"`
	Priority  string               `json:"priority,omitempty"`
	DueDate   string               `json:"due_date,omitempty"`
	Units     []DocumentTaskTarget `json:"units,omitempty"`
}
type DocumentNativeResult struct {
	ID                 string   `json:"id"`
	IDs                []string `json:"ids,omitempty"`
	Count              int      `json:"count,omitempty"`
	Deleted            bool     `json:"deleted"`
	Existing           bool     `json:"existing"`
	Number             int      `json:"number"`
	Year               int      `json:"year"`
	NotificationStatus string   `json:"notification_status"`
}
type DocumentNativeSelection struct {
	Page, PerPage   int
	Query, TargetID string
}

func DocumentNativeQuery(raw string) (DocumentNativeSelection, error) {
	s := DocumentNativeSelection{Page: 1, PerPage: 20}
	q, e := url.ParseQuery(raw)
	bad := errors.New("invalid document page")
	if e != nil || len(raw) > 2000 {
		return s, bad
	}
	for k, v := range q {
		if len(v) != 1 {
			return s, bad
		}
		switch k {
		case "page":
			s.Page, e = strconv.Atoi(v[0])
		case "per_page":
			s.PerPage, e = strconv.Atoi(v[0])
		case "q":
			s.Query = strings.TrimSpace(v[0])
		case "unit_id":
			s.TargetID = v[0]
			if !ValidMeetingID(s.TargetID) {
				return s, bad
			}
		default:
			return s, bad
		}
		if e != nil {
			return s, bad
		}
	}
	if s.Page < 1 || s.Page > 100000 || s.PerPage < 1 || s.PerPage > 100 || utf8.RuneCountInString(s.Query) > 100 {
		return s, bad
	}
	return s, nil
}
func ValidDocumentRequestID(v string) bool {
	if len(v) < 16 || len(v) > 80 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type DocumentNativeRoute struct {
	DocumentID, Kind, Target string
	Selection                DocumentNativeSelection
	Original, Download       bool
}

// ParseNativeDocumentRoute is shared by both servers so method/query rules cannot drift.
func ParseNativeDocumentRoute(path, method, raw string) (DocumentNativeRoute, bool, error) {
	v := DocumentNativeRoute{}
	parts := strings.Split(strings.TrimPrefix(path, "/api/documents/"), "/")
	if len(parts) < 2 {
		return v, false, nil
	}
	v.DocumentID = parts[0]
	v.Kind = parts[1]
	switch v.Kind {
	case "workspace", "attachments", "readers", "recipients", "templates", "routes", "forwarding-history", "register-options", "registrations", "task-options", "task-people", "tasks", "bookmark", "approval-requests", "files", "resolve-route", "operations":
	default:
		return v, false, nil
	}
	// Keep the existing attachment binary endpoint backward compatible.
	if v.Kind == "attachments" && len(parts) == 3 {
		return v, false, nil
	}
	bad := errors.New("invalid native document request")
	if !ValidMeetingID(v.DocumentID) || len(parts) > 3 {
		return v, true, bad
	}
	if len(parts) == 3 {
		v.Target = parts[2]
		if v.Kind == "operations" {
			if !ValidDocumentRequestID(v.Target) {
				return v, true, bad
			}
		} else if !ValidMeetingID(v.Target) {
			return v, true, bad
		}
	}
	allowed := false
	collection := false
	switch v.Kind {
	case "workspace", "resolve-route":
		allowed = method == "GET" && v.Target == ""
	case "files":
		allowed = method == "GET" && v.Target != ""
	case "operations":
		allowed = method == "GET" && v.Target != ""
	case "bookmark":
		allowed = method == "PUT" && v.Target == ""
	case "approval-requests":
		allowed = method == "POST" && v.Target == ""
	case "templates":
		allowed = (method == "GET" || method == "POST") && v.Target == "" || method == "DELETE" && v.Target != ""
		collection = method == "GET"
	case "routes":
		allowed = method == "GET" && v.Target == "" || method == "DELETE" && v.Target != ""
		collection = method == "GET"
	case "registrations", "tasks":
		allowed = (method == "GET" || method == "POST") && v.Target == ""
		collection = method == "GET"
	default:
		allowed = method == "GET" && v.Target == ""
		collection = true
	}
	if !allowed {
		return v, true, bad
	}
	if v.Kind == "files" {
		q, e := url.ParseQuery(raw)
		if e != nil || len(raw) > 200 {
			return v, true, bad
		}
		for k, x := range q {
			if len(x) != 1 {
				return v, true, bad
			}
			switch k {
			case "variant":
				if x[0] != "current" && x[0] != "original" {
					return v, true, bad
				}
				v.Original = x[0] == "original"
			case "download":
				if x[0] != "0" && x[0] != "1" {
					return v, true, bad
				}
				v.Download = x[0] == "1"
			default:
				return v, true, bad
			}
		}
	} else if collection {
		var e error
		v.Selection, e = DocumentNativeQuery(raw)
		if e != nil {
			return v, true, e
		}
		if v.Selection.TargetID != "" && v.Kind != "task-people" {
			return v, true, bad
		}
	} else if raw != "" {
		return v, true, bad
	}
	return v, true, nil
}

func ValidDocumentNativeInput(kind, method string, in DocumentNativeInput) bool {
	if len(in.Text) > 20000 || len(in.Title) > 1000 || len(in.Detail) > 20000 || len(in.Revision) > 64 || len(in.TargetID) > 20 || len(in.DueDate) > 10 || len(in.Units) > 100 {
		return false
	}
	if kind != "bookmark" && in.Bookmark != nil {
		return false
	}
	if kind != "tasks" && (in.Title != "" || in.Detail != "" || in.Priority != "" || in.DueDate != "" || len(in.Units) > 0) {
		return false
	}
	if kind != "registrations" && in.TargetID != "" {
		return false
	}
	if kind != "templates" && in.Text != "" {
		return false
	}
	if method != "DELETE" && in.Revision != "" {
		return false
	}
	if kind == "bookmark" {
		return in.Bookmark != nil && in.RequestID == ""
	}
	if !ValidDocumentRequestID(in.RequestID) {
		return false
	}
	if method == "DELETE" {
		if len(in.Revision) != 64 {
			return false
		}
		_, e := hex.DecodeString(in.Revision)
		return e == nil
	}
	switch kind {
	case "templates":
		return strings.TrimSpace(in.Text) != "" && utf8.RuneCountInString(in.Text) <= 5000
	case "registrations":
		return ValidMeetingID(in.TargetID)
	case "tasks":
		return strings.TrimSpace(in.Title) != "" && utf8.RuneCountInString(in.Title) <= 255 && len(in.Units) > 0 && (in.Priority == "normal" || in.Priority == "urgent")
	case "approval-requests":
		return true
	}
	return false
}

func DocumentNativeBytesMatch(m string, b []byte) bool {
	if len(b) == 0 || len(b) > MaxDocumentBytes {
		return false
	}
	sniff := http.DetectContentType(b)
	switch m {
	case "application/octet-stream":
		// Stored types without a native preview are download-only. Both HTTP
		// servers force attachment disposition for this MIME, including HTML.
		return true
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp":
		return sniff == m
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/zip":
		return bytes.HasPrefix(b, []byte{'P', 'K', 3, 4}) || bytes.HasPrefix(b, []byte{'P', 'K', 5, 6})
	case "application/msword", "application/vnd.ms-excel", "application/vnd.ms-powerpoint":
		return bytes.HasPrefix(b, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	case "application/vnd.rar":
		return bytes.HasPrefix(b, []byte("Rar!\x1a\x07"))
	}
	return false
}
