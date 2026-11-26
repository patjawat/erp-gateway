package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func backupControlFixture(t *testing.T, handler http.Handler) string {
	t.Helper()
	directory := controlTestDir(t)
	control, err := StartAdminControl(directory, func(context.Context, string) (string, error) { return "same-admin", nil }, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	return directory
}

func TestBackupControlDispatchIsPrivateAndPreservesPasswordReset(t *testing.T) {
	var calls atomic.Int32
	directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/admin/api/backups/history" || r.URL.Query().Get("limit") != "2" {
			t.Error("incorrect local rewrite")
		}
		jsonData(w, map[string]bool{"ok": true})
	}))
	client, cleanup, err := backupControlClient(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	response, err := client.Get("http://unix/backups/history?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || calls.Load() != 1 {
		t.Fatal("backup handler was not reached")
	}
	for _, path := range []string{"/backups-other", "/admin/api/backups/history", "/backups/../password"} {
		response, err = client.Get("http://unix" + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("private path escaped backup scope")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unrelated control route delegated to backup handler")
	}
	username, err := RequestAdminPasswordReset(context.Background(), directory, "1")
	if err != nil || username != "same-admin" {
		t.Fatalf("password command compatibility lost: %v", err)
	}
}

func TestRunBackupExportStreamsBoundBundleAndPrivateKit(t *testing.T) {
	jobID, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
	payload := bytes.Repeat([]byte("encrypted-repository-fixture"), 10000)
	kit := map[string]any{"version": 1, "secret": strings.Repeat("s", 43), "dataset": strings.Repeat("d", 32)}
	directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/api/backups":
			jsonData(w, map[string]any{"job": backupJob{ID: jobID, State: "running"}})
		case "/admin/api/backups/jobs/" + jobID:
			jsonData(w, backupJob{ID: jobID, State: "completed", BackupID: id})
		case "/admin/api/backups/" + id + "/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
		case "/admin/api/backups/recovery-kit":
			var in struct {
				BackupID string `json:"backup_id"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.BackupID != id {
				t.Error("recovery kit must be bound to exported capture")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(kit)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	output := t.TempDir()
	bundlePath, kitPath := filepath.Join(output, "gateway.bundle"), filepath.Join(output, "kit.json")
	if err := RunBackupExport(context.Background(), directory, bundlePath, kitPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bundlePath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("downloaded bundle differs")
	}
	for _, path := range []string{bundlePath, kitPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("exported files must be private")
		}
	}
	kitBytes, err := os.ReadFile(kitPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(kitBytes, &decoded) != nil || decoded["secret"] != kit["secret"] {
		t.Fatal("recovery secret not exported")
	}
}

func TestRunBackupExportRefusesOverwriteAndCleansPartialFiles(t *testing.T) {
	var calls atomic.Int32
	directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "fixture-sensitive", 500) }))
	output := t.TempDir()
	bundle, kit := filepath.Join(output, "bundle"), filepath.Join(output, "kit")
	if err := os.WriteFile(kit, []byte("keep-existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunBackupExport(context.Background(), directory, bundle, kit); err == nil {
		t.Fatal("existing kit overwritten")
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Fatal("partial bundle left after reserve failure")
	}
	if got, _ := os.ReadFile(kit); string(got) != "keep-existing" || calls.Load() != 0 {
		t.Fatal("preflight caused side effects")
	}
	if err := RunBackupExport(context.Background(), directory, bundle, bundle); err == nil {
		t.Fatal("same output paths accepted")
	}
	if err := os.Remove(kit); err != nil {
		t.Fatal(err)
	}
	if err := RunBackupExport(context.Background(), directory, bundle, kit); err == nil || strings.Contains(err.Error(), "fixture-sensitive") {
		t.Fatal("failed export must be redacted")
	}
	for _, path := range []string{bundle, kit} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("partial secret export remains")
		}
	}
}

func TestRunBackupImportRequiresBothConfirmations(t *testing.T) {
	for _, confirmation := range []string{"no\n", "RESTORE\nno\n", "RESTORE\nSOURCE STOPPED\n"} {
		t.Run(strings.ReplaceAll(confirmation, "\n", "-"), func(t *testing.T) {
			inspectJob, activationJob, id := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
			var activations atomic.Int32
			payload := []byte("encrypted fixture bundle")
			directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/admin/api/backups/restore":
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					fields := map[string][]byte{}
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						fields[part.FormName()], _ = io.ReadAll(part)
						part.Close()
					}
					if !bytes.Equal(fields["file"], payload) || string(fields["recovery_secret"]) != strings.Repeat("s", 43) || string(fields["public_url"]) != "https://new.example" {
						t.Error("multipart import did not preserve expected values")
					}
					jsonData(w, map[string]any{"job": backupJob{ID: inspectJob, State: "running"}})
				case "/admin/api/backups/jobs/" + inspectJob:
					jsonData(w, backupJob{ID: inspectJob, State: "completed", RestoreID: id})
				case "/admin/api/backups/restore/" + id:
					jsonData(w, restoreInspection{ID: id, CreatedAt: time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC), PublicURL: "https://new.example", Ready: true, Summary: map[string]int64{"hospitals": 2, "sessions": 3}})
				case "/admin/api/backups/restore/" + id + "/activate":
					activations.Add(1)
					var in struct {
						ConfirmID     string `json:"confirm_id"`
						PublicURL     string `json:"public_url"`
						SourceStopped bool   `json:"source_stopped"`
					}
					if json.NewDecoder(r.Body).Decode(&in) != nil || in.ConfirmID != id || !in.SourceStopped || in.PublicURL != "https://new.example" {
						t.Error("activation confirmation did not bind selected restore")
					}
					jsonData(w, map[string]any{"job": backupJob{ID: activationJob, State: "running"}})
				case "/admin/api/backups/jobs/" + activationJob:
					jsonData(w, backupJob{ID: activationJob, State: "completed"})
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			files := t.TempDir()
			bundle, kit := filepath.Join(files, "bundle"), filepath.Join(files, "kit")
			if err := os.WriteFile(bundle, payload, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(kit, []byte(`{"version":1,"secret":"`+strings.Repeat("s", 43)+`","dataset":"`+strings.Repeat("d", 32)+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := RunBackupImport(context.Background(), directory, bundle, kit, "https://new.example", strings.NewReader(confirmation), &output)
			accepted := confirmation == "RESTORE\nSOURCE STOPPED\n"
			if (err == nil) != accepted || (activations.Load() == 1) != accepted {
				t.Fatalf("confirmation boundary failed: accepted=%t activations=%d err=%v", accepted, activations.Load(), err)
			}
			if !strings.Contains(output.String(), "2569") || !strings.Contains(output.String(), "หน่วยงาน: 2") || strings.Contains(output.String(), strings.Repeat("s", 43)) {
				t.Fatal("missing summary/date or recovery secret printed")
			}
		})
	}
}

func TestRunBackupControlHonorsContext(t *testing.T) {
	id := strings.Repeat("a", 32)
	directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			jsonData(w, map[string]any{"job": backupJob{ID: id, State: "running"}})
			return
		}
		jsonData(w, backupJob{ID: id, State: "running"})
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	output := t.TempDir()
	bundle, kit := filepath.Join(output, "bundle"), filepath.Join(output, "kit")
	if err := RunBackupExport(ctx, directory, bundle, kit); err == nil {
		t.Fatal("unbounded pending job")
	}
	for _, path := range []string{bundle, kit} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("cancelled export left partial files")
		}
	}
}

func TestRunBackupControlRedactsFailedJob(t *testing.T) {
	id := strings.Repeat("a", 32)
	directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			jsonData(w, map[string]any{"job": backupJob{ID: id, State: "running"}})
			return
		}
		jsonData(w, backupJob{ID: id, State: "failed", Error: "fixture-sensitive-recovery-secret"})
	}))
	output := t.TempDir()
	bundle, kit := filepath.Join(output, "bundle"), filepath.Join(output, "kit")
	if err := RunBackupExport(context.Background(), directory, bundle, kit); err == nil || strings.Contains(err.Error(), "fixture-sensitive") {
		t.Fatal("failed job was successful or leaked response material")
	}
	for _, path := range []string{bundle, kit} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("failed job left partial export files")
		}
	}
}

func TestRunMigrationCancelOfflineRequiresStoppedTargetAndAuthority(t *testing.T) {
	for _, confirmation := range []string{"no\n", "DESTINATION STOPPED\nno\n", "DESTINATION STOPPED\nUSE SOURCE DATA\n"} {
		t.Run(strings.ReplaceAll(confirmation, "\n", "-"), func(t *testing.T) {
			var calls atomic.Int32
			directory := backupControlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/admin/api/backups/migration/cancel" || r.Method != http.MethodPost {
					t.Error("unexpected cancellation route")
				}
				var in struct {
					Confirmed bool   `json:"destination_inactive_confirmed"`
					Override  bool   `json:"offline_override"`
					Authority string `json:"authoritative_source"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || !in.Confirmed || !in.Override || in.Authority != "USE SOURCE DATA" {
					t.Error("incomplete explicit offline override")
				}
				jsonData(w, map[string]bool{"cancelled": true})
			}))
			var output bytes.Buffer
			err := RunMigrationCancelOffline(context.Background(), directory, strings.NewReader(confirmation), &output)
			accepted := confirmation == "DESTINATION STOPPED\nUSE SOURCE DATA\n"
			if (err == nil) != accepted || (calls.Load() == 1) != accepted {
				t.Fatalf("offline cancellation boundary failed: %v calls=%d", err, calls.Load())
			}
			if !strings.Contains(output.String(), "สำรองข้อมูลปลายทางก่อน") {
				t.Fatal("missing destination divergence/backup warning")
			}
		})
	}
}
