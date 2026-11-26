package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type DocumentSelection struct {
	Query, Status, Period string
	Year, Page, PerPage   int
}

func DocumentQuery(raw string) (DocumentSelection, error) {
	s := DocumentSelection{Status: "all", Page: 1, PerPage: 20}
	bad := errors.New("invalid document query")
	q, e := url.ParseQuery(raw)
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
		case "status":
			s.Status = v[0]
		case "period":
			switch v[0] {
			case "this_week", "this_month", "last_month", "this_year":
				s.Period = v[0]
			default:
				return s, bad
			}
		case "year", "page", "per_page":
			n, e := strconv.Atoi(v[0])
			if e != nil {
				return s, bad
			}
			switch k {
			case "year":
				s.Year = n
			case "page":
				s.Page = n
			case "per_page":
				s.PerPage = n
			}
		default:
			return s, bad
		}
	}
	if utf8.RuneCountInString(s.Query) > 200 || (s.Year != 0 && (s.Year < 2400 || s.Year > 2800)) || s.Page < 1 || s.Page > 100000 || s.PerPage < 1 || s.PerPage > 100 || (s.Status != "all" && s.Status != "read" && s.Status != "unread" && s.Status != "bookmarked") {
		return s, bad
	}
	return s, nil
}

type DocumentSummary struct {
	All        int `json:"all"`
	Unread     int `json:"unread"`
	Read       int `json:"read"`
	Bookmarked int `json:"bookmarked"`
}
type Document struct {
	ID            string `json:"id"`
	RouteID       string `json:"route_id"`
	Number        string `json:"number"`
	ReceiveNumber string `json:"receive_number"`
	Title         string `json:"title"`
	Department    string `json:"department"`
	Date          string `json:"date"`
	IsRead        bool   `json:"is_read"`
	Bookmark      bool   `json:"bookmark"`
	Speed         string `json:"speed"`
}
type DocumentHistory struct {
	Items      []Document      `json:"items"`
	TotalCount int             `json:"total_count"`
	Page       int             `json:"page"`
	PerPage    int             `json:"per_page"`
	Summary    DocumentSummary `json:"summary"`
}
type DocumentAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	IsPrimary   bool   `json:"is_primary"`
	HasOriginal bool   `json:"has_original"`
}
type DocumentDetail struct {
	Document
	ReceivedDate       string               `json:"received_date"`
	Summary            string               `json:"summary"`
	Attachments        []DocumentAttachment `json:"attachments"`
	CanMarkRead        bool                 `json:"can_mark_read"`
	RouteRead          bool                 `json:"route_read"`
	AttachmentsTotal   int                  `json:"attachments_total"`
	AttachmentsHasMore bool                 `json:"attachments_has_more"`
	PrimaryAttachment  *DocumentAttachment  `json:"primary_attachment,omitempty"`
}
type DocumentReadInput struct {
	RouteID string `json:"route_id"`
}
type DocumentReadResult struct {
	IsRead bool `json:"is_read"`
}

const MaxDocumentBytes = 20 * 1024 * 1024
