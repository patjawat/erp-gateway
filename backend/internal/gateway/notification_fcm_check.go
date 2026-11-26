package gateway

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const DefaultFCMProject = "hospital-erp-44fbd"

// FCMCheckResult confirms provider authorization, not delivery to a device.
type FCMCheckResult struct {
	ProjectID         string `json:"project_id"`
	OAuthVerified     bool   `json:"oauth_verified"`
	FCMValidated      bool   `json:"fcm_validated"`
	ValidateOnly      bool   `json:"validate_only"`
	NotificationsSent int    `json:"notifications_sent"`
}

// FCMCheckError deliberately omits credential fields and provider response
// bodies. Its code and optional HTTP status are safe for operational output.
type FCMCheckError struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (e *FCMCheckError) Error() string { return e.Code }

// CheckFCMCredentials checks only an RSA service-account key and the FCM
// validate-only API. It never starts the Gateway, opens its DB, or sends a
// notification. The expected project defaults to the shared Mobile project.
func CheckFCMCredentials(ctx context.Context, path, expectedProject string) (FCMCheckResult, error) {
	return checkFCMCredentials(ctx, path, expectedProject, http.DefaultTransport)
}

func checkFCMCredentials(ctx context.Context, path, expectedProject string, transport http.RoundTripper) (FCMCheckResult, error) {
	if expectedProject == "" {
		expectedProject = DefaultFCMProject
	}
	result := FCMCheckResult{ProjectID: expectedProject, ValidateOnly: true}
	fail := func(code string, status int) (FCMCheckResult, error) {
		return result, &FCMCheckError{Code: code, HTTPStatus: status}
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`).MatchString(expectedProject) {
		return fail("FCM_CHECK_INVALID_PROJECT", 0)
	}
	f, err := os.Open(path)
	if err != nil {
		return fail("FCM_CHECK_CREDENTIALS_UNREADABLE", 0)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	f.Close()
	if err != nil || len(raw) > 64*1024 {
		return fail("FCM_CHECK_INVALID_CREDENTIALS", 0)
	}
	var credential map[string]string
	if json.Unmarshal(raw, &credential) != nil || credential["type"] != "service_account" || credential["client_email"] == "" || credential["token_uri"] != "https://oauth2.googleapis.com/token" {
		return fail("FCM_CHECK_INVALID_CREDENTIALS", 0)
	}
	if credential["project_id"] != expectedProject {
		return fail("FCM_CHECK_PROJECT_MISMATCH", 0)
	}
	if !validFCMCheckKey([]byte(credential["private_key"])) {
		return fail("FCM_CHECK_INVALID_PRIVATE_KEY", 0)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sender, err := NewFCMSender(ctx, path)
	if err != nil {
		return fail("FCM_CHECK_INVALID_CREDENTIALS", 0)
	}
	// NewFCMSender reopens the supplied file. Check again in case it was replaced
	// between validation and construction, before making any network request.
	if sender.project != expectedProject {
		return fail("FCM_CHECK_PROJECT_MISMATCH", 0)
	}
	sender.client.Transport = fcmCheckTransport{ctx: ctx, base: transport}
	auth, err := sender.tokens.Token()
	if err != nil || auth == nil || !auth.Valid() {
		return fail("FCM_CHECK_AUTHENTICATION_FAILED", 0)
	}
	result.OAuthVerified = true
	// This fixed body cannot be switched into a live send through CLI options.
	// A topic target avoids using or exposing any real device identifier.
	const body = `{"validate_only":true,"message":{"topic":"hospital-erp-preflight","data":{"preflight":"true"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fcm.googleapis.com/v1/projects/"+expectedProject+"/messages:send", strings.NewReader(body))
	if err != nil {
		return fail("FCM_CHECK_INVALID_REQUEST", 0)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := sender.client.Do(req)
	if err != nil {
		return fail("FCM_CHECK_NETWORK_FAILED", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("FCM_CHECK_VALIDATION_REJECTED", resp.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, 16384+1))
	if err != nil || len(response) > 16384 {
		return fail("FCM_CHECK_INVALID_RESPONSE", 0)
	}
	var accepted struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(response, &accepted) != nil || !strings.HasPrefix(accepted.Name, "projects/") || !strings.Contains(accepted.Name, "/messages/") {
		return fail("FCM_CHECK_INVALID_RESPONSE", 0)
	}
	result.FCMValidated = true
	return result, nil
}

func validFCMCheckKey(raw []byte) bool {
	block, rest := pem.Decode(raw)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return false
	}
	var key *rsa.PrivateKey
	if block.Type == "PRIVATE KEY" {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return false
		}
		key, _ = parsed.(*rsa.PrivateKey)
	} else if block.Type == "RSA PRIVATE KEY" {
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	return key != nil && key.N.BitLen() >= 2048 && key.Validate() == nil
}

type fcmCheckTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t fcmCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || (req.URL.Host != "oauth2.googleapis.com" && req.URL.Host != "fcm.googleapis.com") {
		return nil, errors.New("FCM preflight endpoint is not allowed")
	}
	// OAuth's JWT token exchange must honor the same overall deadline as the
	// subsequent validate-only call, including when the caller cancels early.
	return t.base.RoundTrip(req.Clone(t.ctx))
}
