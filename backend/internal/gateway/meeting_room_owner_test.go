package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestMeetingOwnerMetadataAcceptsMissingAndRejectsUnsafeIdentity(t *testing.T) {
	room := contracts.MeetingRoom{ID: "A", Name: "ห้องประชุม", Equipment: []string{}}
	options := contracts.MeetingOptions{Rooms: []contracts.MeetingRoom{room}, Layouts: []contracts.MeetingOption{}, Equipment: []contracts.MeetingOption{}, Urgencies: []contracts.MeetingOption{}}
	if !validMeetingOutput(&options) {
		t.Fatal("legacy absent owner rejected")
	}
	owner := &contracts.MeetingRoomOwner{EmployeeID: "17", GivenName: "ผู้ดูแล", FullName: "ผู้ดูแล ห้องประชุม", AvatarURL: contracts.MeetingRoomOwnerAvatarURL("1")}
	options.Rooms[0].Owner = owner
	if !validMeetingOutput(&options) {
		t.Fatal("valid owner rejected")
	}
	availability := contracts.MeetingAvailability{CheckedAt: "now", Rooms: []contracts.MeetingAvailableRoom{{MeetingRoom: options.Rooms[0]}}}
	if !validMeetingOutput(&availability) {
		t.Fatal("available room owner rejected")
	}
	for _, bad := range []string{"https://outside.invalid/private-avatar", "/api/personnel/99/avatar", "/api/meetings/rooms/1/owner-avatar?employee_id=99"} {
		owner.AvatarURL = bad
		if validMeetingOutput(&options) || validMeetingOutput(&availability) {
			t.Fatalf("unsafe owner image accepted %q", bad)
		}
	}
	owner.AvatarURL = ""
	owner.FullName = ""
	if validMeetingOutput(&options) {
		t.Fatal("unresolved owner name accepted")
	}
}

func TestMeetingOwnerAvatarGatewayUsesAuthenticatedHospitalAndRoomOnly(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	calls := 0
	image := true
	path := "/api/meetings/rooms/1/owner-avatar"
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != path || request.URL.Host != h.Code+".example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("owner avatar escaped hospital/session/room selector")
		}
		w := httptest.NewRecorder()
		if image {
			_, _ = io.WriteString(w, "GIF89a-owner-image")
		} else {
			_, _ = io.WriteString(w, "<script>private upstream</script>")
		}
		return w.Result(), nil
	})
	if got := r.request(t, "GET", path, nil, "", false); got.Code != 401 {
		t.Fatalf("unauthenticated %d", got.Code)
	}
	if got := r.request(t, "POST", path, nil, token, false); got.Code != 405 || got.Header().Get("Allow") != "GET" {
		t.Fatalf("method %d", got.Code)
	}
	if got := r.request(t, "GET", path+"?employee_id=99", nil, token, false); got.Code != 400 {
		t.Fatalf("caller employee selected %d", got.Code)
	}
	if calls != 0 {
		t.Fatal("invalid avatar selectors reached upstream")
	}
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 200 || got.Header().Get("Cache-Control") != "no-store, private" || got.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("owner photo %d %s", got.Code, got.Body.String())
	}
	image = false
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 || strings.Contains(got.Body.String(), "script") {
		t.Fatalf("unsafe owner photo %d %s", got.Code, got.Body.String())
	}
}
