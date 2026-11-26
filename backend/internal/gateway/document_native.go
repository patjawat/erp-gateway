package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"mime"
	"net/http"
)

func (s *Server) documentNative(w http.ResponseWriter, r *http.Request) bool {
	route, matched, e := contracts.ParseNativeDocumentRoute(r.URL.Path, r.Method, r.URL.RawQuery)
	if !matched {
		return false
	}
	if e != nil {
		fail(w, 400, "INVALID_REQUEST", "คำสั่งหนังสือไม่ถูกต้อง")
		return true
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return true
	}
	var body []byte
	var output any = &contracts.DocumentNativePage{}
	switch route.Kind {
	case "workspace":
		output = &contracts.DocumentWorkspace{}
	case "resolve-route":
		output = &contracts.DocumentDetail{}
	case "operations":
		output = &contracts.DocumentNativeResult{}
	}
	if r.Method != "GET" {
		var in contracts.DocumentNativeInput
		if !decodeMeeting(w, r, &in) {
			return true
		}
		if !contracts.ValidDocumentNativeInput(route.Kind, r.Method, in) {
			fail(w, 422, "VALIDATION_FAILED", "กรุณาตรวจสอบข้อมูลก่อนบันทึก")
			return true
		}
		body, _ = json.Marshal(in)
		output = &contracts.DocumentNativeResult{}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	res, e := s.upstream(r.Context(), h, r.Method, path, token, body)
	if e != nil {
		documentGatewayDiagnostic("native_transport", 503, "UPSTREAM_UNAVAILABLE")
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
		documentGatewayDiagnostic("native_upstream", res.StatusCode, env.Error.Code)
		codes := map[string]int{"DOCUMENT_NOT_FOUND": 404, "READ_ONLY": 403, "FORBIDDEN": 403, "DOCUMENT_UNAVAILABLE": 503, "WORKFLOW_NOT_READY": 503, "WORKFLOW_CONFLICT": 409, "VALIDATION_FAILED": 422, "INVALID_REQUEST": 400}
		if codes[env.Error.Code] == res.StatusCode {
			fail(w, res.StatusCode, env.Error.Code, "ไม่สามารถดำเนินการกับหนังสือได้ กรุณาตรวจสอบอีกครั้ง")
		} else {
			upstreamUnavailable(w)
		}
		return true
	}
	if route.Kind == "files" {
		defer res.Body.Close()
		b, er := io.ReadAll(io.LimitReader(res.Body, contracts.MaxDocumentBytes+1))
		m, _, parseErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if er != nil || parseErr != nil || !contracts.DocumentNativeBytesMatch(m, b) {
			upstreamUnavailable(w)
			return true
		}
		disposition, params, er := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
		if er != nil || (disposition != "attachment" && disposition != "inline") || len(params["filename"]) > 1000 {
			upstreamUnavailable(w)
			return true
		}
		preview := m == "application/pdf" || m == "image/png" || m == "image/jpeg" || m == "image/gif" || m == "image/webp"
		if route.Download || !preview {
			disposition = "attachment"
		}
		if !s.stillActive(w, r, h) {
			return true
		}
		w.Header().Set("Content-Type", m)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, params))
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
		return true
	}
	if readEnvelope(res, output) != nil || !validDocumentNative(output, route) {
		upstreamUnavailable(w)
		return true
	}
	if !s.stillActive(w, r, h) {
		return true
	}
	writeData(w, output)
	return true
}

func validDocumentNative(v any, route contracts.DocumentNativeRoute) bool {
	switch d := v.(type) {
	case *contracts.DocumentWorkspace:
		return len(d.Status) <= 100 && len(d.Type) <= 255 && len(d.Speed) <= 255 && len(d.Secret) <= 255 && d.CommentCount >= 0 && d.ReadCount >= 0 && (!(d.CanRequestApproval || d.CanCreateTask) || d.CanWrite) && (!d.CanRequestApproval || d.Status == "DS1")
	case *contracts.DocumentDetail:
		return validDocuments(d)
	case *contracts.DocumentNativeResult:
		if !contracts.ValidMeetingID(d.ID) || d.NotificationStatus != "not_requested" || d.Number < 0 || d.Count < 0 || len(d.IDs) > 100 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range d.IDs {
			if !contracts.ValidMeetingID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
		switch route.Kind {
		case "registrations":
			return !d.Deleted && d.Number > 0 && d.Year >= 2400 && d.Year <= 2800
		case "tasks":
			return !d.Deleted && d.Count >= 1 && d.Count <= 100 && d.Count == len(d.IDs) && d.ID == d.IDs[0]
		case "templates", "routes":
			if route.Target != "" {
				return d.Deleted && d.ID == route.Target
			}
			return !d.Deleted
		case "bookmark", "approval-requests":
			return !d.Deleted
		}
		return true
	case *contracts.DocumentNativePage:
		if d.Items == nil || d.Page != route.Selection.Page || d.PerPage != route.Selection.PerPage || !validDocumentWorkflowPage(d.Page, d.PerPage, d.TotalCount, len(d.Items), d.HasMore) {
			return false
		}
		seen := map[string]bool{}
		for _, item := range d.Items {
			if !contracts.ValidMeetingID(item.ID) || seen[item.ID] || len(item.Name) > 20000 || len(item.Text) > 100000 || len(item.Revision) > 64 || len(item.Kind) > 100 || len(item.MimeType) > 200 || len(item.EmployeeName) > 2000 || len(item.GivenName) > 2000 || item.Number < 0 || item.SizeBytes < 0 || item.SizeBytes > contracts.MaxDocumentBytes || item.EmployeeID != "" && !contracts.ValidMeetingID(item.EmployeeID) {
				return false
			}
			if item.IsSuggested && (route.Kind != "task-options" || item.Kind != "unit") {
				return false
			}
			seen[item.ID] = true
		}
		return true
	}
	return false
}
