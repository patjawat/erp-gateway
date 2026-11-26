package gateway

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) leaveHistory(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	selection, err := contracts.ParseLeaveHistorySelection(r.URL.RawQuery)
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "ตัวเลือกประวัติการลาไม่ถูกต้อง")
		return
	}
	// Local Go validates the complete query and selects the employee from its
	// verified session. No caller-supplied employee or hospital selector is used.
	query := url.Values{
		"page":     {strconv.Itoa(selection.Page)},
		"per_page": {strconv.Itoa(selection.PerPage)},
	}
	if selection.FiscalYear != 0 {
		query.Set("fiscal_year", strconv.Itoa(selection.FiscalYear))
	}
	if selection.LeaveTypeID != "" {
		query.Set("leave_type_id", selection.LeaveTypeID)
	}
	path := "/api/leaves?" + query.Encode()
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayError(w, response, false)
		return
	}
	var value contracts.LeaveHistory
	if err = readEnvelope(response, &value); err != nil || value.Items == nil || len(value.Items) > 100 || value.TotalCount < 0 || value.Page < 1 || value.PerPage < 1 || value.PerPage > 100 || !validLeaveHistoryFilters(value.FilterOptions) || !contracts.ValidLeaveHistoryMonthSummaries(value.MonthSummaries, value.Items, value.TotalCount) {
		upstreamUnavailable(w)
		return
	}
	for _, item := range value.Items {
		if !validSavedLeaveFiscalYear(item.FiscalYear) {
			upstreamUnavailable(w)
			return
		}
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, value)
}

func validSavedLeaveFiscalYear(year *int) bool {
	return year == nil || (*year >= 2443 && *year <= 2744)
}

func validLeaveHistoryFilters(options *contracts.LeaveHistoryFilterOptions) bool {
	if options == nil {
		return true // Older connected APIs may omit filter metadata.
	}
	if options.FiscalYears == nil || options.LeaveTypes == nil || len(options.FiscalYears) > 302 || len(options.LeaveTypes) > 1000 {
		return false
	}
	previous := 2745
	for _, year := range options.FiscalYears {
		if year < 2443 || year > 2744 || year >= previous {
			return false
		}
		previous = year
	}
	seen := map[string]bool{}
	for _, kind := range options.LeaveTypes {
		if !contracts.ValidLeaveTypeID(kind.ID) || strings.TrimSpace(kind.Name) == "" || seen[kind.ID] {
			return false
		}
		seen[kind.ID] = true
	}
	return true
}
