package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"unicode/utf8"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) repairReadPage(w http.ResponseWriter, r *http.Request, h Hospital, token, id, kind string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	selection, rest, err := contracts.ReadPagination(r.URL.RawQuery)
	if err != nil || len(rest) != 0 {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกประวัติแจ้งซ่อมไม่ถูกต้อง")
		return
	}
	selection.Explicit = true // New child routes always require a real paged response.
	path := "/api/repairs/" + id + "/" + kind
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound {
			defer response.Body.Close()
			var env struct {
				Error contracts.Failure `json:"error"`
			}
			_ = json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&env)
			if env.Error.Code == "REPAIR_NOT_FOUND" {
				fail(w, 404, "REPAIR_NOT_FOUND", "ไม่พบรายการแจ้งซ่อม")
				return
			}
			upstreamUnavailable(w)
			return
		}
		s.relayRepairError(w, response)
		return
	}
	var output any
	valid := false
	if kind == "timeline" {
		page := &contracts.ReadPage[contracts.RepairEvent]{}
		output = page
		valid = readEnvelope(response, page) == nil && page.Items != nil && contracts.ValidReadPage(page.ReadPageMetadata, len(page.Items), selection) && validRepairEvents(page.Items)
	} else {
		page := &contracts.ReadPage[contracts.RepairAttachment]{}
		output = page
		valid = readEnvelope(response, page) == nil && page.Items != nil && contracts.ValidReadPage(page.ReadPageMetadata, len(page.Items), selection) && validRepairAttachments(page.Items, id)
	}
	if !valid {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, output)
}

func validRepairEvents(items []contracts.RepairEvent) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if !repairPathID.MatchString(item.ID) || seen[item.ID] || !utf8.ValidString(item.Title) || len(item.Title) > 16000 || len(item.Status) > 255 || len(item.CreatedAt) > 100 {
			return false
		}
		seen[item.ID] = true
	}
	return true
}
func validRepairAttachments(items []contracts.RepairAttachment, id string) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if !repairPathID.MatchString(item.ID) || seen[item.ID] || !utf8.ValidString(item.FileName) || item.FileName == "" || len(item.FileName) > 4000 || item.DownloadURL != "" && item.DownloadURL != "/api/repairs/"+id+"/attachments/"+item.ID {
			return false
		}
		switch item.MimeType {
		case "", "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return false
		}
		seen[item.ID] = true
	}
	return true
}

func validRepairDetailPages(value *contracts.RepairDetail, id string) bool {
	selection := contracts.ReadPageSelection{Page: 1, PerPage: 50, Explicit: true}
	if value.TimelinePage != nil {
		page := value.TimelinePage
		if page.Items == nil || !contracts.ValidReadPage(page.ReadPageMetadata, len(page.Items), selection) || !validRepairEvents(page.Items) || !reflect.DeepEqual(page.Items, value.Timeline) || *page.HasMore != value.TimelineHasMore {
			return false
		}
	}
	if value.AttachmentsPage != nil {
		page := value.AttachmentsPage
		if page.Items == nil || !contracts.ValidReadPage(page.ReadPageMetadata, len(page.Items), selection) || !validRepairAttachments(page.Items, id) || !reflect.DeepEqual(page.Items, value.Attachments) || *page.HasMore != value.AttachmentsHasMore {
			return false
		}
	}
	return true
}
