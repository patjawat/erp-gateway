package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
)

func (s *Server) documentWorkflow(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/documents/"), "/")
	if len(parts) < 2 {
		return false
	}
	kind := parts[1]
	if kind != "comments" && kind != "comment-options" && kind != "comment-templates" && kind != "timeline" {
		return false
	}
	id := parts[0]
	comment := ""
	if len(parts) == 3 && kind == "comments" {
		comment = parts[2]
	}
	if !contracts.ValidMeetingID(id) || (len(parts) != 2 && comment == "") || (comment != "" && !contracts.ValidMeetingID(comment)) {
		fail(w, 404, "DOCUMENT_NOT_FOUND", "ไม่พบรายการ")
		return true
	}
	allowed := r.Method == "GET" || kind == "comments" && (r.Method == "POST" && comment == "" || (r.Method == "PUT" || r.Method == "DELETE") && comment != "")
	if !allowed {
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับวิธีดำเนินการ")
		return true
	}
	var page contracts.DocumentWorkflowSelection
	if kind == "comment-options" {
		if _, e := contracts.DocumentOptionsQuery(r.URL.RawQuery); e != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกไม่ถูกต้อง")
			return true
		}
	} else if r.Method == "GET" && comment == "" && (kind == "comments" || kind == "timeline") {
		var err error
		page, err = contracts.DocumentWorkflowQuery(r.URL.RawQuery)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกไม่ถูกต้อง")
			return true
		}
	} else if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกไม่ถูกต้อง")
		return true
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return true
	}
	var v any
	var body []byte
	switch kind {
	case "comments":
		v = &contracts.DocumentComments{}
		if comment != "" {
			v = &contracts.DocumentComment{}
		}
	case "comment-options":
		v = &contracts.DocumentCommentOptions{}
	case "comment-templates":
		v = &contracts.DocumentTemplates{}
	case "timeline":
		v = &contracts.DocumentTimeline{}
	}
	if r.Method != "GET" {
		v = &contracts.DocumentCommentResult{}
		if r.Method == "DELETE" && (r.ContentLength != 0 || r.Header.Get("If-Match") != "") {
			var in contracts.DocumentCommentInput
			if r.ContentLength != 0 {
				if !decodeMeeting(w, r, &in) {
					return true
				}
			}
			if header := strings.Trim(r.Header.Get("If-Match"), "\""); header != "" {
				if in.Revision != "" && in.Revision != header {
					fail(w, 422, "VALIDATION_FAILED", "ข้อมูลไม่ถูกต้อง")
					return true
				}
				in.Revision = header
			}
			if len(in.Revision) != 64 || in.OperationVersion < 0 || in.OperationVersion > 1 || (in.OperationVersion == 1 && !contracts.ValidDocumentRequestID(in.RequestID)) {
				fail(w, 422, "VALIDATION_FAILED", "ข้อมูลความเห็นเปลี่ยนแล้ว กรุณาโหลดใหม่")
				return true
			}
			body, _ = json.Marshal(in)
		}
		if r.Method != "DELETE" {
			var in contracts.DocumentCommentInput
			if !decodeMeeting(w, r, &in) {
				return true
			}
			if !contracts.ValidateDocumentComment(in) {
				fail(w, 422, "VALIDATION_FAILED", "กรุณาตรวจสอบความเห็นและผู้รับ")
				return true
			}
			body, _ = json.Marshal(in)
		}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	res, e := s.upstream(r.Context(), h, r.Method, path, token, body)
	if e != nil {
		upstreamUnavailable(w)
		return true
	}
	if res.StatusCode != 200 {
		if res.StatusCode == 401 || res.StatusCode == 429 {
			s.relayError(w, res, false)
			return true
		}
		defer res.Body.Close()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&env)
		codes := map[string]int{"DOCUMENT_NOT_FOUND": 404, "READ_ONLY": 403, "FORBIDDEN": 403, "DOCUMENT_UNAVAILABLE": 503, "WORKFLOW_NOT_READY": 503, "WORKFLOW_CONFLICT": 409, "VALIDATION_FAILED": 422, "INVALID_REQUEST": 400}
		if codes[env.Error.Code] == res.StatusCode {
			fail(w, res.StatusCode, env.Error.Code, "ไม่สามารถดำเนินการกับความเห็นได้ กรุณาตรวจสอบอีกครั้ง")
		} else {
			upstreamUnavailable(w)
		}
		return true
	}
	if readEnvelope(res, v) != nil || !validDocumentWorkflow(v) {
		upstreamUnavailable(w)
		return true
	}
	if r.Method == "GET" && comment == "" && (kind == "comments" || kind == "timeline") {
		gotPage, gotPerPage := 0, 0
		switch data := v.(type) {
		case *contracts.DocumentComments:
			gotPage, gotPerPage = data.Page, data.PerPage
		case *contracts.DocumentTimeline:
			gotPage, gotPerPage = data.Page, data.PerPage
		}
		if gotPage != page.Page || gotPerPage != page.PerPage {
			upstreamUnavailable(w)
			return true
		}
	}
	if !s.stillActive(w, r, h) {
		return true
	}
	writeData(w, v)
	return true
}
func validDocumentRecipients(v []contracts.DocumentRecipient) bool {
	if v == nil || len(v) > 10000 {
		return false
	}
	for _, r := range v {
		if !contracts.ValidMeetingID(r.ID) || len(r.Name) > 2000 || len(r.GivenName) > 2000 || len(r.PositionName) > 2000 {
			return false
		}
	}
	return true
}
func validDocumentWorkflow(v any) bool {
	switch d := v.(type) {
	case *contracts.DocumentComment:
		return contracts.ValidMeetingID(d.ID) && (d.AuthorEmployeeID == "" || contracts.ValidMeetingID(d.AuthorEmployeeID)) && len(d.Text) <= 100000 && len(d.AuthorName) <= 2000 && len(d.AuthorGivenName) <= 2000 && len(d.AuthorDepartmentName) <= 2000 && len(d.Revision) <= 64 && validDocumentRecipients(d.Employees) && validDocumentRecipients(d.Departments) && (!(d.CanEdit || d.CanDelete) || d.IsOwn)
	case *contracts.DocumentComments:
		if d.Items == nil || !validDocumentWorkflowPage(d.Page, d.PerPage, d.TotalCount, len(d.Items), d.HasMore) {
			return false
		}
		seen := map[string]bool{}
		for _, c := range d.Items {
			if seen[c.ID] || !contracts.ValidMeetingID(c.ID) || (c.AuthorEmployeeID != "" && !contracts.ValidMeetingID(c.AuthorEmployeeID)) || len(c.Text) > 100000 || len(c.AuthorName) > 2000 || len(c.AuthorGivenName) > 2000 || len(c.AuthorDepartmentName) > 2000 || !validDocumentRecipients(c.Employees) || !validDocumentRecipients(c.Departments) || ((c.CanEdit || c.CanDelete) && !c.IsOwn) {
				return false
			}
			seen[c.ID] = true
		}
		return true
	case *contracts.DocumentCommentOptions:
		return d.Page > 0 && len(d.Items) <= 50 && validDocumentRecipients(d.Items) && d.SuggestedEmployeeIDs != nil && (d.DirectorID == nil || contracts.ValidMeetingID(*d.DirectorID))
	case *contracts.DocumentTemplates:
		if d.Items == nil || len(d.Items) > 200 {
			return false
		}
		for _, t := range d.Items {
			if !contracts.ValidMeetingID(t.ID) || len(t.Text) > 100000 {
				return false
			}
		}
		return true
	case *contracts.DocumentTimeline:
		if d.Items == nil || !validDocumentWorkflowPage(d.Page, d.PerPage, d.TotalCount, len(d.Items), d.HasMore) {
			return false
		}
		seen := map[string]bool{}
		for _, x := range d.Items {
			if (x.ActorEmployeeID != "" && !contracts.ValidMeetingID(x.ActorEmployeeID)) || seen[x.ID] || !contracts.ValidMeetingID(x.ID) || len(x.Text) > 100000 || len(x.ActorName) > 2000 || len(x.ActorGivenName) > 2000 || len(x.RecipientName) > 2000 {
				return false
			}
			seen[x.ID] = true
		}
		return true
	case *contracts.DocumentCommentResult:
		return contracts.ValidMeetingID(d.ID) && d.NotificationStatus == "not_requested"
	}
	return false
}

func validDocumentWorkflowPage(page, perPage, total, length int, hasMore bool) bool {
	return page >= 1 && page <= 100000 && perPage >= 1 && perPage <= 100 && total >= 0 && length <= perPage && length <= total &&
		(length == 0 || (page-1)*perPage+length <= total) && hasMore == (page*perPage < total) && !(length == 0 && hasMore)
}
