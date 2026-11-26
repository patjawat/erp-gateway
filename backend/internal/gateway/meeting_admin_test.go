package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestMeetingAdminGatewayScopesTenantAndRejectsCallerAuthority(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokens := map[string]string{a.Code: r.login(t, a.Code), b.Code: r.login(t, b.Code)}
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("admin crossed tenant credentials")
		}
		w := httptest.NewRecorder()
		item := contracts.MeetingAdminBooking{Meeting: contracts.Meeting{MeetingInput: meetingRequest(), ID: "14", Code: "MTG14", Status: "pending", RoomName: code}, Requester: contracts.MeetingRequester{EmployeeID: "23", GivenName: "ผู้ขอ", FullName: code, AvatarURL: contracts.MeetingAdminRequesterAvatarURL("14")}, CanApprove: true, CanAdminCancel: true}
		switch request.URL.Path {
		case "/api/meetings/admin/rooms":
			writeData(w, contracts.MeetingAdminRooms{Items: []contracts.MeetingAdminRoom{{MeetingRoom: contracts.MeetingRoom{ID: "A", Name: code, Equipment: []string{}, ImageURL: contracts.MeetingAdminRoomPhotoURL("1")}, PendingCount: 2}}, Page: 1, PerPage: 20, TotalCount: 1, CanWrite: true})
		case "/api/meetings/admin/bookings":
			writeData(w, contracts.MeetingAdminBookings{Items: []contracts.MeetingAdminBooking{item}, Page: 1, PerPage: 20, TotalCount: 1})
		case "/api/meetings/admin/bookings/14":
			writeData(w, item)
		case "/api/meetings/admin/bookings/14/decision":
			var body contracts.MeetingAdminDecision
			if json.NewDecoder(request.Body).Decode(&body) != nil || body.Decision != "approve" || body.RequestID != "meeting-admin-request-01" {
				t.Fatal("decision payload changed")
			}
			item.Status = "approved"
			item.CanApprove = false
			item.CanAdminCancel = false
			writeData(w, item)
		case "/api/meetings/admin/rooms/1/photo", "/api/meetings/admin/bookings/14/requester-avatar":
			_, _ = io.WriteString(w, "GIF89a-admin-image")
		default:
			t.Fatalf("unexpected admin path %s", request.URL.Path)
		}
		return w.Result(), nil
	})
	for code, token := range tokens {
		for _, path := range []string{"/api/meetings/admin/rooms", "/api/meetings/admin/bookings?room_id=A&status=pending", "/api/meetings/admin/bookings/14"} {
			got := r.request(t, "GET", path, nil, token, false)
			if got.Code != 200 || !strings.Contains(got.Body.String(), code) {
				t.Fatalf("admin %s: %d %s", path, got.Code, got.Body.String())
			}
		}
		for _, path := range []string{"/api/meetings/admin/rooms/1/photo", "/api/meetings/admin/bookings/14/requester-avatar"} {
			got := r.request(t, "GET", path, nil, token, false)
			if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatalf("admin image %d %s", got.Code, got.Body.String())
			}
		}
		got := r.request(t, "POST", "/api/meetings/admin/bookings/14/decision", contracts.MeetingAdminDecision{RequestID: "meeting-admin-request-01", Decision: "approve"}, token, false)
		if got.Code != 200 {
			t.Fatalf("decision %d %s", got.Code, got.Body.String())
		}
	}
	before := calls
	token := tokens[a.Code]
	for _, path := range []string{"/api/meetings/admin/rooms?employee_id=99", "/api/meetings/admin/rooms?per_page=101", "/api/meetings/admin/bookings?room_id=A&owner=99", "/api/meetings/admin/bookings?room_id=A&status=Pass", "/api/meetings/admin/bookings", "/api/meetings/admin/bookings/14/requester-avatar?employee_id=99"} {
		if got := r.request(t, "GET", path, nil, token, false); got.Code != 400 {
			t.Fatalf("bad selector %s: %d", path, got.Code)
		}
	}
	if got := r.request(t, "POST", "/api/meetings/admin/bookings/14/decision", map[string]string{"request_id": "meeting-admin-request-01", "decision": "approve", "employee_id": "99"}, token, false); got.Code != 400 {
		t.Fatalf("authority body %d", got.Code)
	}
	if got := r.request(t, "GET", "/api/meetings/admin/bookings/14/decision", nil, token, false); got.Code != 405 {
		t.Fatalf("decision method %d", got.Code)
	}
	if got := r.request(t, "GET", "/api/meetings/admin/rooms", nil, "", false); got.Code != 401 {
		t.Fatalf("unauthenticated %d", got.Code)
	}
	if calls != before {
		t.Fatal("invalid admin request reached upstream")
	}
}

func TestMeetingAdminGatewayRejectsUnscopedOrUnsafeUpstream(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, bad := range []string{"room", "avatar", "page"} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			item := contracts.MeetingAdminBooking{Meeting: contracts.Meeting{MeetingInput: meetingRequest(), ID: "14", Code: "MTG14", Status: "pending"}, Requester: contracts.MeetingRequester{EmployeeID: "23", AvatarURL: contracts.MeetingAdminRequesterAvatarURL("14")}}
			page := contracts.MeetingAdminBookings{Items: []contracts.MeetingAdminBooking{item}, Page: 1, PerPage: 20, TotalCount: 1}
			if bad == "room" {
				page.Items[0].RoomID = "B"
			}
			if bad == "avatar" {
				page.Items[0].Requester.AvatarURL = "https://external.invalid/photo"
			}
			if bad == "page" {
				page.Page = 2
			}
			writeData(w, page)
			return w.Result(), nil
		})
		if got := r.request(t, "GET", "/api/meetings/admin/bookings?room_id=A", nil, token, false); got.Code != 503 {
			t.Fatalf("unsafe %s accepted %d %s", bad, got.Code, got.Body.String())
		}
	}
}
