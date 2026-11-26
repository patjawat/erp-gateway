package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeetingUsageGatewayIsolatesTenantsValidatesAndSanitizes(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	date := "2099-10-01"
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.URL.Path != "/api/meetings/usage" || request.URL.RawQuery != "date=2099-10-01" || request.Method != "GET" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) {
			t.Fatalf("wrong tenant/request %s", request.URL)
		}
		w := httptest.NewRecorder()
		writeData(w, map[string]any{"date": date, "checked_at": "2099-10-01T00:00:00Z", "private": "secret", "items": []any{map[string]any{"room_id": "A", "room_name": code, "start_date": "2099-10-01", "end_date": "2099-10-01", "start_time": "09:00", "end_time": "10:00", "status": "pending", "status_label": "รออนุมัติ", "title": "ประชุมทบทวนแผนงาน", "phone": "secret-contact"}}})
		return w.Result(), nil
	})
	for _, v := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		got := r.request(t, "GET", "/api/meetings/usage?date=2099-10-01", nil, v.token, false)
		if got.Code != 200 || !strings.Contains(got.Body.String(), v.code) || strings.Contains(got.Body.String(), v.other) || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "secret") || !strings.Contains(got.Body.String(), "ประชุมทบทวนแผนงาน") {
			t.Fatalf("unsafe schedule %d %s", got.Code, got.Body.String())
		}
	}
	before := calls
	for _, path := range []string{"/api/meetings/usage", "/api/meetings/usage?date=2026-02-29", "/api/meetings/usage?date=2099-10-01&emp_id=99", "/api/meetings/usage?date=2099-10-01&date=2099-10-02"} {
		if got := r.request(t, "GET", path, nil, ta, false); got.Code != 400 {
			t.Fatalf("invalid query %s: %d", path, got.Code)
		}
	}
	if got := r.request(t, "GET", "/api/meetings/usage?date=2099-10-01", nil, "", false); got.Code != 401 {
		t.Fatalf("no auth %d", got.Code)
	}
	if got := r.request(t, "POST", "/api/meetings/usage?date=2099-10-01", nil, ta, false); got.Code != 405 || got.Header().Get("Allow") != "GET" {
		t.Fatalf("wrong method %d", got.Code)
	}
	if calls != before {
		t.Fatal("invalid schedule request reached upstream")
	}
	date = "2099-10-02"
	if got := r.request(t, "GET", "/api/meetings/usage?date=2099-10-01", nil, ta, false); got.Code != 503 {
		t.Fatalf("wrong response date accepted %d %s", got.Code, got.Body.String())
	}
}
