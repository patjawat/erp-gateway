package contracts

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const NotificationPageSize = 20
const NotificationBatchSize = 50

var notificationID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var installationID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)

type Notification struct {
	ID        string             `json:"id"`
	Kind      string             `json:"kind"`
	Title     string             `json:"title"`
	Message   string             `json:"message"`
	CreatedAt string             `json:"created_at"`
	IsRead    bool               `json:"is_read"`
	Target    NotificationTarget `json:"target"`
}
type NotificationTarget struct {
	Service string `json:"service"`
	ID      string `json:"id"`
	Action  string `json:"action"`
}
type NotificationSummary struct {
	Enabled     bool   `json:"enabled"`
	PushEnabled bool   `json:"push_enabled"`
	UnreadCount int    `json:"unread_count"`
	Snapshot    string `json:"snapshot"`
}
type NotificationPage struct {
	Items       []Notification `json:"items"`
	NextCursor  string         `json:"next_cursor"`
	HasMore     bool           `json:"has_more"`
	UnreadCount int            `json:"unread_count"`
	Snapshot    string         `json:"snapshot"`
}
type NotificationSelection struct {
	Before, Snapshot int64
	Unread           bool
	Service          string
}

func ParseNotificationSelection(raw string) (NotificationSelection, error) {
	v, e := url.ParseQuery(raw)
	s := NotificationSelection{}
	if e != nil {
		return s, errors.New("invalid selection")
	}
	for k, vs := range v {
		if len(vs) != 1 || (k != "cursor" && k != "status" && k != "service") {
			return s, errors.New("invalid selection")
		}
	}
	if x := v.Get("status"); x != "" && x != "all" && x != "unread" {
		return s, errors.New("invalid status")
	}
	s.Unread = v.Get("status") == "unread"
	s.Service = v.Get("service")
	if s.Service != "" && s.Service != "leave" && s.Service != "documents" {
		return s, errors.New("invalid service")
	}
	if c := v.Get("cursor"); c != "" {
		if len(c) > 150 {
			return s, errors.New("invalid cursor")
		}
		b, e := base64.RawURLEncoding.Strict().DecodeString(c)
		if e != nil {
			return s, e
		}
		p := strings.Split(string(b), ":")
		if len(p) != 4 || p[2] != strconv.FormatBool(s.Unread) || p[3] != s.Service {
			return s, errors.New("cursor filter changed")
		}
		s.Snapshot, e = strconv.ParseInt(p[0], 10, 64)
		if e != nil || s.Snapshot < 1 {
			return s, errors.New("invalid snapshot")
		}
		s.Before, e = strconv.ParseInt(p[1], 10, 64)
		if e != nil || s.Before < 1 || s.Before > s.Snapshot {
			return s, errors.New("invalid before")
		}
	}
	return s, nil
}
func NotificationCursor(snapshot, before int64, s NotificationSelection) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(snapshot, 10) + ":" + strconv.FormatInt(before, 10) + ":" + strconv.FormatBool(s.Unread) + ":" + s.Service))
}
func ValidNotificationID(s string) bool {
	_, e := strconv.ParseInt(s, 10, 64)
	return notificationID.MatchString(s) && e == nil
}
func ValidInstallationID(s string) bool { return installationID.MatchString(s) }
func ValidNotification(v Notification) bool {
	return ValidNotificationID(v.ID) && v.Title != "" && len(v.Title) < 300 && len(v.Message) < 1000 && v.CreatedAt != "" && ValidNotificationTarget(v.Target)
}
func ValidNotificationTarget(t NotificationTarget) bool {
	return ValidNotificationID(t.ID) && ((t.Service == "leave" && (t.Action == "detail" || t.Action == "approval")) || (t.Service == "documents" && t.Action == "detail"))
}

type NotificationPushItem struct {
	ID           string       `json:"id"`
	UserID       string       `json:"user_id"`
	UnreadCount  int          `json:"unread_count"`
	Notification Notification `json:"notification"`
}
type NotificationPushBatch struct {
	Items []NotificationPushItem `json:"items"`
}
type NotificationAck struct {
	IDs []string `json:"ids"`
}
type NotificationReadInput struct {
	Snapshot string `json:"snapshot"`
}
type NotificationDeviceInput struct {
	InstallationID string `json:"installation_id"`
	FID            string `json:"fid"`
	Platform       string `json:"platform"`
}

type NotificationLeaveDetail struct {
	ID            string  `json:"id"`
	LeaveType     string  `json:"leave_type"`
	StartDate     string  `json:"start_date"`
	EndDate       string  `json:"end_date"`
	Status        string  `json:"status"`
	TotalDays     float64 `json:"total_days"`
	RequesterName string  `json:"requester_name"`
	ApprovalOnly  bool    `json:"approval_only"`
}
