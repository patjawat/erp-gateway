package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func controlTestDir(t *testing.T) string {
	t.Helper()
	// Keep the socket below the macOS sockaddr_un path limit, including test names.
	dir, err := os.MkdirTemp("/tmp", "gwctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestAdminControlPrivateSocketRoundTripAndCleanup(t *testing.T) {
	dir := controlTestDir(t)
	var calls atomic.Int32
	const password = "test-only-new-admin-password"
	control, err := StartAdminControl(dir, func(ctx context.Context, got string) (string, error) {
		calls.Add(1)
		if got != password || ctx.Err() != nil {
			t.Error("incorrect private reset request")
		}
		return "original-admin", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	for path, mode := range map[string]os.FileMode{filepath.Join(dir, ".admin"): 0700, filepath.Join(dir, ".admin", "sock"): 0600, filepath.Join(dir, ".admin", "lock"): 0600} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("incorrect permissions on %s: %v", filepath.Base(path), err)
		}
	}
	user, err := RequestAdminPasswordReset(context.Background(), dir, password)
	if err != nil || user != "original-admin" || calls.Load() != 1 {
		t.Fatal("local reset failed", user, err, calls.Load())
	}
	if another, err := StartAdminControl(dir, func(context.Context, string) (string, error) { return "", nil }); err == nil {
		_ = another.Close()
		t.Fatal("another listener replaced the live control socket")
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".admin", "sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("control left its socket behind", err)
	}
	if err := control.Close(); err != nil {
		t.Fatal("close must be idempotent", err)
	}
	if _, err := RequestAdminPasswordReset(context.Background(), dir, password); err == nil || strings.Contains(err.Error(), password) {
		t.Fatal("stopped listener did not give a safe error")
	}
}

func TestAdminControlRejectsUnsafePathsAndPreservesFiles(t *testing.T) {
	for _, test := range []string{"directory-symlink", "directory-public", "socket-file", "socket-symlink", "lock-symlink", "lock-hardlink"} {
		t.Run(test, func(t *testing.T) {
			dir := controlTestDir(t)
			private := filepath.Join(dir, ".admin")
			target := filepath.Join(dir, "untouched")
			if err := os.WriteFile(target, []byte("preserve this file"), 0600); err != nil {
				t.Fatal(err)
			}
			if test == "directory-symlink" {
				if err := os.Symlink(dir, private); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(private, 0700); err != nil {
					t.Fatal(err)
				}
				switch test {
				case "directory-public":
					if err := os.Chmod(private, 0755); err != nil {
						t.Fatal(err)
					}
				case "socket-file":
					if err := os.WriteFile(filepath.Join(private, "sock"), []byte("not a socket"), 0600); err != nil {
						t.Fatal(err)
					}
				case "socket-symlink":
					if err := os.Symlink(target, filepath.Join(private, "sock")); err != nil {
						t.Fatal(err)
					}
				case "lock-symlink":
					if err := os.Symlink(target, filepath.Join(private, "lock")); err != nil {
						t.Fatal(err)
					}
				case "lock-hardlink":
					if err := os.Link(target, filepath.Join(private, "lock")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if control, err := StartAdminControl(dir, func(context.Context, string) (string, error) { return "", nil }); err == nil {
				_ = control.Close()
				t.Fatal("unsafe path accepted")
			}
			if body, _ := os.ReadFile(target); string(body) != "preserve this file" {
				t.Fatal("unrelated file was modified")
			}
		})
	}
}

func TestAdminControlRejectsBadRequestsAndSanitizesErrors(t *testing.T) {
	dir := controlTestDir(t)
	var calls atomic.Int32
	password := "test-only-sensitive-password"
	control, err := StartAdminControl(dir, func(context.Context, string) (string, error) {
		calls.Add(1)
		return "", errors.New(password)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, ".admin", "sock"))
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, test := range []struct{ method, path, content, body string }{
		{"GET", "/password", "application/json", ""},
		{"POST", "/other", "application/json", `{}`},
		{"POST", "/password", "text/plain", password},
		{"POST", "/password", "application/json", `{`},
		{"POST", "/password", "application/json", `{"password":"short"}`},
		{"POST", "/password", "application/json", `{"password":"` + strings.Repeat("a", 4097) + `"}`},
		{"POST", "/password", "application/json", `{"password":"` + password + `","unexpected":true}`},
		{"POST", "/password", "application/json", `{"password":"` + password + `"} {}`},
		{"POST", "/password", "application/json", strings.Repeat(" ", 32<<10) + `{}`},
	} {
		request, _ := http.NewRequest(test.method, "http://unix"+test.path, bytes.NewBufferString(test.body))
		request.Header.Set("Content-Type", test.content)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode < 400 || strings.Contains(string(body), password) {
			t.Fatal("bad request was accepted or disclosed input", response.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached password reset callback")
	}
	if _, err := RequestAdminPasswordReset(context.Background(), dir, password); err == nil || strings.Contains(err.Error(), password) {
		t.Fatal("callback failure disclosed password or succeeded")
	}
	if calls.Load() != 1 {
		t.Fatal("valid request did not reach callback")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RequestAdminPasswordReset(cancelled, dir, password); err == nil {
		t.Fatal("cancelled client succeeded")
	}
}

func TestAdminControlReclaimsOnlyStaleSocket(t *testing.T) {
	dir := controlTestDir(t)
	private := filepath.Join(dir, ".admin")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(private, "sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	control, err := StartAdminControl(dir, func(context.Context, string) (string, error) { return "admin", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if user, err := RequestAdminPasswordReset(context.Background(), dir, "test-only-new-password"); err != nil || user != "admin" {
		t.Fatal(user, err)
	}
}

func TestAdminControlPreservesUnmanagedLiveSocket(t *testing.T) {
	dir := controlTestDir(t)
	private := filepath.Join(dir, ".admin")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(private, "sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	before, _ := os.Lstat(socket)
	if control, err := StartAdminControl(dir, func(context.Context, string) (string, error) { return "admin", nil }); err == nil {
		control.Close()
		t.Fatal("active socket without this implementation's lock was replaced")
	}
	after, err := os.Lstat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("live socket changed", err)
	}
}

func TestAdminControlCommittedWarningRemainsSuccess(t *testing.T) {
	dir := controlTestDir(t)
	control, err := StartAdminControl(dir, func(context.Context, string) (string, error) { return "admin", &PasswordResetDurabilityWarning{} })
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	username, err := RequestAdminPasswordReset(context.Background(), dir, "test-only-new-password")
	var warning *PasswordResetDurabilityWarning
	if username != "admin" || !errors.As(err, &warning) {
		t.Fatal("committed change became failure", username, err)
	}
}

func TestAdminControlClientRejectsBadOrUnboundedResponse(t *testing.T) {
	const password = "test-only-sensitive-password"
	for _, body := range []string{
		password,
		strings.Repeat(password, 400),
		`{"data":{"username":""}}`,
		`{"data":{"username":"` + strings.Repeat("x", 101) + `"}}`,
	} {
		dir := controlTestDir(t)
		if err := os.Mkdir(filepath.Join(dir, ".admin"), 0700); err != nil {
			t.Fatal(err)
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, ".admin", "sock"), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
		})}
		go server.Serve(listener)
		_, err = RequestAdminPasswordReset(context.Background(), dir, password)
		server.Close()
		if err == nil || strings.Contains(err.Error(), password) {
			t.Fatal("unsafe control response accepted or disclosed", err)
		}
	}
}
