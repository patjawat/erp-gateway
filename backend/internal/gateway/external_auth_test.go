package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func TestExternalGatewayRoutesTenantAndNeverExposesLocalToken(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	localToken := "private-local-identity-token"
	flowID := strings.Repeat("a", 43)
	verifier := strings.Repeat("v", 43)
	status := "pending"
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "11447.example.invalid" || req.Header.Get("X-Gateway-Key") != strings.Repeat("11447", 8) || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("cross-tenant or credential forwarding")
		}
		var value any
		switch req.URL.Path {
		case "/api/auth/external/start":
			value = contracts.ExternalFlow{FlowID: flowID, AuthorizationURL: "https://provider.example/authorize?state=random", ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), PollInterval: 2}
		case "/api/auth/external/status":
			var input contracts.ExternalFlowRequest
			if json.NewDecoder(req.Body).Decode(&input) != nil || input.HospitalCode != h.Code || input.CodeVerifier != verifier {
				t.Fatal("device capability not routed")
			}
			value = contracts.ExternalResult{Status: status, Token: contracts.Token{UserID: json.RawMessage(`7`), AccessToken: localToken, ExpiresAt: time.Now().Add(time.Hour).Unix(), TokenType: "Bearer"}}
		case "/api/auth/external/cancel":
			value = map[string]string{"status": "cancelled"}
		default:
			t.Fatal("unexpected local route", req.URL.Path)
		}
		data, _ := json.Marshal(map[string]any{"data": value})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	start := contracts.ExternalStart{HospitalCode: h.Code, Provider: "provider_id", CodeChallenge: strings.Repeat("c", 43)}
	w := r.request(t, "POST", "/api/auth/external/start", start, "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), localToken) {
		t.Fatal("cannot start tenant flow", w.Code, w.Body.String())
	}
	poll := contracts.ExternalFlowRequest{HospitalCode: h.Code, FlowID: flowID, CodeVerifier: verifier}
	w = r.request(t, "POST", "/api/auth/external/status", poll, "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "access_token") || strings.Contains(w.Body.String(), localToken) {
		t.Fatal("pending response exposed local token")
	}
	status = "complete"
	w = r.request(t, "POST", "/api/auth/external/status", poll, "", false)
	var envelope struct {
		Data contracts.ExternalResult `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data.Status != "complete" || envelope.Data.AccessToken == "" || strings.Contains(w.Body.String(), localToken) {
		t.Fatal("local token leaked or no gateway session", w.Body.String())
	}
	stored, err := r.app.config.Store.session(t.Context(), envelope.Data.AccessToken)
	if err != nil || stored.hospitalID != h.ID {
		t.Fatal("gateway session not tenant-bound")
	}
	w = r.request(t, "POST", "/api/auth/external/cancel", poll, "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatal("cannot cancel flow")
	}
}

func TestExternalGatewayRejectsInvalidAndDisabledTenantBeforeUpstream(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	w := r.request(t, "POST", "/api/auth/external/start", contracts.ExternalStart{HospitalCode: h.Code, Provider: "thaid", CodeChallenge: strings.Repeat("c", 43)}, "", false)
	if w.Code != 403 || r.erpCalls[h.Code] != 0 {
		t.Fatal("disabled tenant reached upstream")
	}
	r.enable(t, h)
	for _, input := range []contracts.ExternalStart{{HospitalCode: h.Code, Provider: "email", CodeChallenge: strings.Repeat("c", 43)}, {HospitalCode: h.Code, Provider: "thaid", CodeChallenge: "invalid"}} {
		if w := r.request(t, "POST", "/api/auth/external/start", input, "", false); w.Code != 400 {
			t.Fatal("invalid external start accepted")
		}
	}
}
