package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSnapshotSQLiteIncludesCommittedWALAndLeavesSourceUsable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE example (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO example(value) VALUES ('committed in WAL')"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "snapshot.db")
	if err := SnapshotSQLite(context.Background(), source, dest); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var value string
	if err := copyDB.QueryRow("SELECT value FROM example").Scan(&value); err != nil || value != "committed in WAL" {
		t.Fatalf("snapshot value=%q err=%v", value, err)
	}
	if _, err := db.Exec("INSERT INTO example(value) VALUES ('after capture')"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := copyDB.QueryRow("SELECT count(*) FROM example").Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot count=%d err=%v", count, err)
	}
	if err := VerifySQLite(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotSQLiteRefusesMissingOrExistingAndCancellation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "missing.db")
	dest := filepath.Join(dir, "snapshot.db")
	if err := SnapshotSQLite(context.Background(), source, dest); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source created: %v", err)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE example(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := os.WriteFile(dest, []byte("keep existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotSQLite(context.Background(), source, dest); err == nil {
		t.Fatal("existing destination accepted")
	}
	body, _ := os.ReadFile(dest)
	if string(body) != "keep existing" {
		t.Fatal("existing destination changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SnapshotSQLite(ctx, source, filepath.Join(dir, "cancelled.db")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation err=%v", err)
	}
}

func TestSnapshotSQLiteBusyIsBoundedByContext(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "busy.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE example(value TEXT); BEGIN EXCLUSIVE; INSERT INTO example VALUES ('uncommitted')"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ROLLBACK")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	dest := filepath.Join(dir, "cancelled.db")
	started := time.Now()
	if err := SnapshotSQLite(ctx, source, dest); err == nil {
		t.Fatal("busy snapshot unexpectedly succeeded")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("busy snapshot ignored cancellation")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed capture retained: %v", err)
	}
}

func TestSnapshotSQLiteConcurrentWritesStayTransactional(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "live.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE state(n INTEGER); INSERT INTO state VALUES(0); CREATE TABLE events(n INTEGER); CREATE TABLE padding(body TEXT); INSERT INTO padding SELECT printf('%0100000d',0) FROM json_each('[1,2,3,4,5,6,7,8,9,10]')"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var writes atomic.Int64
	writerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				writerDone <- nil
				return
			default:
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				writerDone <- err
				return
			}
			if _, err = tx.ExecContext(ctx, "UPDATE state SET n=n+1; INSERT INTO events SELECT n FROM state"); err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				writerDone <- err
				return
			}
			writes.Add(1)
			if err := pause(ctx, 10*time.Millisecond); err != nil {
				writerDone <- nil
				return
			}
		}
	}()
	for writes.Load() == 0 {
		if err := pause(ctx, time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	before := writes.Load()
	destination := filepath.Join(dir, "capture.db")
	if err := SnapshotSQLite(ctx, source, destination); err != nil {
		cancel()
		<-writerDone
		t.Fatal(err)
	}
	// Confirm the operational writer remains usable after the read-only helper
	// has finished, without needing the Store's sole connection for capture.
	for writes.Load() <= before {
		if err := pause(ctx, time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	if err := <-writerDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var state, events int
	if err := copyDB.QueryRow("SELECT n,(SELECT count(*) FROM events) FROM state").Scan(&state, &events); err != nil {
		t.Fatal(err)
	}
	if state < 1 || state != events {
		t.Fatalf("snapshot split a committed transaction: state=%d events=%d", state, events)
	}
}
