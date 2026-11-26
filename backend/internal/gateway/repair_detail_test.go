package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRepairDetailTenantRoutingValidationAndErrors(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "11447.example.invalid" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[a.Code] || req.Header.Get("Cookie") != "" {
			t.Fatal("wrong tenant")
		}
		w := httptest.NewRecorder()
		if strings.Contains(req.URL.Path, "/12") {
			fail(w, 404, "REPAIR_NOT_FOUND", "private upstream details")
			return w.Result(), nil
		}
		if strings.HasSuffix(req.URL.Path, "/feedback") {
			fail(w, 409, "REPAIR_STATE_CHANGED", "private details")
			return w.Result(), nil
		}
		writeData(w, contracts.RepairDetail{Record: contracts.Repair{ID: "10"}, Timeline: []contracts.RepairEvent{}, Attachments: []contracts.RepairAttachment{}})
		return w.Result(), nil
	})
	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/api/repairs/10", nil, 200},
		{"GET", "/api/repairs/12", nil, 404},
		{"POST", "/api/repairs/10/feedback", map[string]string{"rating": "5", "comment": ""}, 409},
	} {
		w := r.request(t, tc.method, tc.path, tc.body, token, false)
		if w.Code != tc.want || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	before := calls
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/repairs/10?emp_id=99", nil},
		{"POST", "/api/repairs/10/feedback", map[string]string{"rating": "9"}},
		{"POST", "/api/repairs/10/feedback", map[string]string{"rating": "5", "emp_id": "99"}},
		{"POST", "/api/repairs/10/edit", map[string]string{"title": "x", "status": "success"}},
	} {
		w := r.request(t, tc.method, tc.path, tc.body, token, false)
		if w.Code != 400 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if calls != before {
		t.Fatal("invalid request forwarded")
	}
}
