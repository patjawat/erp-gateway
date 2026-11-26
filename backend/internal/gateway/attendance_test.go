package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestAttendanceHistoryRoutesOnlyToSessionHospital(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokenA, tokenB := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	reads := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/attendance" {
			return previous.RoundTrip(req)
		}
		reads++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.URL.RawQuery != "month=2026-11" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) {
			t.Fatalf("incorrect upstream selection/credentials: %s", req.URL)
		}
		inside := false
		payload := contracts.AttendanceHistory{Month: "2026-11", Items: []contracts.AttendanceDay{{Date: "2026-11-18", Status: "pending", SourceLabel: "ERP " + code,
			Events: []contracts.AttendanceEvent{{ID: "1", CheckType: "scan", RecordedAt: "2026-11-18T08:00:00+07:00", Status: "pending", HasPhoto: true, Reason: "  ไปปฏิบัติงานนอกสถานที่  ", IsInLocation: &inside}}}}}
		body, _ := json.Marshal(map[string]any{"data": payload})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	for _, tenant := range []struct{ token, code, other string }{{tokenA, a.Code, b.Code}, {tokenB, b.Code, a.Code}} {
		w := r.request(t, "GET", "/api/attendance?month=2026-11", nil, tenant.token, false)
		if !strings.Contains(w.Body.String(), `"has_photo":true`) || !strings.Contains(w.Body.String(), `"is_in_location":false`) || !strings.Contains(w.Body.String(), `"reason":"  ไปปฏิบัติงานนอกสถานที่  "`) {
			t.Fatalf("metadata lost: %s", w.Body.String())
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ERP "+tenant.code) || strings.Contains(w.Body.String(), tenant.other) {
			t.Fatalf("wrong tenant history: %d %s", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"", "month=2026-11&emp_id=99", "month=2026-11&hospital_code=12448", "month=2026-13", "month=2026-11&month=2026-12", "month=%zz"} {
		if w := r.request(t, "GET", "/api/attendance?"+query, nil, tokenA, false); w.Code != 400 || reads != 2 {
			t.Errorf("invalid selection accepted %q: %d", query, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/attendance?month=2026-11", nil, "", false); w.Code != 401 || reads != 2 {
		t.Fatal("anonymous attendance request reached upstream")
	}
	if w := r.request(t, "POST", "/api/attendance?month=2026-11", nil, tokenA, false); w.Code != 405 || reads != 2 {
		t.Fatal("attendance accepted write method")
	}
	w := r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test suspension"}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.request(t, "GET", "/api/attendance?month=2026-11", nil, tokenA, false); w.Code != 403 || reads != 2 {
		t.Fatal("suspended hospital request reached upstream")
	}
}

func TestAttendanceHistoryRejectsMalformedUpstream(t *testing.T) {
	valid := contracts.AttendanceHistory{Month: "2026-11", Items: []contracts.AttendanceDay{{Date: "2026-11-18", Status: "pending", Events: []contracts.AttendanceEvent{{ID: "1", CheckType: "scan", RecordedAt: "2026-11-18T08:00:00+07:00", Status: "pending"}}}}}
	if !validAttendanceHistory(valid, "2026-11") {
		t.Fatal("valid upstream payload rejected")
	}
	for _, change := range []func(*contracts.AttendanceHistory){
		func(v *contracts.AttendanceHistory) { v.Month = "2026-12" },
		func(v *contracts.AttendanceHistory) { v.Items[0].Events[0].Reason = strings.Repeat("x", 12001) },
		func(v *contracts.AttendanceHistory) { v.Items = nil },
		func(v *contracts.AttendanceHistory) { v.Items[0].Date = "2026-12-01" },
		func(v *contracts.AttendanceHistory) { v.Items[0].Events = nil },
		func(v *contracts.AttendanceHistory) { v.Items[0].Status = "unknown" },
		func(v *contracts.AttendanceHistory) { v.Items[0].Events[0].RecordedAt = "bad time" },
		func(v *contracts.AttendanceHistory) { v.Items[0].Events[0].CheckType = "unknown" },
		func(v *contracts.AttendanceHistory) { n := -1; v.Items[0].DurationMinutes = &n },
	} {
		encoded, _ := json.Marshal(valid)
		var value contracts.AttendanceHistory
		_ = json.Unmarshal(encoded, &value)
		change(&value)
		if validAttendanceHistory(value, "2026-11") {
			t.Fatalf("malformed history accepted: %#v", value)
		}
	}
}
