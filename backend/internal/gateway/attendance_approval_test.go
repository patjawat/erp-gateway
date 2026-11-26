package gateway

import (
	"bytes"
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAttendanceApprovalRoutingAndValidation(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	calls := 0
	item := contracts.AttendanceApproval{ID: "8", RecordID: "9", EmployeeName: "Employee", RecordedAt: "2026-11-18T12:00:00+07:00", CheckType: "in", Status: "pending", Revision: strings.Repeat("a", 64), CanDecide: true}
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/attendance/approvals") {
			return previous.RoundTrip(req)
		}
		calls++
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[a.Code] || req.URL.Host != a.Code+".example.invalid" {
			t.Fatal("wrong session")
		}
		var value any = item
		if req.URL.Path == "/api/attendance/approvals" {
			if req.URL.RawQuery != "page=1" {
				t.Fatal("query")
			}
			value = contracts.AttendanceApprovalList{Items: []contracts.AttendanceApproval{item}, Page: 1}
		}
		if strings.HasSuffix(req.URL.Path, "/decision") {
			var v contracts.AttendanceDecision
			if json.NewDecoder(req.Body).Decode(&v) != nil || v.Decision != "approve" {
				t.Fatal("decision")
			}
			approved := item
			approved.Status = "approved"
			approved.CanDecide = false
			value = approved
		}
		data, _ := json.Marshal(map[string]any{"data": value})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	for _, path := range []string{"/api/attendance/approvals", "/api/attendance/approvals/8"} {
		if w := r.request(t, "GET", path, nil, token, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := r.request(t, "POST", "/api/attendance/approvals/8/decision", contracts.AttendanceDecision{Decision: "approve", Revision: item.Revision}, token, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/attendance/approvals?emp_id=9", "/api/attendance/approvals?page=0", "/api/attendance/approvals/8?hospital_id=2"} {
		if w := r.request(t, "GET", path, nil, token, false); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := r.request(t, "POST", "/api/attendance/approvals/8/decision", map[string]string{"decision": "approve", "revision": "old"}, token, false); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if calls != 3 {
		t.Fatal("invalid request reached upstream", calls)
	}
}
