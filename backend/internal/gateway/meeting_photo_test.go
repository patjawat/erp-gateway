package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestMeetingRoomPhotoGatewayStaysInTenantAndRejectsUnsafeBodies(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	path := "/api/meetings/rooms/1/photo"
	image := true
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != h.Code+".example.invalid" || request.URL.Path != path || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("room photo escaped selected tenant/session")
		}
		w := httptest.NewRecorder()
		if image {
			_, _ = io.WriteString(w, "GIF89a-room-image")
		} else {
			_, _ = io.WriteString(w, "<script>untrusted upstream</script>")
		}
		return w.Result(), nil
	})
	if got := r.request(t, "GET", path, nil, "", false); got.Code != 401 {
		t.Fatalf("unauthenticated %d", got.Code)
	}
	if got := r.request(t, "POST", path, nil, token, false); got.Code != 405 || got.Header().Get("Allow") != "GET" {
		t.Fatalf("method %d", got.Code)
	}
	if got := r.request(t, "GET", path+"?upload_id=9", nil, token, false); got.Code != 400 {
		t.Fatalf("selector %d", got.Code)
	}
	if calls != 0 {
		t.Fatal("invalid selector reached upstream")
	}
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 200 || got.Header().Get("Content-Type") != "image/gif" || got.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("photo %d %s", got.Code, got.Body.String())
	}
	image = false
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 || strings.Contains(got.Body.String(), "script") {
		t.Fatalf("unsafe body %d %s", got.Code, got.Body.String())
	}
}

func TestMeetingOutputRejectsExternalRoomPhotoPaths(t *testing.T) {
	options := contracts.MeetingOptions{Rooms: []contracts.MeetingRoom{{ID: "A", ImageURL: "https://external.invalid/private.png"}}, Layouts: []contracts.MeetingOption{}, Equipment: []contracts.MeetingOption{}, Urgencies: []contracts.MeetingOption{}}
	if validMeetingOutput(&options) {
		t.Fatal("external room image URL accepted")
	}
	available := contracts.MeetingAvailability{CheckedAt: "now", Rooms: []contracts.MeetingAvailableRoom{{MeetingRoom: options.Rooms[0]}}}
	if validMeetingOutput(&available) {
		t.Fatal("external availability image accepted")
	}
	options.Rooms[0].ImageURL = ""
	if !validMeetingOutput(&options) {
		t.Fatal("legacy rooms without image stopped working")
	}
}

func TestMeetingLayoutPhotoGatewayAndMetadataValidation(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	path := "/api/meetings/layouts/4/photo"
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != h.Code+".example.invalid" || request.URL.Path != path || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] {
			t.Fatal("layout image escaped selected tenant")
		}
		w := httptest.NewRecorder()
		_, _ = io.WriteString(w, "GIF89a-layout-image")
		return w.Result(), nil
	})
	if got := r.request(t, "GET", path, nil, "", false); got.Code != 401 {
		t.Fatalf("unauthenticated %d", got.Code)
	}
	if got := r.request(t, "POST", path, nil, token, false); got.Code != 405 {
		t.Fatalf("method %d", got.Code)
	}
	if got := r.request(t, "GET", path+"?ref=other", nil, token, false); got.Code != 400 {
		t.Fatalf("selector %d", got.Code)
	}
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 200 || got.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("image %d", got.Code)
	}
	options := contracts.MeetingOptions{Rooms: []contracts.MeetingRoom{}, Layouts: []contracts.MeetingOption{{ID: "U", Label: "รูปตัวยู", ImageURL: path}}, Equipment: []contracts.MeetingOption{}, Urgencies: []contracts.MeetingOption{}}
	if !validMeetingOutput(&options) {
		t.Fatal("layout image rejected")
	}
	for _, bad := range []string{"https://external.invalid/photo", "/api/meetings/rooms/4/photo", path + "?upload=2", "/api/meetings/layouts/../photo"} {
		options.Layouts[0].ImageURL = bad
		if validMeetingOutput(&options) {
			t.Fatalf("unsafe layout URL accepted: %s", bad)
		}
	}
}
