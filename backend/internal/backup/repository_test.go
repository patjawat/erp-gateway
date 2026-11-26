package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func integrationRepository(t *testing.T) Repository {
	t.Helper()
	binary := os.Getenv("GATEWAY_TEST_RESTIC")
	if binary == "" {
		t.Skip("set GATEWAY_TEST_RESTIC to run real encrypted-repository integration")
	}
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("test-only-random-backup-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Repository{Binary: binary, Path: filepath.Join(dir, "repository"), PasswordFile: password}
}

func TestRepositoryRealRoundTripPortableAndWrongKey(t *testing.T) {
	repo := integrationRepository(t)
	// Native development supplies relative data/config paths. Resolve them
	// before Backup changes the subprocess working directory to the capture.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo.Path, err = filepath.Rel(cwd, repo.Path)
	if err != nil {
		t.Fatal(err)
	}
	repo.PasswordFile, err = filepath.Rel(cwd, repo.PasswordFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "capture")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "runtime.json"), []byte("secret patient token"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := repo.Backup(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if !objectID(id) {
		t.Fatalf("invalid snapshot id %q", id)
	}
	if err := repo.Check(ctx); err != nil {
		t.Fatal(err)
	}
	snapshots, err := repo.Snapshots(ctx)
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != id {
		t.Fatalf("snapshots=%v err=%v", snapshots, err)
	}
	bundle := filepath.Join(t.TempDir(), "complete.gateway")
	if err := Pack(ctx, repo.Path, bundle); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret patient token") || strings.Contains(string(raw), "test-only-random-backup-secret") {
		t.Fatal("portable file exposed plaintext")
	}
	repoCopy := repo
	repoCopy.Path = filepath.Join(t.TempDir(), "imported-repository")
	if err := Unpack(ctx, bundle, repoCopy.Path, 64<<20); err != nil {
		t.Fatal(err)
	}
	if err := repoCopy.Check(ctx); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := repoCopy.Restore(ctx, id, restored); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(restored, "runtime.json"))
	if err != nil || string(body) != "secret patient token" {
		t.Fatalf("restored logical path body=%q err=%v", body, err)
	}
	wrong := filepath.Join(t.TempDir(), "wrong-password")
	if err := os.WriteFile(wrong, []byte("wrong-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	repoCopy.PasswordFile = wrong
	if err := repoCopy.Check(ctx); err == nil {
		t.Fatal("wrong recovery key accepted")
	}
}

func TestRepositoryRefusesAmbiguousRestoreAndLeakingCommandErrors(t *testing.T) {
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("not-for-output"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "restic")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'private-upstream-token and not-for-output' >&2\nexit 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	repo := Repository{Binary: binary, Path: filepath.Join(dir, "repo"), PasswordFile: password}
	if err := repo.Check(context.Background()); err == nil || strings.Contains(err.Error(), "private-upstream-token") || strings.Contains(err.Error(), "not-for-output") {
		t.Fatalf("unsafe command error: %v", err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '{\"message_type\":\"summary\",\"snapshot_id\":\""+strings.Repeat("a", 64)+"\"}'\nexit 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.Backup(context.Background(), t.TempDir()); err == nil || id != "" {
		t.Fatalf("partial capture accepted: id=%q err=%v", id, err)
	}
	if err := repo.Restore(context.Background(), "latest", filepath.Join(dir, "restore")); err == nil {
		t.Fatal("ambiguous snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repo.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command err=%v", err)
	}
}

func TestRepositoryRealRestoredLinksRejected(t *testing.T) {
	repo := integrationRepository(t)
	ctx := context.Background()
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	id, err := repo.Backup(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "unsafe")
	if err := repo.Restore(ctx, id, dest); err == nil {
		t.Fatal("restored symlink accepted")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe restored state retained: %v", err)
	}
}

func TestRepositoryRealRcloneLocalRetention(t *testing.T) {
	repo := integrationRepository(t)
	rclone := os.Getenv("GATEWAY_TEST_RCLONE")
	if rclone == "" {
		t.Skip("set GATEWAY_TEST_RCLONE for the real rclone-local integration")
	}
	ctx := context.Background()
	root := t.TempDir()
	config := filepath.Join(root, "rclone.conf")
	if err := os.WriteFile(config, []byte("[gateway]\ntype = local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo.Path = "rclone:gateway:" + filepath.Join(root, "remote-repository")
	repo.Env = []string{"RCLONE_CONFIG=" + config, "PATH=" + filepath.Dir(rclone) + ":" + os.Getenv("PATH")}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := repo.ID(ctx)
	if err != nil || !objectID(id) {
		t.Fatalf("repository ID=%q err=%v", id, err)
	}
	capture := t.TempDir()
	file := filepath.Join(capture, "config.json")
	if err := os.WriteFile(file, []byte("first generation"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Backup(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("second generation"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Backup(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("changed capture reused snapshot ID")
	}
	if err := repo.Forget(ctx, nil); err == nil {
		t.Fatal("retention allowed zero copies")
	}
	if err := repo.Forget(ctx, []string{strings.Repeat("0", 64)}); err == nil {
		t.Fatal("missing retention keep set accepted")
	}
	if err := repo.Forget(ctx, []string{second}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckMetadata(ctx); err != nil {
		t.Fatal("metadata check", err)
	}
	if err := repo.CheckSubset(ctx, 1, 2); err != nil {
		t.Fatal("subset check", err)
	}
	if err := repo.CheckSubset(ctx, 0, 2); err == nil {
		t.Fatal("invalid subset accepted")
	}
	if err := repo.CheckSubset(ctx, 1, 366); err == nil {
		t.Fatal("unbounded subset accepted")
	}
	if err := repo.Check(ctx); err != nil {
		t.Fatal(err)
	}
	snapshots, err := repo.Snapshots(ctx)
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != second {
		t.Fatalf("retained snapshots=%v err=%v", snapshots, err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := repo.Restore(ctx, second, restored); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(restored, "config.json"))
	if err != nil || string(body) != "second generation" {
		t.Fatalf("remote restore body=%q err=%v", body, err)
	}
}
