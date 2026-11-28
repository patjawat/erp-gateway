package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func TestTokenExchangePreservesOpaqueLoginDataAndMetadata(t *testing.T) {
	for _, endpoint := range []string{"/api/auth/login", "/api/auth/external/status"} {
		t.Run(endpoint, func(t *testing.T) {
			r := rig(t)
			h := r.add(t, "11447")
			r.enable(t, h)
			r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != endpoint || req.Header.Get("Authorization") != "" {
					t.Fatal("wrong token exchange boundary")
				}
				w := httptest.NewRecorder()
				writeJSON(w, 200, map[string]any{
					"data": map[string]any{
						"user_id": 7, "access_token": "upstream-private-token", "token_type": "Bearer", "scope": "private:scope", "expires_at": time.Now().Add(48 * time.Hour).Unix(),
						"status": "complete", "profile": map[string]any{"future_field": []any{"opaque", nil, "1.000"}}, "future_token_metadata": true,
					},
					"future_envelope_metadata": map[string]any{"revision": "opaque"},
				})
				return w.Result(), nil
			})
			var input any = contracts.Login{HospitalCode: h.Code, Username: "staff", Password: "secret"}
			if endpoint != "/api/auth/login" {
				input = contracts.ExternalFlowRequest{HospitalCode: h.Code, FlowID: strings.Repeat("f", 43), CodeVerifier: strings.Repeat("v", 43)}
			}
			w := r.request(t, "POST", endpoint, input, "", false)
			var envelope map[string]json.RawMessage
			var data map[string]json.RawMessage
			var token contracts.Token
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || json.Unmarshal(envelope["data"], &data) != nil || json.Unmarshal(envelope["data"], &token) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			if string(data["profile"]) != `{"future_field":["opaque",null,"1.000"]}` || string(data["future_token_metadata"]) != "true" || string(envelope["future_envelope_metadata"]) != `{"revision":"opaque"}` {
				t.Fatal("opaque login data lost:", w.Body.String())
			}
			if token.AccessToken == "" || strings.Contains(w.Body.String(), "upstream-private-token") || token.Scope != "mobile:read" || token.ExpiresAt > time.Now().Add(24*time.Hour).Unix() {
				t.Fatal("token exchange security contract changed:", w.Body.String())
			}
			if session, err := r.app.config.Store.session(t.Context(), token.AccessToken); err != nil || session.hospitalID != h.ID {
				t.Fatal("token not bound to selected hospital", err)
			}
		})
	}
}
