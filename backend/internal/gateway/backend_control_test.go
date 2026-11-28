package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBackendControlReturnsOnlySessionBoundConnectionIdentity(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	for _, code := range []string{"11447", "12448"} {
		w := r.request(code, "/api/auth/backend")
		var envelope struct {
			Data map[string]string `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || len(envelope.Data) != 3 || envelope.Data["hospital_id"] != r.sites[code].ID || envelope.Data["hospital_code"] != code {
			t.Fatal(w.Code, w.Body.String())
		}
		want := "go"
		if code == "12448" {
			want = "erp_api_module"
		}
		if envelope.Data["backend"] != want || strings.Contains(w.Body.String(), "upstream-token") || strings.Contains(w.Body.String(), ".invalid") {
			t.Fatal(w.Body.String())
		}
	}
	if w := r.request("12448", "/api/auth/backend?backend=go"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := r.store.revoke(context.Background(), r.tokens["12448"]); err != nil {
		t.Fatal(err)
	}
	if w := r.request("12448", "/api/auth/backend"); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBackendControlRejectsUnknownDiscoveryAndChangedSession(t *testing.T) {
	for _, mode := range []string{"unknown", "revoked", "changed_destination"} {
		t.Run(mode, func(t *testing.T) {
			r := newDirectoryDiscoveryRig(t)
			if mode == "unknown" {
				r.fixtures["12448"].marker = "unknown"
			}
			prior := r.app.client.Transport
			r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				resp, err := prior.RoundTrip(req)
				if req.URL.Path == "/api/status" {
					if mode == "revoked" {
						if err := r.store.revoke(context.Background(), r.tokens["12448"]); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "changed_destination" {
						if _, err := r.store.db.Exec("UPDATE hospitals SET api_url=? WHERE id=?", "https://11447.example.invalid", r.sites["12448"].ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				return resp, err
			})
			w := r.request("12448", "/api/auth/backend")
			want := 503
			if mode == "revoked" {
				want = 401
			}
			if w.Code != want || strings.Contains(w.Body.String(), `"backend"`) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
