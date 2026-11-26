package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryCancellationCleansRealNativeLock(t *testing.T) {
	repository := integrationRepository(t)
	producer, err := exec.LookPath("yes")
	if err != nil {
		t.Skip("yes is required to hold a real streaming backup open")
	}
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan struct{})
	var backupErr error
	go func() {
		defer close(completed)
		_, backupErr = repository.run(ctx, "", "backup", "--stdin-from-command", "--stdin-filename", "held-input", producer)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-completed:
		case <-time.After(8 * time.Second):
			t.Error("cancelled backup process did not terminate")
		}
		// Test fixture only: this process exclusively owns its temporary repo.
		_, _ = repository.run(context.Background(), "", "unlock", "--remove-all")
	})
	deadline := time.Now().Add(8 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		locks, err := os.ReadDir(filepath.Join(repository.Path, "locks"))
		if err == nil && len(committedNativeLocks(locks)) > 0 {
			observed = true
			break
		}
		select {
		case <-completed:
			t.Fatalf("streaming backup ended before native lock: %v", backupErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("did not observe a real native backup lock")
	}
	started := time.Now()
	cancel()
	select {
	case <-completed:
	case <-time.After(8 * time.Second):
		t.Fatal("cancellation exceeded bounded graceful shutdown")
	}
	if !errors.Is(backupErr, context.Canceled) {
		t.Fatalf("cancelled backup err=%v", backupErr)
	}
	if time.Since(started) > 8*time.Second {
		t.Fatal("cancelled subprocess did not respect shutdown bound")
	}
	locks, err := os.ReadDir(filepath.Join(repository.Path, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	if native := committedNativeLocks(locks); len(native) != 0 {
		t.Fatalf("normal cancellation stranded native locks %v", native)
	}
}

func TestRepositoryControlledLocalLockRecovery(t *testing.T) {
	repository := integrationRepository(t)
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture")
	if err := os.Mkdir(capture, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capture, "verified.txt"), []byte("immutable verified fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Backup(context.Background(), capture)
	if err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	command := exec.Command(repository.Binary, "--no-cache", "--json", "--repo", repository.Path, "--password-file", repository.PasswordFile, "backup", "--stdin", "--stdin-filename", "crashed-input")
	command.Stdin = read
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_, _ = repository.run(context.Background(), "", "unlock", "--remove-all")
	})
	observed := false
	var lockID string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		locks, err := os.ReadDir(filepath.Join(repository.Path, "locks"))
		if native := committedNativeLocks(locks); err == nil && len(native) > 0 {
			observed = true
			lockID = native[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("no native lock observed")
	}
	// Local backend atomic writes first create <id>-tmp-<random>. Killing at
	// that moment only strands an unregistered temporary file, which native
	// unlock deliberately does not enumerate. Authenticate the committed lock
	// and its actual child PID before reproducing a process crash.
	lockBody, err := repository.runRead(context.Background(), "cat", "lock", lockID)
	if err != nil {
		t.Fatal("committed native lock could not be authenticated", err)
	}
	var owner struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(lockBody, &owner); err != nil || owner.PID != command.Process.Pid {
		t.Fatalf("committed lock owner pid=%d expected=%d err=%v", owner.PID, command.Process.Pid, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	waited = true
	locks, err := os.ReadDir(filepath.Join(repository.Path, "locks"))
	if err != nil || len(committedNativeLocks(locks)) == 0 {
		t.Fatal("SIGKILL fixture did not leave a native lock", err)
	}
	staged := filepath.Join(t.TempDir(), "cold-restore")
	if err := repository.Restore(context.Background(), snapshot, staged); err != nil {
		t.Fatal("exact immutable restore depended on a stale native lock", err)
	}
	if body, err := os.ReadFile(filepath.Join(staged, "verified.txt")); err != nil || string(body) != "immutable verified fixture" {
		t.Fatal("cold restore lost verified data", err)
	}
	locks, err = os.ReadDir(filepath.Join(repository.Path, "locks"))
	if err != nil || len(committedNativeLocks(locks)) == 0 {
		t.Fatal("read-only restore removed a repository lock", err)
	}
	// This test process exclusively owns the private fixture and the child has
	// terminated. No shared repo or running peer is ever force-unlocked.
	if err := repository.RecoverLocalLocks(context.Background()); err != nil {
		t.Fatal(err)
	}
	locks, err = os.ReadDir(filepath.Join(repository.Path, "locks"))
	if err != nil || len(committedNativeLocks(locks)) != 0 {
		var remaining []string
		for _, entry := range locks {
			info, _ := entry.Info()
			size := int64(-1)
			if info != nil {
				size = info.Size()
			}
			remaining = append(remaining, entry.Name()+" size="+fmt.Sprint(size))
		}
		listed, listErr := repository.runCommand(context.Background(), "", true, "list", "locks")
		t.Fatalf("private lock recovery failed: %d, %v; entries=%v native_list=%q list_err=%v", len(locks), err, remaining, string(listed), listErr)
	}
	if err := repository.CheckMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func committedNativeLocks(entries []os.DirEntry) []string {
	var result []string
	for _, entry := range entries {
		if !entry.IsDir() && objectID(entry.Name()) {
			result = append(result, entry.Name())
		}
	}
	return result
}

func TestLockRecoveryNeverUnlocksSharedOrPublicDirectories(t *testing.T) {
	for _, path := range []string{"rclone:gateway:repository", "s3:https://bucket.example/repository", "https://backup.example/repository"} {
		if err := (Repository{Path: path}).RecoverLocalLocks(context.Background()); err == nil {
			t.Fatal("shared lock recovery accepted", path)
		}
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := (Repository{Path: dir}).RecoverLocalLocks(context.Background()); err == nil {
		t.Fatal("public repository lock recovery accepted")
	}
	if _, err := (Repository{}).runRead(context.Background(), "prune"); err == nil {
		t.Fatal("no-lock mutation accepted")
	}
}
