package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fcmCheckFixture(t *testing.T, pkcs1 bool) (string, map[string]string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("cannot generate synthetic RSA key")
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if !pkcs1 {
		block.Type = "PRIVATE KEY"
		block.Bytes, err = x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal("cannot marshal synthetic RSA key")
		}
	}
	credential := map[string]string{
		"type":           "service_account",
		"project_id":     DefaultFCMProject,
		"private_key_id": "synthetic-key",
		"private_key":    string(pem.EncodeToMemory(block)),
		"client_email":   "synthetic-sender@example.test",
		"token_uri":      "https://oauth2.googleapis.com/token",
	}
	path := filepath.Join(t.TempDir(), "synthetic-service-account.json")
	writeFCMCheckFixture(t, path, credential)
	return path, credential
}

func writeFCMCheckFixture(t *testing.T, path string, credential map[string]string) {
	t.Helper()
	raw, err := json.Marshal(credential)
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot write synthetic credential fixture")
	}
}

func fcmCheckResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func requireFCMCheckError(t *testing.T, err error, code string, status int) {
	t.Helper()
	var checked *FCMCheckError
	if !errors.As(err, &checked) || checked.Code != code || checked.HTTPStatus != status {
		t.Fatalf("unexpected preflight result: %v", err)
	}
	if strings.Contains(err.Error(), "SENSITIVE") || strings.Contains(err.Error(), "example.test") || strings.Contains(err.Error(), "BEGIN") {
		t.Fatal("preflight error exposed sensitive data")
	}
}

func TestFCMCheckOnlyValidatesAndNeverTargetsADevice(t *testing.T) {
	for _, pkcs1 := range []bool{false, true} {
		t.Run(map[bool]string{false: "PKCS8", true: "PKCS1"}[pkcs1], func(t *testing.T) {
			path, _ := fcmCheckFixture(t, pkcs1)
			var oauthCalls, fcmCalls int
			result, err := checkFCMCredentials(context.Background(), path, "", transportFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 20*time.Second || r.Method != http.MethodPost {
					t.Fatal("provider call is not bounded or uses an unexpected method")
				}
				switch r.URL.Host {
				case "oauth2.googleapis.com":
					oauthCalls++
					if r.URL.Path != "/token" || r.ParseForm() != nil {
						t.Fatal("unexpected OAuth request")
					}
					assertion := strings.Split(r.Form.Get("assertion"), ".")
					if len(assertion) != 3 {
						t.Fatal("missing signed OAuth assertion")
					}
					claims, err := base64.RawURLEncoding.DecodeString(assertion[1])
					var scope struct {
						Scope string `json:"scope"`
					}
					if err != nil || json.Unmarshal(claims, &scope) != nil || scope.Scope != "https://www.googleapis.com/auth/firebase.messaging" {
						t.Fatal("OAuth requested an unexpected scope")
					}
					return fcmCheckResponse(200, `{"access_token":"SENSITIVE_TEST_TOKEN","token_type":"Bearer","expires_in":3600}`), nil
				case "fcm.googleapis.com":
					fcmCalls++
					if r.URL.Path != "/v1/projects/"+DefaultFCMProject+"/messages:send" || r.Header.Get("Authorization") != "Bearer SENSITIVE_TEST_TOKEN" {
						t.Fatal("unexpected validation endpoint or authorization")
					}
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["validate_only"] != true {
						t.Fatal("preflight attempted a live send")
					}
					message, ok := body["message"].(map[string]any)
					if !ok || message["topic"] != "hospital-erp-preflight" || message["token"] != nil || message["fid"] != nil || message["notification"] != nil || len(body) != 2 {
						t.Fatal("preflight must use generic validation-only topic data")
					}
					return fcmCheckResponse(200, `{"name":"projects/hospital-erp-44fbd/messages/fake_message_id"}`), nil
				default:
					t.Fatal("preflight contacted an unexpected provider")
					return nil, errors.New("unexpected provider")
				}
			}))
			if err != nil || oauthCalls != 1 || fcmCalls != 1 || !result.OAuthVerified || !result.FCMValidated || !result.ValidateOnly || result.NotificationsSent != 0 || result.ProjectID != DefaultFCMProject {
				t.Fatalf("preflight did not establish validation-only access: %+v %v", result, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "SENSITIVE") || strings.Contains(string(raw), "example.test") {
				t.Fatal("preflight result exposed credential information")
			}
		})
	}
}

func TestFCMCheckRejectsWrongProjectOrInvalidKeyBeforeNetwork(t *testing.T) {
	path, valid := fcmCheckFixture(t, false)
	for _, tc := range []struct {
		name, field, value, code string
	}{
		{"wrong project", "project_id", "another-project", "FCM_CHECK_PROJECT_MISMATCH"},
		{"missing key", "private_key", "", "FCM_CHECK_INVALID_PRIVATE_KEY"},
		{"invalid PEM", "private_key", "SENSITIVE_NOT_A_KEY", "FCM_CHECK_INVALID_PRIVATE_KEY"},
		{"wrong credential type", "type", "authorized_user", "FCM_CHECK_INVALID_CREDENTIALS"},
		{"untrusted token endpoint", "token_uri", "https://untrusted.example.test/token", "FCM_CHECK_INVALID_CREDENTIALS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := map[string]string{}
			for k, v := range valid {
				credential[k] = v
			}
			credential[tc.field] = tc.value
			writeFCMCheckFixture(t, path, credential)
			_, err := checkFCMCredentials(context.Background(), path, "", transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid credentials reached the network")
				return nil, errors.New("must not call")
			}))
			requireFCMCheckError(t, err, tc.code, 0)
		})
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if validFCMCheckKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})) {
		t.Fatal("non-RSA key was accepted")
	}
}

func TestFCMCheckSanitizesOAuthAndProviderFailures(t *testing.T) {
	path, _ := fcmCheckFixture(t, false)
	for _, tc := range []struct {
		name, code string
		status     int
		oauth      bool
		network    bool
	}{
		{"OAuth error body", "FCM_CHECK_AUTHENTICATION_FAILED", 0, true, false},
		{"OAuth transport error", "FCM_CHECK_AUTHENTICATION_FAILED", 0, true, true},
		{"FCM permission error", "FCM_CHECK_VALIDATION_REJECTED", 403, false, false},
		{"FCM transport error", "FCM_CHECK_NETWORK_FAILED", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkFCMCredentials(context.Background(), path, "", transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "oauth2.googleapis.com" && !tc.oauth {
					return fcmCheckResponse(200, `{"access_token":"SENSITIVE_TEST_TOKEN","token_type":"Bearer","expires_in":3600}`), nil
				}
				if tc.network {
					return nil, errors.New("SENSITIVE_PROVIDER_TOKEN synthetic-sender@example.test")
				}
				status := tc.status
				if tc.oauth {
					status = 400
				}
				return fcmCheckResponse(status, `{"error":"SENSITIVE_PRIVATE_KEY synthetic-sender@example.test"}`), nil
			}))
			requireFCMCheckError(t, err, tc.code, tc.status)
		})
	}
}

func TestFCMCheckDoesNotFollowOAuthOrFCMRedirects(t *testing.T) {
	path, _ := fcmCheckFixture(t, false)
	for _, oauth := range []bool{true, false} {
		calls := 0
		_, err := checkFCMCredentials(context.Background(), path, "", transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host == "oauth2.googleapis.com" && !oauth {
				return fcmCheckResponse(200, `{"access_token":"SENSITIVE_TEST_TOKEN","token_type":"Bearer","expires_in":3600}`), nil
			}
			if r.URL.Host != "oauth2.googleapis.com" && r.URL.Host != "fcm.googleapis.com" {
				t.Fatal("preflight followed a redirect")
			}
			response := fcmCheckResponse(302, "")
			response.Header.Set("Location", "https://untrusted.example.test/collect")
			return response, nil
		}))
		if oauth {
			requireFCMCheckError(t, err, "FCM_CHECK_AUTHENTICATION_FAILED", 0)
			if calls != 1 {
				t.Fatal("unexpected OAuth redirect call count")
			}
		} else {
			requireFCMCheckError(t, err, "FCM_CHECK_VALIDATION_REJECTED", 302)
			if calls != 2 {
				t.Fatal("unexpected FCM redirect call count")
			}
		}
	}
}

func TestFCMCheckOAuthHonorsCallerCancellation(t *testing.T) {
	path, _ := fcmCheckFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := checkFCMCredentials(ctx, path, "", transportFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(time.Second):
			t.Fatal("OAuth token exchange ignored caller cancellation")
			return nil, errors.New("timeout")
		}
	}))
	requireFCMCheckError(t, err, "FCM_CHECK_AUTHENTICATION_FAILED", 0)
}
