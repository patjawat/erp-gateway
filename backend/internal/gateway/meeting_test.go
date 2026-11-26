package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func meetingRequest() contracts.MeetingInput {
	return contracts.MeetingInput{RoomID: "A", Title: "ประชุมทีม", StartDate: "2099-10-01", EndDate: "2099-10-01", StartTime: "09:00", EndTime: "10:00", Attendees: 4, Phone: "1234", UrgencyID: "normal", EquipmentIDs: []string{}, RequestID: "meeting_request_00001"}
}
func TestMeetingGatewayIsolatesTenantsAndValidatesRequests(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("Cookie") != "" {
			t.Fatal("wrong tenant credentials")
		}
		w := httptest.NewRecorder()
		item := contracts.Meeting{MeetingInput: meetingRequest(), ID: "1", Code: "MTG-M1", Status: "pending", RoomName: code}
		switch request.URL.Path {
		case "/api/meetings/options":
			writeData(w, contracts.MeetingOptions{CanWrite: true, Rooms: []contracts.MeetingRoom{{ID: "A", Name: code, Equipment: []string{}}}, Layouts: []contracts.MeetingOption{}, Equipment: []contracts.MeetingOption{}, Urgencies: []contracts.MeetingOption{}})
		case "/api/meetings/availability":
			writeData(w, contracts.MeetingAvailability{CheckedAt: "2099-10-01T00:00:00Z", Rooms: []contracts.MeetingAvailableRoom{{MeetingRoom: contracts.MeetingRoom{ID: "A", Name: code, Equipment: []string{}}, Available: true}}})
		case "/api/meetings":
			if request.Method == http.MethodGet {
				writeData(w, contracts.MeetingHistory{Items: []contracts.Meeting{item}, Page: 1, PerPage: 50, TotalCount: 1})
			} else {
				var input contracts.MeetingInput
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.RequestID != meetingRequest().RequestID {
					t.Fatal("mutation payload lost")
				}
				writeData(w, item)
			}
		case "/api/meetings/1", "/api/meetings/1/cancel":
			writeData(w, item)
		default:
			t.Fatalf("unexpected route %s", request.URL.Path)
		}
		return w.Result(), nil
	})
	for _, v := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, path := range []string{"/api/meetings", "/api/meetings/options", "/api/meetings/1", "/api/meetings/availability?start_date=2099-10-01&end_date=2099-10-01&start_time=09%3A00&end_time=10%3A00&attendees=4"} {
			w := r.request(t, "GET", path, nil, v.token, false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), v.code) || strings.Contains(w.Body.String(), v.other) {
				t.Fatalf("tenant leak %d %s", w.Code, w.Body.String())
			}
		}
		for _, m := range []struct {
			method, path string
			body         any
		}{{"POST", "/api/meetings", meetingRequest()}, {"PUT", "/api/meetings/1", meetingRequest()}, {"POST", "/api/meetings/1/cancel", contracts.MeetingCancel{RequestID: "cancel_request_000001"}}} {
			w := r.request(t, m.method, m.path, m.body, v.token, false)
			if w.Code != 200 {
				t.Fatalf("mutation %d %s", w.Code, w.Body.String())
			}
		}
	}
	before := calls
	for _, path := range []string{"/api/meetings?emp_id=99", "/api/meetings?hospital_code=99999", "/api/meetings?status=raw", "/api/meetings/options?x=1", "/api/meetings/availability?attendees=0"} {
		if got := r.request(t, "GET", path, nil, ta, false); got.Code != 400 {
			t.Fatalf("selector accepted %s: %d", path, got.Code)
		}
	}
	if got := r.request(t, "POST", "/api/meetings", map[string]string{"emp_id": "99"}, ta, false); got.Code != 400 {
		t.Fatal("ownership field accepted")
	}
	if got := r.request(t, "DELETE", "/api/meetings", nil, ta, false); got.Code != 405 {
		t.Fatalf("method %d", got.Code)
	}
	for _, route := range []struct{ method, path, allow string }{
		{"POST", "/api/meetings/options", "GET"},
		{"PUT", "/api/meetings/availability", "GET"},
		{"HEAD", "/api/meetings/options", "GET"},
		{"POST", "/api/meetings/1", "GET, PUT"},
		{"GET", "/api/meetings/1/cancel", "POST"},
	} {
		got := r.request(t, route.method, route.path, nil, ta, false)
		if got.Code != 405 || got.Header().Get("Allow") != route.allow || !strings.Contains(got.Body.String(), "METHOD_NOT_ALLOWED") {
			t.Fatalf("method %s %s: %d %s", route.method, route.path, got.Code, got.Body.String())
		}
	}
	for _, path := range []string{"/api/meetings/not-an-id", "/api/meetings/1/extra", "/api/meetings/options/cancel"} {
		if got := r.request(t, "GET", path, nil, ta, false); got.Code != 404 {
			t.Fatalf("unknown route %s: %d", path, got.Code)
		}
	}
	if calls != before {
		t.Fatal("invalid request reached local API")
	}
	if got := r.request(t, "POST", "/api/meetings", meetingRequest(), "", false); got.Code != 401 {
		t.Fatal("unauthenticated mutation")
	}
	r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test"}, "", true)
	if got := r.request(t, "POST", "/api/meetings", meetingRequest(), ta, false); got.Code != 403 {
		t.Fatal("suspended tenant mutated")
	}
}
func TestMeetingGatewaySanitizesErrorsAndRetainsFieldKeys(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, v := range []struct {
		status   int
		code     string
		expected int
	}{{409, "SLOT_CONFLICT", 409}, {409, "STATUS_CHANGED", 409}, {409, "REQUEST_ID_CONFLICT", 409}, {403, "READ_ONLY", 403}, {404, "NOT_FOUND", 404}, {422, "VALIDATION_FAILED", 422}, {500, "private-database", 503}, {403, "GATEWAY_UNAUTHORIZED", 503}} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			writeJSON(w, v.status, map[string]any{"error": map[string]any{"code": v.code, "message": "private password", "fields": map[string]string{"attendees": "private SQL", "private_key": "private"}}})
			return w.Result(), nil
		})
		w := r.request(t, "POST", "/api/meetings", meetingRequest(), token, false)
		if w.Code != v.expected || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("unsafe error %d %s", w.Code, w.Body.String())
		}
		if v.status == 422 && !strings.Contains(w.Body.String(), `"attendees"`) {
			t.Fatal("field error lost")
		}
	}
}
