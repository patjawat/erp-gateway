package backupcloud

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparedConfigPrivateAndRefreshedCredentialsSurviveFailure(t *testing.T) {
	credentials := Credentials{ClientID: "owner-client", ClientSecret: "private-secret", RedirectURL: "https://gateway.example/callback", FolderID: "app-folder_123", AccessToken: "access-first", RefreshToken: "refresh-first", Expiry: time.Now().Add(time.Hour)}
	p, err := Prepare(context.Background(), credentials, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Repository != "rclone:gateway:repository" {
		t.Fatalf("unexpected repository %q", p.Repository)
	}
	var path string
	for _, env := range p.Env {
		if strings.HasPrefix(env, "RCLONE_CONFIG=") {
			path = strings.TrimPrefix(env, "RCLONE_CONFIG=")
		}
		if strings.Contains(env, "private-secret") || strings.Contains(env, "access-first") {
			t.Fatal("secret in environment")
		}
	}
	if path == "" {
		t.Fatal("missing config environment")
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("config must be private")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || parent.Mode().Perm() != 0700 {
		t.Fatal("directory must be private")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "scope = drive.file") || !strings.Contains(string(body), "root_folder_id = app-folder_123") {
		t.Fatal("config lost least scope/root folder")
	}
	token, _ := json.Marshal(map[string]any{"access_token": "refreshed-after-failed-upload", "refresh_token": "refresh-rotated", "expiry": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano), "token_type": "Bearer"})
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "token = ") {
			line = "token = " + string(token)
		}
		lines = append(lines, line)
	}
	if err = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := p.Credentials()
	if err != nil || got.AccessToken != "refreshed-after-failed-upload" || got.RefreshToken != "refresh-rotated" || got.ClientSecret != credentials.ClientSecret {
		t.Fatalf("refreshed credentials lost: %v", err)
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("plaintext config directory remains")
	}
}

func TestPrepareRejectsConfigInjection(t *testing.T) {
	for _, credentials := range []Credentials{
		{ClientID: "client\n[attacker]", ClientSecret: "secret", FolderID: "folder", AccessToken: "access"},
		{ClientID: "client", ClientSecret: "secret\r\ntype = local", FolderID: "folder", AccessToken: "access"},
		{ClientID: "client", ClientSecret: "secret", FolderID: "folder/../../etc", AccessToken: "access"},
	} {
		if p, err := Prepare(context.Background(), credentials, t.TempDir()); err == nil {
			_ = p.Close()
			t.Fatal("accepted rclone config injection")
		}
	}
}

func TestRcloneAcceptsPreparedConfig(t *testing.T) {
	binary := os.Getenv("GATEWAY_TEST_RCLONE")
	if binary == "" {
		t.Skip("set GATEWAY_TEST_RCLONE to a verified rclone binary for integration")
	}
	credentials := Credentials{ClientID: "test-owner-client", ClientSecret: "test-client-secret", FolderID: "test-app-folder", AccessToken: "test-access-token", RefreshToken: "test-refresh-token", Expiry: time.Now().Add(time.Hour)}
	p, err := Prepare(context.Background(), credentials, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	command := exec.Command(binary, "config", "dump")
	command.Env = append(os.Environ(), p.Env...)
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("rclone could not read private config: %v", err)
	}
	var result map[string]map[string]string
	if json.Unmarshal(raw, &result) != nil {
		t.Fatal("rclone returned an invalid configuration document")
	}
	got := result["gateway"]
	if got["type"] != "drive" || got["scope"] != "drive.file" || got["root_folder_id"] != credentials.FolderID || got["client_id"] != credentials.ClientID || got["client_secret"] != credentials.ClientSecret || !strings.Contains(got["token"], credentials.AccessToken) {
		t.Fatal("rclone did not recognize the owner-scoped configuration")
	}
}
