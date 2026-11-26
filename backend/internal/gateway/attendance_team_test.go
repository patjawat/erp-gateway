package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestAttendanceTeamGatewayTenantAndStrictPagination(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	malformed := false
	upstreamError := ""
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || req.Header.Get("Cookie") != "" {
			t.Fatal("wrong hospital session")
		}
		w := httptest.NewRecorder()
		if upstreamError != "" {
			fail(w, 409, upstreamError, "private upstream text")
			return w.Result(), nil
		}
		if strings.HasSuffix(req.URL.Path, "/members") {
			if req.URL.Query().Get("department_id") != "3" || req.URL.Query().Get("per_page") != "1" || req.URL.Query().Get("scope_key") != strings.Repeat("a", 64) {
				t.Fatal("query guards lost", req.URL)
			}
			date := "2026-11-22"
			if malformed {
				date = "2026-11-23"
			}
			writeData(w, contracts.AttendanceTeamMembersPage{Date: date, ScopeKey: strings.Repeat("a", 64), DepartmentID: "3", Department: "Department " + code, SelectedDepartmentID: "3", SelectedDepartment: "Department " + code, Status: "pending", TotalPeople: 2, PendingCount: 2, Items: []contracts.AttendanceTeamMember{{ID: "17", FullName: "Member", DepartmentID: "3", Department: "Department " + code}}, Page: 1, PerPage: 1, TotalCount: 2, HasMore: true})
		} else {
			count := 2
			if malformed {
				count = -1
			}
			writeData(w, contracts.AttendanceTeamSummary{Date: "2026-11-22", ScopeKey: strings.Repeat("a", 64), DepartmentID: "3", Department: "Department " + code, CheckedCount: 1, TotalCount: count, PendingCount: 1})
		}
		return w.Result(), nil
	})
	memberPath := "/api/attendance/team/members?department_id=3&status=pending&per_page=1&expected_date=2026-11-22&scope_key=" + strings.Repeat("a", 64)
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, path := range []string{"/api/attendance/team", memberPath} {
			w := r.request(t, "GET", path, nil, tenant.token, false)
			if w.Code != 200 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), "Department "+tenant.code) || strings.Contains(w.Body.String(), tenant.other) || w.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	before := calls
	for _, path := range []string{"/api/attendance/team?employee_id=99", "/api/attendance/team/departments?department_id=3&per_page=51", "/api/attendance/team/members?department_id=3&status=pending&role=head"} {
		if w := r.request(t, "GET", path, nil, ta, false); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/attendance/team", nil, "", false); w.Code != 401 || calls != before {
		t.Fatal("unauthenticated or injected query reached upstream")
	}
	if w := r.request(t, "POST", "/api/attendance/team", nil, ta, false); w.Code != 405 || calls != before {
		t.Fatal("read endpoint accepted mutation")
	}
	malformed = true
	for _, path := range []string{"/api/attendance/team", memberPath} {
		if w := r.request(t, "GET", path, nil, ta, false); w.Code != 503 {
			t.Fatal("invalid upstream accepted", w.Code, w.Body.String())
		}
	}
	upstreamError = "ATTENDANCE_TEAM_CHANGED"
	if w := r.request(t, "GET", memberPath, nil, ta, false); w.Code != 409 || strings.Contains(w.Body.String(), "private upstream") {
		t.Fatal("stale scope error not sanitized", w.Code, w.Body.String())
	}
}
