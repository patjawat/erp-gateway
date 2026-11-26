package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepairSearchForwardingAndAggregateCounts(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/api/repairs" || req.URL.Query().Get("q") != "เครื่อง%" || req.URL.Query().Get("status") != "success" || req.URL.Query().Get("page") != "1" || req.URL.Query().Get("fiscal_year") != "2570" {
			t.Fatal("search not forwarded", req.URL)
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.RepairHistory{Items: []contracts.Repair{}, Page: 1, PerPage: 50, TotalCount: 5, SummaryTotal: 12, StatusCounts: map[string]int{"pending": 7, "success": 5}})
		return w.Result(), nil
	})
	w := r.request(t, "GET", "/api/repairs?q=%E0%B9%80%E0%B8%84%E0%B8%A3%E0%B8%B7%E0%B9%88%E0%B8%AD%E0%B8%87%25&status=success&fiscal_year=2570", nil, token, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/repairs?fiscal_year=2570&fiscal_year=2569", "/api/repairs?fiscal_year=all", "/api/repairs?q=a&q=b", "/api/repairs?status=all", "/api/repairs?q=%00", "/api/repairs?q=a&emp_id=99"} {
		if w = r.request(t, "GET", path, nil, token, false); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("invalid search reached backend")
	}
}

func TestRepairSummaryValidationAndLegacyCompatibility(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, tc := range []struct {
		name           string
		total, summary int
		counts         map[string]int
		want           int
	}{
		{"legacy", 2, 0, nil, 200},
		{"valid", 1, 2, map[string]int{"pending": 1, "success": 1}, 200},
		{"negative", 0, 2, map[string]int{"pending": -1, "success": 3}, 503},
		{"mismatch", 0, 2, map[string]int{"pending": 1}, 503},
		{"filtered exceeds", 3, 2, map[string]int{"pending": 2}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				writeData(w, contracts.RepairHistory{Items: []contracts.Repair{}, Page: 1, PerPage: 50, TotalCount: tc.total, SummaryTotal: tc.summary, StatusCounts: tc.counts})
				return w.Result(), nil
			})
			w := r.request(t, "GET", "/api/repairs", nil, token, false)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
