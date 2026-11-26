package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHospitalLoginPolicyIsPublicScopedAndRedactsPrivateMetadata(t *testing.T) {
	rig := rig(t)
	rig.enable(t, rig.add(t, "11447"))
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hospitals/11447/login-methods", nil))
	var response struct {
		Data struct {
			Hospital struct {
				Code string `json:"code"`
			} `json:"hospital"`
			Methods  map[string]bool `json:"login_methods"`
			External map[string]struct {
				Available bool `json:"available"`
			} `json:"external_auth"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data.Hospital.Code != "11447" || !response.Data.Methods["password"] || !response.Data.Methods["provider_id"] || !response.Data.Methods["thaid"] || response.Data.External["provider_id"].Available || response.Data.External["thaid"].Available {
		t.Fatalf("policy response: %d %s", w.Code, w.Body.String())
	}
	for _, private := range []string{"api_url", "gateway_key", "secret", "example.invalid"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private metadata exposed: %s", w.Body.String())
		}
	}
	for path, want := range map[string]int{"/api/hospitals/00000/login-methods": 403, "/api/hospitals/invalid/login-methods": 400} {
		w = httptest.NewRecorder()
		rig.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestLoginPolicyRejectsIncompleteOrCrossTenantUpstreamResponses(t *testing.T) {
	for name, payload := range map[string]string{
		"missing bool":      `{"hospital_code":"11447","login_methods":{"password":true,"thaid":true},"external_auth":{"provider_id":{"available":false},"thaid":{"available":false}}}`,
		"null bool":         `{"hospital_code":"11447","login_methods":{"password":null,"provider_id":true,"thaid":true},"external_auth":{"provider_id":{"available":false},"thaid":{"available":false}}}`,
		"wrong type":        `{"hospital_code":"11447","login_methods":{"password":"true","provider_id":true,"thaid":true},"external_auth":{"provider_id":{"available":false},"thaid":{"available":false}}}`,
		"cross tenant":      `{"hospital_code":"12448","login_methods":{"password":true,"provider_id":true,"thaid":true},"external_auth":{"provider_id":{"available":false},"thaid":{"available":false}}}`,
		"missing readiness": `{"hospital_code":"11447","login_methods":{"password":true,"provider_id":true,"thaid":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := rig(t)
			rig.enable(t, rig.add(t, "11447"))
			rig.app.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/auth/login-methods" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Gateway-Key") == "" {
					t.Fatalf("invalid scoped policy request: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":` + payload + `}`))}, nil
			})}
			w := httptest.NewRecorder()
			rig.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hospitals/11447/login-methods", nil))
			if w.Code != 503 || strings.Contains(w.Body.String(), "login_methods") {
				t.Fatalf("bad upstream policy accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPasswordPolicyRejectionIsRelayedWithoutLosingItsMeaning(t *testing.T) {
	rig := rig(t)
	w := httptest.NewRecorder()
	rig.app.relayError(w, &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"LOGIN_METHOD_DISABLED","message":"private upstream detail"}}`))}, true)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "LOGIN_METHOD_DISABLED") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("policy failure incorrectly relayed: %d %s", w.Code, w.Body.String())
	}
}
