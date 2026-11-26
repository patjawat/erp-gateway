// Package backup supplies streaming, encrypted repository and consistent capture
// primitives. Authorization, logical-file selection and runtime cutover belong
// to the bootstrap coordinator; these functions never open the live Store.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
)

const snapshotTimeout = 10 * time.Minute

// SnapshotSQLite captures committed pages, including WAL, using a separate
// read-only connection. The destination must not exist and is removed on error.
// It does not hold the application's operational connection while copying.
func SnapshotSQLite(ctx context.Context, source, destination string) (result error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := regularFile(source); err != nil {
		return fmt.Errorf("snapshot source: %w", err)
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if source == destination {
		return errors.New("snapshot source and destination must differ")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("snapshot destination must be a new private file")
	}
	if err = f.Close(); err != nil {
		os.Remove(destination)
		return err
	}
	defer func() {
		if result != nil {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				_ = os.Remove(destination + suffix)
			}
		}
	}()
	db, err := sql.Open("sqlite", sqliteURI(source, true))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.Raw(func(raw any) (captureErr error) {
		source, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite driver does not support consistent online backup")
		}
		capture, err := source.NewBackup(sqliteURI(destination, false))
		if err != nil {
			return err
		}
		defer func() { captureErr = errors.Join(captureErr, capture.Finish()) }()
		busySince := time.Time{}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := capture.Step(128)
			if err != nil {
				var sqliteErr *sqlite.Error
				if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != 5 && sqliteErr.Code()&0xff != 6) {
					return err
				}
				if busySince.IsZero() {
					busySince = time.Now()
				}
				if time.Since(busySince) >= 5*time.Second {
					return errors.New("SQLite snapshot remained busy; retry capture later")
				}
				if err := pause(ctx, 20*time.Millisecond); err != nil {
					return err
				}
				continue
			}
			busySince = time.Time{}
			if !more {
				return nil
			}
			if err := pause(ctx, time.Millisecond); err != nil {
				return err
			}
		}
	}); err != nil {
		return err
	}
	if err = VerifySQLite(ctx, destination); err != nil {
		return err
	}
	f, err = os.OpenFile(destination, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

// VerifySQLite performs read-only inspection. It never initializes an empty DB
// and never runs Gateway schema migration or changes a source encryption key.
func VerifySQLite(ctx context.Context, path string) error {
	if err := regularFile(path); err != nil {
		return err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqliteURI(path, true))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return errors.New("SQLite snapshot integrity inspection failed")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return err
		}
		if status != "ok" || count != 0 {
			return errors.New("SQLite snapshot failed integrity check")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("SQLite integrity check returned no result")
	}
	return nil
}

func sqliteURI(path string, readonly bool) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{"_pragma": []string{"busy_timeout(50)"}}
	if readonly {
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(1)")
	} else {
		query.Set("mode", "rw")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("expected a regular file, not a link or device")
	}
	return nil
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
