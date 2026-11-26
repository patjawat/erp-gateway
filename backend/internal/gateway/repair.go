package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) repairHistory(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	page, perPage, query, err := contracts.RepairHistoryQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกประวัติแจ้งซ่อมไม่ถูกต้อง")
		return
	}
	query.Set("page", strconv.Itoa(page))
	query.Set("per_page", strconv.Itoa(perPage))
	path := "/api/repairs?" + query.Encode()
	var value contracts.RepairHistory
	if !s.readRepair(w, r, h, token, path, &value) {
		return
	}
	if value.Items == nil || len(value.Items) > perPage || value.Page != page || value.PerPage != perPage || value.TotalCount < 0 {
		upstreamUnavailable(w)
		return
	}
	// Older hospital APIs may omit aggregates; preserve nil rather than invent
	// a total from the loaded page. When present, reject inconsistent summaries.
	if len(value.FiscalYears) > 302 {
		upstreamUnavailable(w)
		return
	}
	seenYears := map[int]bool{}
	for _, year := range value.FiscalYears {
		if year < 2443 || year > 2744 || seenYears[year] {
			upstreamUnavailable(w)
			return
		}
		seenYears[year] = true
	}
	if value.StatusCounts != nil {
		total := 0
		if len(value.StatusCounts) > 100 || value.SummaryTotal < 0 || value.TotalCount > value.SummaryTotal {
			upstreamUnavailable(w)
			return
		}
		for status, count := range value.StatusCounts {
			if len(status) > 100 || count < 0 || count > value.SummaryTotal-total {
				upstreamUnavailable(w)
				return
			}
			total += count
		}
		if total != value.SummaryTotal {
			upstreamUnavailable(w)
			return
		}
	}
	writeData(w, value)
}

func (s *Server) repairOptions(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกแจ้งซ่อมไม่ถูกต้อง")
		return
	}
	var value contracts.RepairOptions
	if !s.readRepair(w, r, h, token, "/api/repairs/options", &value) {
		return
	}
	if value.DeviceTypes == nil || value.RepairGroups == nil || value.Urgencies == nil || len(value.DeviceTypes) > 1000 || len(value.RepairGroups) > 20 || len(value.Urgencies) > 20 {
		upstreamUnavailable(w)
		return
	}
	writeData(w, value)
}

func (s *Server) repairAssets(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	query, err := contracts.RepairAssetQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกครุภัณฑ์ไม่ถูกต้อง")
		return
	}
	var value contracts.RepairAssets
	if !s.readRepair(w, r, h, token, "/api/repairs/assets?"+query.Encode(), &value) {
		return
	}
	page, perPage, _ := contracts.RepairAssetPagination(query)
	metadata := query.Has("page") || query.Has("per_page") || value.Page != 0 || value.PerPage != 0
	if value.Items == nil || len(value.Items) > perPage || (metadata && (value.Page != page || value.PerPage != perPage)) {
		upstreamUnavailable(w)
		return
	}
	if code := query.Get("code"); code != "" {
		for _, item := range value.Items {
			if item.Code != code {
				upstreamUnavailable(w)
				return
			}
		}
	}
	writeData(w, value)
}

func (s *Server) createRepair(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var request contracts.RepairCreate
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ข้อมูลแจ้งซ่อมไม่ถูกต้อง")
		return
	}
	if !decodeLimit(w, r, &request, 64*1024) {
		return
	}
	if contracts.ValidateRepairCreate(&request) != nil {
		fail(w, 400, "INVALID_REQUEST", "กรุณาตรวจสอบข้อมูลแจ้งซ่อม")
		return
	}
	body, _ := json.Marshal(request)
	response, err := s.upstream(r.Context(), h, http.MethodPost, "/api/repairs", token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayRepairError(w, response)
		return
	}
	var value contracts.Repair
	if readEnvelopeLimit(response, &value, 8*1024*1024) != nil || value.ID == "" || value.Title == "" {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}

func (s *Server) readRepair(w http.ResponseWriter, r *http.Request, h Hospital, token, path string, out any) bool {
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return false
	}
	if response.StatusCode != http.StatusOK {
		s.relayRepairError(w, response)
		return false
	}
	if readEnvelopeLimit(response, out, 8*1024*1024) != nil {
		upstreamUnavailable(w)
		return false
	}
	return s.stillActive(w, r, h)
}

// Only known status/code pairs cross the gateway. Database messages and local
// infrastructure details never become Mobile errors.
func (s *Server) relayRepairError(w http.ResponseWriter, response *http.Response) {
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusTooManyRequests {
		s.relayError(w, response, false)
		return
	}
	defer response.Body.Close()
	var envelope struct {
		Error contracts.Failure `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	_ = json.Unmarshal(data, &envelope)
	for _, known := range []struct {
		status  int
		code    string
		message string
	}{
		{400, "INVALID_REQUEST", "กรุณาตรวจสอบข้อมูลแจ้งซ่อม"},
		{403, "EMPLOYEE_REQUIRED", "บัญชีนี้ยังไม่มีข้อมูลบุคลากร กรุณาติดต่อผู้ดูแล"},
		{409, "ASSET_UNAVAILABLE", "ข้อมูลครุภัณฑ์เปลี่ยนแปลง กรุณาเลือกครุภัณฑ์ใหม่"},
		{503, "REPAIR_DISABLED", "หน่วยงานยังไม่เปิดรับแจ้งซ่อมผ่านมือถือ กรุณาติดต่อผู้ดูแล"},
	} {
		if response.StatusCode == known.status && envelope.Error.Code == known.code {
			fail(w, known.status, known.code, known.message)
			return
		}
	}
	upstreamUnavailable(w)
}
