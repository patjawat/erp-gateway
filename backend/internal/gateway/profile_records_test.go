package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestProfileRecordsGatewayTenantBindingAndBounds(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	malformed := false
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/profile/records" && req.URL.Path != "/api/profile/personal" {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) {
			t.Fatal("wrong tenant credentials")
		}
		var result any
		if req.URL.Path == "/api/profile/personal" {
			result = contracts.ProfilePersonal{Fields: []contracts.ProfileField{{Label: "ภูมิลำเนาเดิม", Value: "ERP " + code}}}
		} else {
			q, err := contracts.ProfileRecordsQuery(req.URL.RawQuery)
			if err != nil || q.PerPage != 20 {
				t.Fatal("unbounded query")
			}
			result = contracts.ProfileRecords{Items: []contracts.ProfileRecord{{ID: "1", Title: "ERP " + code, Fields: []contracts.ProfileField{}, Children: []contracts.ProfileRecordLink{}}}, Section: q.Section, ParentID: q.ParentID, Page: q.Page, PerPage: q.PerPage, TotalCount: 1, HasMore: malformed}
		}
		raw, _ := json.Marshal(map[string]any{"data": result})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, v := range []struct{ token, own, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, path := range []string{"/api/profile/personal", "/api/profile/records?section=position"} {
			w := r.request(t, "GET", path, nil, v.token, false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "ERP "+v.own) || strings.Contains(w.Body.String(), "ERP "+v.other) || w.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatal("tenant leak", w.Code, w.Body.String())
			}
		}
	}
	before := calls
	for _, path := range []string{"/api/profile/records?section=position&emp_id=18", "/api/profile/records?section=position&per_page=100", "/api/profile/personal?employee_id=18"} {
		if w := r.request(t, "GET", path, nil, ta, false); w.Code != 400 || calls != before {
			t.Fatal("invalid query reached hospital", w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/profile/records?section=position", nil, "", false); w.Code != 401 || calls != before {
		t.Fatal("anonymous access")
	}
	malformed = true
	if w := r.request(t, "GET", "/api/profile/records?section=position", nil, ta, false); w.Code != 503 {
		t.Fatal("malformed page accepted", w.Code)
	}
}
