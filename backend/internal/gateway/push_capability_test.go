package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestPushCapabilityRemainsLocalAndSessionBound(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("Gateway push capability reached ERP")
		return nil, nil
	})
	for _, test := range []struct {
		token, query string
		status       int
	}{{"", "", 401}, {token, "?hospital_code=12448", 400}, {token, "", 200}} {
		w := r.request(t, "GET", "/api/notification-devices/capabilities"+test.query, nil, test.token, false)
		if w.Code != test.status || test.status == 200 && !strings.Contains(w.Body.String(), `"push_enabled":false`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// The sender stub's presence, not ERP business fields, determines readiness.
	r.app.config.NotificationSender = &fixturePushSender{}
	if w := r.request(t, "GET", "/api/notification-devices/capabilities", nil, token, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"push_enabled":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
