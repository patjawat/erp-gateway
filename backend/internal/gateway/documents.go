package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
)

func (s *Server) documents(w http.ResponseWriter, r *http.Request) {
	if s.documentNative(w, r) {
		return
	}
	if s.documentWorkflow(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/documents"), "/")
	id, file := "", ""
	read := false
	if len(parts) > 1 {
		if !contracts.ValidMeetingID(parts[1]) {
			fail(w, 404, "DOCUMENT_NOT_FOUND", "ไม่พบหนังสือ")
			return
		}
		id = parts[1]
		if len(parts) == 3 && parts[2] == "read" {
			read = true
		} else if len(parts) == 4 && parts[2] == "attachments" && contracts.ValidMeetingID(parts[3]) {
			file = parts[3]
		} else if len(parts) != 2 {
			fail(w, 404, "DOCUMENT_NOT_FOUND", "ไม่พบหนังสือ")
			return
		}
	}
	method := http.MethodGet
	if read {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	if _, e := contracts.DocumentQuery(r.URL.RawQuery); e != nil || (id != "" && r.URL.RawQuery != "") {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกหนังสือไม่ถูกต้อง")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var body []byte
	var output any = &contracts.DocumentHistory{}
	if id != "" {
		output = &contracts.DocumentDetail{}
	}
	if read {
		var in contracts.DocumentReadInput
		if !decodeMeeting(w, r, &in) {
			return
		}
		if !contracts.ValidMeetingID(in.RouteID) {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกหนังสือไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(in)
		output = &contracts.DocumentReadResult{}
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	res, e := s.upstream(r.Context(), h, method, path, token, body)
	if e != nil {
		documentGatewayDiagnostic("transport", 503, "UPSTREAM_UNAVAILABLE")
		upstreamUnavailable(w)
		return
	}
	if res.StatusCode != 200 {
		if res.StatusCode == 401 || res.StatusCode == 429 {
			s.relayError(w, res, false)
			return
		}
		defer res.Body.Close()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&env)
		documentGatewayDiagnostic("upstream", res.StatusCode, env.Error.Code)
		codes := map[string]int{"DOCUMENT_NOT_FOUND": 404, "READ_ONLY": 403, "FORBIDDEN": 403, "DOCUMENT_UNAVAILABLE": 503, "INVALID_REQUEST": 400}
		if codes[env.Error.Code] == res.StatusCode {
			fail(w, res.StatusCode, env.Error.Code, "ไม่สามารถดำเนินการกับหนังสือได้")
			return
		}
		upstreamUnavailable(w)
		return
	}
	if file != "" {
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, contracts.MaxDocumentBytes+1))
		m := http.DetectContentType(b)
		if e != nil || len(b) == 0 || len(b) > contracts.MaxDocumentBytes || (m != "application/pdf" && m != "image/png" && m != "image/jpeg" && m != "image/gif" && m != "image/webp") {
			upstreamUnavailable(w)
			return
		}
		if !s.stillActive(w, r, h) {
			return
		}
		w.Header().Set("Content-Type", m)
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
		return
	}
	if readEnvelope(res, output) != nil {
		documentGatewayDiagnostic("envelope", 503, "INVALID_RESPONSE")
		upstreamUnavailable(w)
		return
	}
	if !validDocuments(output) {
		documentGatewayDiagnostic("contract", 503, "INVALID_RESPONSE")
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}
func validDocument(d contracts.Document) bool {
	return contracts.ValidMeetingID(d.ID) && contracts.ValidMeetingID(d.RouteID) && len(d.Title) <= 10000 && len(d.Number) <= 1000 && len(d.ReceiveNumber) <= 1000 && len(d.Department) <= 2000 && len(d.Date) <= 64 && len(d.Speed) <= 255
}
func validDocuments(v any) bool {
	switch d := v.(type) {
	case *contracts.DocumentHistory:
		if d.Items == nil || d.PerPage < 1 || d.PerPage > 100 || len(d.Items) > d.PerPage || d.TotalCount < 0 || d.Summary.All < 0 || d.Summary.Read < 0 || d.Summary.Unread < 0 || d.Summary.Bookmarked < 0 || d.Summary.Bookmarked > d.Summary.All || d.Summary.All != d.Summary.Read+d.Summary.Unread {
			return false
		}
		for _, x := range d.Items {
			if !validDocument(x) {
				return false
			}
		}
		return true
	case *contracts.DocumentDetail:
		if !validDocument(d.Document) || len(d.Summary) > 100000 || d.Attachments == nil || len(d.Attachments) > 200 || d.AttachmentsTotal < 0 {
			return false
		}
		if d.AttachmentsTotal > 0 && (len(d.Attachments) > d.AttachmentsTotal || d.AttachmentsHasMore != (len(d.Attachments) < d.AttachmentsTotal)) {
			return false
		}
		if d.AttachmentsTotal == 0 && d.AttachmentsHasMore {
			return false
		}
		if d.PrimaryAttachment != nil && (!validDocumentAttachment(*d.PrimaryAttachment) || !d.PrimaryAttachment.IsPrimary || d.PrimaryAttachment.MimeType != "application/pdf") {
			return false
		}
		for _, a := range d.Attachments {
			if !validDocumentAttachment(a) {
				return false
			}
		}
		return true
	case *contracts.DocumentReadResult:
		return d.IsRead
	}
	return false
}

func validDocumentAttachment(a contracts.DocumentAttachment) bool {
	return contracts.ValidMeetingID(a.ID) && len(a.Name) <= 1000 && len(a.MimeType) <= 200 && (!a.HasOriginal || a.MimeType == "application/pdf")
}
