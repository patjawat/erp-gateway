package contracts

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type RepairPermissions struct {
	CanDownload      bool   `json:"can_download"`
	CanEdit          bool   `json:"can_edit"`
	CanFeedback      bool   `json:"can_feedback"`
	CanUpload        bool   `json:"can_upload"`
	AttachmentReason string `json:"attachment_reason"`
}
type RepairEvent struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}
type RepairAttachment struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	MimeType    string `json:"mime_type"`
	DownloadURL string `json:"download_url"`
}
type RepairFeedback struct {
	Rating    int    `json:"rating"`
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}
type RepairDetail struct {
	Record             Repair                      `json:"record"`
	Permissions        RepairPermissions           `json:"permissions"`
	Timeline           []RepairEvent               `json:"timeline"`
	TimelineHasMore    bool                        `json:"timeline_has_more"`
	Attachments        []RepairAttachment          `json:"attachments"`
	AttachmentsHasMore bool                        `json:"attachments_has_more"`
	Feedback           RepairFeedback              `json:"feedback"`
	TimelinePage       *ReadPage[RepairEvent]      `json:"timeline_page,omitempty"`
	AttachmentsPage    *ReadPage[RepairAttachment] `json:"attachments_page,omitempty"`
}
type RepairEdit struct {
	Title             string `json:"title"`
	Location          string `json:"location"`
	Phone             string `json:"phone"`
	Urgency           string `json:"urgency"`
	Problem           string `json:"problem,omitempty"`
	Note              string `json:"note"`
	RequestRepairDate string `json:"request_repair_date"`
}
type RepairFeedbackInput struct {
	Rating  string `json:"rating"`
	Comment string `json:"comment"`
}

func ValidateRepairEdit(v *RepairEdit) error {
	if v == nil {
		return errors.New("invalid edit")
	}
	for _, f := range []struct {
		p        *string
		max      int
		required bool
	}{
		{&v.Title, 255, true}, {&v.Location, 1000, true}, {&v.Phone, 100, true}, {&v.Urgency, 8, true}, {&v.Problem, 4000, false}, {&v.Note, 4000, false}, {&v.RequestRepairDate, 10, false},
	} {
		*f.p = strings.TrimSpace(*f.p)
		if !utf8.ValidString(*f.p) || utf8.RuneCountInString(*f.p) > f.max || strings.ContainsRune(*f.p, 0) || (f.required && *f.p == "") {
			return errors.New("invalid edit")
		}
	}
	switch v.Urgency {
	case "low", "medium", "high", "critical":
	default:
		return errors.New("invalid urgency")
	}
	if v.RequestRepairDate != "" {
		if _, err := time.Parse(time.DateOnly, v.RequestRepairDate); err != nil {
			return err
		}
	}
	return nil
}
func ValidateRepairFeedback(v *RepairFeedbackInput) error {
	if v == nil {
		return errors.New("invalid feedback")
	}
	v.Comment = strings.TrimSpace(v.Comment)
	if len(v.Rating) != 1 || v.Rating < "1" || v.Rating > "5" || !utf8.ValidString(v.Comment) || utf8.RuneCountInString(v.Comment) > 2000 || strings.ContainsRune(v.Comment, 0) {
		return errors.New("invalid feedback")
	}
	return nil
}
