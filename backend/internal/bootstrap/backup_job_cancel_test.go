package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func cancelJobFixture(t *testing.T) (*backupService, backupRecord) {
	t.Helper()
	s, record := cloudPolicyFixture(t)
	if _, err := s.db.Exec(`CREATE TABLE jobs(id TEXT PRIMARY KEY,body TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.m = &Manager{fenced: true}
	t.Cleanup(func() { s.cancel(); s.wg.Wait() })
	return s, record
}

func cancelJobRequest(s *backupService, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/api/backups/jobs/"+id+"/cancel", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	s.cancelJobHTTP(w, r, id)
	return w
}

func finishedCancelJob(t *testing.T, s *backupService, id string) backupJob {
	t.Helper()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled job did not stop")
	}
	var body string
	if err := s.db.QueryRow("SELECT body FROM jobs WHERE id=?", id).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var job backupJob
	if json.Unmarshal([]byte(body), &job) != nil {
		t.Fatal("invalid persisted job")
	}
	return job
}

func TestCancelRunningBackupJobPreservesReadyCopyAndSourceFence(t *testing.T) {
	s, record := cancelJobFixture(t)
	path := filepath.Join(s.recordPath(record.ID), "gateway-backup.bundle")
	if err := os.WriteFile(path, []byte("verified encrypted fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	job, err := s.startJob("uploading", func(ctx context.Context, job *backupJob) error {
		s.publishBackup(record.ID) // Deliberately published independently of local job.
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	if w := cancelJobRequest(s, job.ID); w.Code != 200 {
		t.Fatalf("cancel rejected: %d", w.Code)
	}
	final := finishedCancelJob(t, s, job.ID)
	if final.State != "cancelled" || final.Phase != "cancelled" || final.BackupID != record.ID {
		t.Fatalf("cancelled state lost ready backup: %+v", final)
	}
	if copy, err := s.getRecord(record.ID); err != nil || !copy.LocalAvailable {
		t.Fatal("cancelled upload invalidated ready local copy")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("ready local bundle deleted")
	}
	if !s.m.fenced {
		t.Fatal("job cancellation resumed a migration source")
	}
}

func TestCancelCompletedJobCannotCancelNewJob(t *testing.T) {
	s, _ := cancelJobFixture(t)
	old, err := s.startJob("verifying", func(context.Context, *backupJob) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if job := finishedCancelJob(t, s, old.ID); job.State != "completed" {
		t.Fatal("first job did not complete")
	}
	started := make(chan context.Context, 1)
	current, err := s.startJob("uploading", func(ctx context.Context, _ *backupJob) error { started <- ctx; <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	currentContext := <-started
	if w := cancelJobRequest(s, old.ID); w.Code != 409 {
		t.Fatal("completed job accepted late cancellation")
	}
	if w := cancelJobRequest(s, backupID()); w.Code != 409 {
		t.Fatal("unrelated job ID accepted cancellation")
	}
	select {
	case <-currentContext.Done():
		t.Fatal("old cancellation affected new job")
	default:
	}
	if w := cancelJobRequest(s, current.ID); w.Code != 200 {
		t.Fatal("current job cancellation rejected")
	}
	if job := finishedCancelJob(t, s, current.ID); job.State != "cancelled" {
		t.Fatal("new job did not cancel")
	}
}

func TestLateJobCancellationDoesNotUndoCompletedActivation(t *testing.T) {
	s, _ := cancelJobFixture(t)
	committed := make(chan struct{})
	job, err := s.startJob("activating", func(ctx context.Context, _ *backupJob) error { close(committed); <-ctx.Done(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-committed
	if w := cancelJobRequest(s, job.ID); w.Code != 200 {
		t.Fatal("cancellation request rejected")
	}
	if final := finishedCancelJob(t, s, job.ID); final.State != "completed" || final.Phase != "completed" {
		t.Fatal("late cancellation relabeled durable activation as undone")
	}
}

func TestCancelQueuedJobPreventsItsCallbackFromStarting(t *testing.T) {
	s, _ := cancelJobFixture(t)
	s.operation.Lock()
	var calls atomic.Int32
	job, err := s.startJob("capturing", func(context.Context, *backupJob) error { calls.Add(1); return nil })
	if err != nil {
		s.operation.Unlock()
		t.Fatal(err)
	}
	w := cancelJobRequest(s, job.ID)
	s.operation.Unlock()
	if w.Code != 200 {
		t.Fatal("queued job cancellation rejected")
	}
	if final := finishedCancelJob(t, s, job.ID); final.State != "cancelled" || calls.Load() != 0 {
		t.Fatal("cancelled queued job still started work")
	}
}

func TestBackupJobParentShutdownCancelsCurrentJob(t *testing.T) {
	s, _ := cancelJobFixture(t)
	started := make(chan struct{})
	job, err := s.startJob("capturing", func(ctx context.Context, _ *backupJob) error { close(started); <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	s.cancel()
	if final := finishedCancelJob(t, s, job.ID); final.State != "cancelled" {
		t.Fatal("service shutdown did not propagate to job")
	}
}
