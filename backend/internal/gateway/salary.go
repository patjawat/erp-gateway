package gateway

import (
	"net/http"
	"strconv"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) salaryHistory(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	selection, err := contracts.SalaryQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกข้อมูลเงินเดือนไม่ถูกต้อง")
		return
	}
	path := "/api/salary?page=" + strconv.Itoa(selection.Page) + "&page_size=" + strconv.Itoa(selection.PageSize)
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayError(w, response, false)
		return
	}
	var value contracts.SalaryHistory
	if err = readEnvelope(response, &value); err != nil || !contracts.ValidSalaryHistory(value, selection) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}
