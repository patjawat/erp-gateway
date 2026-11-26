package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestLeaveHistoryRoutesOnlyToSessionHospital(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokenA, tokenB := r.login(t, a.Code), r.login(t, b.Code)
	for _, tenant := range []struct{ token, code, other string }{{tokenA, a.Code, b.Code}, {tokenB, b.Code, a.Code}} {
		w := r.request(t, http.MethodGet, "/api/leaves?page=2&per_page=10", nil, tenant.token, false)
		var envelope struct {
			Data contracts.LeaveHistory `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data.Page != 2 || envelope.Data.PerPage != 10 || len(envelope.Data.Items) != 1 || envelope.Data.Items[0].LeaveType != "Leave "+tenant.code || strings.Contains(w.Body.String(), tenant.other) {
			t.Fatalf("wrong tenant history: %d %s", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"emp_id=99", "hospital_code=12448", "page=0", "per_page=101", "page=1&page=2", "page=%zz"} {
		if w := r.request(t, http.MethodGet, "/api/leaves?"+query, nil, tokenA, false); w.Code != 400 {
			t.Errorf("invalid query %q accepted: %d", query, w.Code)
		}
	}
	if w := r.request(t, http.MethodGet, "/api/leaves", nil, "", false); w.Code != 401 {
		t.Fatalf("unauthenticated history accepted: %d", w.Code)
	}
	if w := r.request(t, http.MethodPost, "/api/leaves", nil, tokenA, false); w.Code != 415 {
		t.Fatalf("empty leave submission accepted: %d", w.Code)
	}
	w := r.request(t, http.MethodPost, "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test suspension"}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = r.request(t, http.MethodGet, "/api/leaves", nil, tokenA, false); w.Code != 403 {
		t.Fatalf("suspended hospital history accepted: %d", w.Code)
	}
}

func TestLeaveHistoryFiltersForwardOnlySessionSelection(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Method != http.MethodGet || request.URL.Path != "/api/leaves" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("filtered history used another hospital's credentials")
		}
		query := request.URL.Query()
		if query.Get("fiscal_year") != "2570" || query.Get("leave_type_id") != "LT4" || query.Get("per_page") != "10" || len(query) != 4 || request.URL.RawQuery != query.Encode() {
			t.Fatalf("history filters changed or escaped canonical query: %s", request.URL.RawQuery)
		}
		page, err := strconv.Atoi(query.Get("page"))
		if err != nil || page < 2 || page > 3 {
			t.Fatal("pagination lost filtered selection")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.LeaveHistory{
			Items: []contracts.Leave{{ID: "81", LeaveTypeID: "LT4", LeaveType: "Leave " + code}},
			Page:  page, PerPage: 10, TotalCount: 21, HasMore: page < 3,
			FilterOptions: &contracts.LeaveHistoryFilterOptions{
				FiscalYears: []int{2570, 2569},
				LeaveTypes:  []contracts.LeaveType{{ID: "LT4", Name: "Vacation " + code}},
			},
		})
		return w.Result(), nil
	})
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, page := range []int{2, 3} {
			path := "/api/leaves?leave_type_id=LT4&fiscal_year=2570&per_page=10&page=" + strconv.Itoa(page)
			w := r.request(t, http.MethodGet, path, nil, tenant.token, false)
			var envelope struct {
				Data contracts.LeaveHistory `json:"data"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data.Page != page || envelope.Data.FilterOptions == nil || len(envelope.Data.FilterOptions.FiscalYears) != 2 || envelope.Data.FilterOptions.LeaveTypes[0].Name != "Vacation "+tenant.code || strings.Contains(w.Body.String(), tenant.other) {
				t.Fatalf("filtered history or metadata crossed tenants: %d %s", w.Code, w.Body.String())
			}
		}
	}
	before := calls
	for _, query := range []string{
		"emp_id=99", "hospital_code=12448", "status=Approve", "unrecognized=1",
		"fiscal_year=", "fiscal_year=bad", "fiscal_year=2570&fiscal_year=2569",
		"fiscal_year=2442", "fiscal_year=2745", "fiscal_year=2026",
		"leave_type_id=", "leave_type_id=LT4&leave_type_id=LT1", "leave_type_id=LT4%26emp_id%3D99",
		"leave_type_id=" + strings.Repeat("x", 33),
	} {
		if w := r.request(t, http.MethodGet, "/api/leaves?"+query, nil, ta, false); w.Code != http.StatusBadRequest {
			t.Errorf("invalid filter accepted %q: %d", query, w.Code)
		}
	}
	if w := r.request(t, http.MethodGet, "/api/leaves?fiscal_year=2570&leave_type_id=LT4", nil, "", false); w.Code != http.StatusUnauthorized || calls != before {
		t.Fatal("invalid or unauthenticated filter reached hospital API")
	}
}

func TestLeaveHistoryRejectsUnsafeFilterMetadata(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	var options *contracts.LeaveHistoryFilterOptions
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeData(w, contracts.LeaveHistory{Items: []contracts.Leave{}, Page: 1, PerPage: 50, FilterOptions: options})
		return w.Result(), nil
	})
	for _, test := range []struct {
		name    string
		options *contracts.LeaveHistoryFilterOptions
	}{
		{"missing arrays", &contracts.LeaveHistoryFilterOptions{}},
		{"missing types", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{2570}}},
		{"missing years", &contracts.LeaveHistoryFilterOptions{LeaveTypes: []contracts.LeaveType{}}},
		{"ascending years", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{2569, 2570}, LeaveTypes: []contracts.LeaveType{}}},
		{"duplicate year", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{2570, 2570}, LeaveTypes: []contracts.LeaveType{}}},
		{"invalid year", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{2745}, LeaveTypes: []contracts.LeaveType{}}},
		{"invalid type ID", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{}, LeaveTypes: []contracts.LeaveType{{ID: "LT4&emp_id=99", Name: "private label"}}}},
		{"blank type label", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{}, LeaveTypes: []contracts.LeaveType{{ID: "LT4", Name: " "}}}},
		{"duplicate type", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{}, LeaveTypes: []contracts.LeaveType{{ID: "LT4", Name: "Vacation"}, {ID: "LT4", Name: "Duplicate"}}}},
		{"too many types", &contracts.LeaveHistoryFilterOptions{FiscalYears: []int{}, LeaveTypes: make([]contracts.LeaveType, 1001)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options = test.options
			w := r.request(t, http.MethodGet, "/api/leaves", nil, token, false)
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "private label") {
				t.Fatalf("unsafe filter metadata accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, compatible := range []*contracts.LeaveHistoryFilterOptions{nil, {FiscalYears: []int{}, LeaveTypes: []contracts.LeaveType{}}} {
		options = compatible
		if w := r.request(t, http.MethodGet, "/api/leaves", nil, token, false); w.Code != http.StatusOK {
			t.Fatalf("compatible metadata rejected: %d %s", w.Code, w.Body.String())
		}
	}
}
