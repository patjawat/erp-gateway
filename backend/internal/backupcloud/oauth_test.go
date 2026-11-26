package backupcloud

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBeginUsesPrivateDriveScopeAndPKCE(t *testing.T) {
	a, err := Begin("owner.apps.googleusercontent.com", "http://127.0.0.1:8080/admin/api/backups/drive/callback")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Begin("owner.apps.googleusercontent.com", "https://gateway.example.org/admin/api/backups/drive/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	challenge := sha256.Sum256([]byte(a.Verifier))
	if a.State == b.State || a.Verifier == b.Verifier || len(a.State) < 43 || q.Get("state") != a.State || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "https://www.googleapis.com/auth/drive.file" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Fatal("missing state, PKCE or least-privilege consent")
	}
	if time.Until(a.ExpiresAt) < 9*time.Minute || time.Until(a.ExpiresAt) > 10*time.Minute {
		t.Fatal("unexpected attempt expiry")
	}
}

func TestBeginRejectsUnsupportedCallback(t *testing.T) {
	for _, callback := range []string{"http://10.10.8.127:8080/callback", "http://evil.example/callback", "https://name:password@gateway.example/callback", "https://gateway.example/callback#fragment", "file:///tmp/callback", "https://gateway.example/callback?code=old"} {
		if _, err := Begin("client", callback); err == nil {
			t.Errorf("accepted unsupported callback %q", callback)
		}
	}
}

func TestOAuthExchangeRefreshAndRedactedFailure(t *testing.T) {
	var grant, verifier, redirect string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		grant, verifier, redirect = r.Form.Get("grant_type"), r.Form.Get("code_verifier"), r.Form.Get("redirect_uri")
		if r.Form.Get("client_id") != "owner-client" || r.Form.Get("client_secret") != "private-client-secret" {
			t.Error("wrong owner client")
		}
		if r.Form.Get("code") == "revoked" || r.Form.Get("refresh_token") == "revoked" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"private-client-secret refresh-sensitive"}`))
			return
		}
		if grant == "authorization_code" {
			_, _ = w.Write([]byte(`{"access_token":"access-first","refresh_token":"refresh-first","expires_in":3600,"token_type":"Bearer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"access-new","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), TokenURL: server.URL}
	original := Credentials{ClientID: "owner-client", ClientSecret: "private-client-secret", RedirectURL: "https://gateway.example/callback"}
	got, err := c.Exchange(context.Background(), original, "valid-code", strings.Repeat("v", 43))
	if err != nil {
		t.Fatal(err)
	}
	if grant != "authorization_code" || verifier != strings.Repeat("v", 43) || redirect != original.RedirectURL || got.RefreshToken != "refresh-first" || got.AccessToken != "access-first" {
		t.Fatal("exchange did not bind PKCE/client/callback")
	}
	got.Expiry = time.Now().Add(-time.Minute)
	got, err = c.Refresh(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if grant != "refresh_token" || got.AccessToken != "access-new" || got.RefreshToken != "refresh-first" || got.ClientSecret != original.ClientSecret {
		t.Fatal("refresh lost credentials")
	}
	got.RefreshToken, got.Expiry = "revoked", time.Now().Add(-time.Minute)
	_, err = c.Refresh(context.Background(), got)
	if !errors.Is(err, ErrReconnect) || strings.Contains(err.Error(), "private-client-secret") || strings.Contains(err.Error(), "refresh-sensitive") {
		t.Fatalf("failure must be typed and redacted: %v", err)
	}
}

func TestEnsureFolderCreatesAndReusesOnlyAppFolder(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Error("missing access token")
		}
		switch {
		case r.URL.Path == "/files" && r.Method == "GET":
			if !strings.Contains(r.URL.Query().Get("q"), "appProperties") || r.URL.Query().Get("pageSize") != "2" {
				t.Error("folder search must be app-scoped and bounded")
			}
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.URL.Path == "/files" && r.Method == "POST":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["mimeType"] != "application/vnd.google-apps.folder" || body["appProperties"].(map[string]any)["erp_gateway_backup"] != "v1" {
				t.Error("new folder not owned by this app")
			}
			created = true
			_, _ = w.Write([]byte(`{"id":"app-folder_123","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}`))
		case r.URL.Path == "/files/app-folder_123":
			_, _ = w.Write([]byte(`{"id":"app-folder_123","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}`))
		case r.URL.Path == "/files/unrelated":
			_, _ = w.Write([]byte(`{"id":"unrelated","mimeType":"application/vnd.google-apps.folder"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), DriveURL: server.URL}
	credentials := Credentials{ClientID: "client", ClientSecret: "secret", AccessToken: "access"}
	got, err := c.EnsureFolder(context.Background(), credentials)
	if err != nil || !created || got.FolderID != "app-folder_123" {
		t.Fatalf("folder creation: %+v %v", got, err)
	}
	got, err = c.EnsureFolder(context.Background(), got)
	if err != nil || got.FolderID != "app-folder_123" {
		t.Fatalf("folder reuse: %v", err)
	}
	got.FolderID = "unrelated"
	if _, err = c.EnsureFolder(context.Background(), got); err == nil {
		t.Fatal("accepted folder not owned by app")
	}
}

func TestDriveQuotaAndPermissionErrorsAreRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"access-sensitive","errors":[{"reason":"storageQuotaExceeded"}]}}`))
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), DriveURL: server.URL}
	_, err := c.EnsureFolder(context.Background(), Credentials{ClientID: "c", ClientSecret: "s", AccessToken: "access-sensitive"})
	if !errors.Is(err, ErrQuota) || strings.Contains(err.Error(), "access-sensitive") {
		t.Fatalf("expected redacted quota failure: %v", err)
	}
}
