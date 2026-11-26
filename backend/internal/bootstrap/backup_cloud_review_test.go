package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/backupcloud"
)

func cloudReviewRemoteTool(t *testing.T, s *backupService, ids ...string) string {
	t.Helper()
	path := filepath.Join(s.dir, "stateful-restic")
	script := `#!/bin/sh
set -eu
base=${0%/*}
operation=
while [ "$#" -gt 0 ]; do
  operation=$1
  shift
  case "$operation" in snapshots|forget|prune) break;; esac
done
case "$operation" in
 snapshots)
  printf '['
  separator=
  while IFS= read -r snapshot; do
   test -n "$snapshot" || continue
   printf '%s{"id":"%s","time":"2026-11-26T00:00:00Z"}' "$separator" "$snapshot"
   separator=,
  done < "$base/remote.ids"
  printf ']'
  ;;
 forget)
  for snapshot do
   printf '%s\n' "$snapshot" >> "$base/deleted.ids"
   awk -v snapshot="$snapshot" '$0 != snapshot' "$base/remote.ids" > "$base/remote.next"
   mv "$base/remote.next" "$base/remote.ids"
  done
  ;;
 prune) exit 0;;
 *) exit 37;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "remote.ids"), []byte(strings.Join(ids, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func cloudReviewRecord(t *testing.T, s *backupService, id string, created time.Time) backupRecord {
	t.Helper()
	r := backupRecord{ID: backupID(), CreatedAt: created, Status: "completed", RemoteSnapshotID: id, RemoteScope: cloudScope(s.state.Drive, s.state.Dataset)}
	r.OffsiteAt = &r.CreatedAt
	if err := s.putRecord(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDriveRetentionRanksOnlyExistingObjectsAndClearsDeletedIDs(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	s.state.Drive = cloudPolicyCredentials()
	oldID, missingID, newID, unknownID, nextID := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64), strings.Repeat("5", 64)
	old := cloudReviewRecord(t, s, oldID, time.Now().Add(-48*time.Hour))
	missing := cloudReviewRecord(t, s, missingID, time.Now().Add(-24*time.Hour))
	newest := cloudReviewRecord(t, s, newID, time.Now())
	s.binary = cloudReviewRemoteTool(t, s, newID, oldID, unknownID)
	if err := s.retainDrive(context.Background(), backupRetention{Latest: 2}); err != nil {
		t.Fatalf("missing historical object consumed a keep slot: %v", err)
	}
	got, err := s.getRecord(missing.ID)
	if err != nil || got.RemoteSnapshotID != "" || got.OffsiteAt == nil {
		t.Fatal("missing remote ID was not cleared while preserving offsite history")
	}
	if err = os.WriteFile(filepath.Join(s.dir, "remote.ids"), []byte(strings.Join([]string{nextID, newID, oldID, unknownID}, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cloudReviewRecord(t, s, nextID, time.Now().Add(time.Hour))
	if err = s.retainDrive(context.Background(), backupRetention{Latest: 2}); err != nil {
		t.Fatal(err)
	}
	got, err = s.getRecord(old.ID)
	if err != nil || got.RemoteSnapshotID != "" || got.OffsiteAt == nil {
		t.Fatal("deleted remote object remains advertised as restorable")
	}
	if got, err = s.getRecord(newest.ID); err != nil || got.RemoteSnapshotID != newID {
		t.Fatal("existing newest snapshot was removed")
	}
	remaining, err := os.ReadFile(filepath.Join(s.dir, "remote.ids"))
	if err != nil || !strings.Contains(string(remaining), unknownID) || strings.Contains(string(remaining), oldID) {
		t.Fatal("unknown snapshot lost or expired snapshot retained")
	}
	if err = s.retainDrive(context.Background(), backupRetention{Latest: 2}); err != nil {
		t.Fatalf("second retention cycle broke on deleted catalog IDs: %v", err)
	}
}

func TestBackupStatusScopesLatestOffsiteToCurrentRestorableConnection(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	s.state.Drive = cloudPolicyCredentials()
	s.state.Policy.Enabled = true
	s.state.Policy.Destination = "drive"
	current := cloudReviewRecord(t, s, strings.Repeat("1", 64), time.Now().Add(-24*time.Hour))
	foreign := cloudReviewRecord(t, s, strings.Repeat("2", 64), time.Now())
	foreign.RemoteScope = "other-connection"
	if err := s.putRecord(foreign); err != nil {
		t.Fatal(err)
	}
	cloudReviewRecord(t, s, "", time.Now().Add(time.Minute))
	w := httptest.NewRecorder()
	s.statusHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/api/backups", nil))
	var response struct {
		Data struct {
			Latest *backupRecord `json:"latest_offsite"`
			Health struct {
				Stale bool `json:"offsite_stale"`
			} `json:"health"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data.Latest == nil || response.Data.Latest.ID != current.ID || !response.Data.Health.Stale {
		t.Fatal("offsite status used a different connection or deleted object")
	}
}

func cloudReviewRecovery(t *testing.T, s *backupService) *cloudRecovery {
	t.Helper()
	creds := cloudPolicyCredentials()
	creds.ClientID = "old-recovery-client"
	creds.FolderID = "old-recovery-folder"
	recovery := &cloudRecovery{Secret: strings.Repeat("r", 43), Dataset: strings.Repeat("a", 32), Credentials: creds}
	s.recovery = recovery
	if err := s.saveSealed(filepath.Join(s.dir, "cloud-recovery.enc"), recovery); err != nil {
		t.Fatal(err)
	}
	return recovery
}

func TestDriveDisconnectClearsRecoveryContextAndPendingOAuth(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	s.m = &Manager{}
	s.state.Drive = cloudPolicyCredentials()
	cloudReviewRecovery(t, s)
	s.oauth = &backupcloud.Attempt{State: "pending", ExpiresAt: time.Now().Add(time.Minute)}
	s.oauthRecovery = s.recovery
	s.oauthDataset = s.state.Dataset
	s.oauthBase = s.state.Drive
	status := httptest.NewRecorder()
	s.statusHTTP(status, httptest.NewRequest(http.MethodGet, "/admin/api/backups", nil))
	var before struct {
		Data struct {
			Drive struct {
				Source   string `json:"connection_source"`
				Ready    bool   `json:"recovery_ready"`
				Selected string `json:"selected_dataset"`
			} `json:"drive"`
			KeyID string `json:"recovery_key_id"`
		} `json:"data"`
	}
	if json.Unmarshal(status.Body.Bytes(), &before) != nil || before.Data.Drive.Source != "recovery" || !before.Data.Drive.Ready || before.Data.Drive.Selected != s.recovery.Dataset || before.Data.KeyID != recoveryKeyID(s.state.Secret) {
		t.Fatal("status does not distinguish temporary recovery from the configured account")
	}
	w := httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/api/backups/drive/disconnect", strings.NewReader(`{}`)))
	if w.Code != 200 {
		t.Fatalf("disconnect rejected: %d", w.Code)
	}
	if source, err := s.cloudSource(); err == nil || source.Credentials.RefreshToken != "" {
		t.Fatal("disconnected account still resolves old recovery credentials")
	}
	if s.recovery != nil || s.oauth != nil || s.oauthRecovery != nil {
		t.Fatal("recovery or pending OAuth context survived disconnect")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "cloud-recovery.enc")); !os.IsNotExist(err) {
		t.Fatal("old recovery context would reload after restart")
	}
	status = httptest.NewRecorder()
	s.statusHTTP(status, httptest.NewRequest(http.MethodGet, "/admin/api/backups", nil))
	if json.Unmarshal(status.Body.Bytes(), &before) != nil || before.Data.Drive.Source != "none" || before.Data.Drive.Ready {
		t.Fatal("disconnected recovery remains advertised as ready")
	}
}

type cloudReviewTransport func(*http.Request) (*http.Response, error)

func (f cloudReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNormalDriveReconnectSupersedesRecoveryContext(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	s.m = &Manager{}
	s.state.Drive = cloudPolicyCredentials()
	cloudReviewRecovery(t, s)
	s.oauth = &backupcloud.Attempt{State: "owner-state", Verifier: strings.Repeat("v", 43), ExpiresAt: time.Now().Add(time.Minute)}
	s.oauthCredentials = backupcloud.Credentials{ClientID: "new-owner-client", ClientSecret: "new-owner-secret", RedirectURL: "https://gateway.example/backup/oauth/callback"}
	s.oauthBase = s.state.Drive
	s.oauthDataset = s.state.Dataset
	s.oauthRecovery = nil
	previous := http.DefaultTransport
	http.DefaultTransport = cloudReviewTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token":
			body = `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"token_type":"Bearer"}`
		case r.URL.Host == "www.googleapis.com" && r.URL.Path == "/drive/v3/files":
			body = `{"files":[{"id":"new-folder","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}]}`
		default:
			return nil, errors.New("unexpected fake OAuth request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	w := httptest.NewRecorder()
	s.oauthCallback(w, httptest.NewRequest(http.MethodGet, "/backup/oauth/callback?state=owner-state&code=approved", nil))
	if w.Code != 200 {
		t.Fatalf("normal connection failed: %d", w.Code)
	}
	source, err := s.cloudSource()
	if err != nil || source.Credentials.ClientID != "new-owner-client" || source.Credentials.RefreshToken != "new-refresh" || source.Dataset != s.state.Dataset || s.recovery != nil {
		t.Fatal("normal connection still uses old recovery source")
	}
	if _, err = os.Stat(filepath.Join(s.dir, "cloud-recovery.enc")); !os.IsNotExist(err) {
		t.Fatal("old recovery connection survives restart")
	}
}

func TestDisconnectedRecoveryCallbackCannotResurrectContext(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	s.m = &Manager{}
	s.state.Drive = cloudPolicyCredentials()
	recovery := cloudReviewRecovery(t, s)
	s.oauth = &backupcloud.Attempt{State: "pending-recovery", Verifier: strings.Repeat("v", 43), ExpiresAt: time.Now().Add(time.Minute)}
	s.oauthRecovery = recovery
	s.oauthCredentials = recovery.Credentials
	s.oauthBase, s.oauthDataset = s.state.Drive, s.state.Dataset
	started, release := make(chan struct{}), make(chan struct{})
	previous := http.DefaultTransport
	http.DefaultTransport = cloudReviewTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token":
			close(started)
			<-release
			body = `{"access_token":"late-access","refresh_token":"late-refresh","expires_in":3600,"token_type":"Bearer"}`
		case r.URL.Host == "www.googleapis.com" && r.URL.Path == "/drive/v3/files/old-recovery-folder":
			body = `{"id":"old-recovery-folder","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}`
		default:
			return nil, errors.New("unexpected fake recovery callback request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	callback := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.oauthCallback(callback, httptest.NewRequest(http.MethodGet, "/backup/oauth/callback?state=pending-recovery&code=approved", nil))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("callback did not reach fake token exchange")
	}
	w := httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/api/backups/drive/disconnect", strings.NewReader(`{}`)))
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled callback did not finish")
	}
	if w.Code != 200 || callback.Code != 409 || s.recovery != nil || s.state.Drive.RefreshToken != "" {
		t.Fatal("late recovery callback recreated a disconnected context")
	}
}
