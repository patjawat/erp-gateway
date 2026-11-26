package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/bootstrap"
)

func TestSetupPasswordPromptsConfirmAndNeverPrintSecret(t *testing.T) {
	for _, test := range []struct{ name, password string }{
		{"single-character", "1"},
		{"preserves-spaces", "  test-only-admin-password  "},
	} {
		t.Run(test.name, func(t *testing.T) {
			password := test.password
			var prompts []string
			var output bytes.Buffer
			calls := 0
			err := setupPasswordWithPrompts(context.Background(), func(prompt string) ([]byte, error) {
				prompts = append(prompts, prompt)
				return []byte(password), nil
			}, func(ctx context.Context, got string) (string, error) {
				calls++
				if ctx.Err() != nil || got != password {
					t.Error("password was trimmed or incorrect")
				}
				return "original-admin", nil
			}, &output)
			if err != nil || calls != 1 || len(prompts) != 2 || prompts[0] != "New Admin password: " || prompts[1] != "Confirm new Admin password: " {
				t.Fatal("interactive reset failed", err, calls, prompts)
			}
			if strings.Contains(output.String(), password) || !strings.Contains(output.String(), "original-admin") {
				t.Fatal("success output disclosed password or omitted username")
			}
		})
	}
}

func TestSetupPasswordStopsBeforeResetOnInvalidOrCancelledInput(t *testing.T) {
	for _, test := range []struct {
		name, first, second string
		inputErr            error
		cancelled           bool
	}{
		{name: "empty", first: ""},
		{name: "too-long", first: strings.Repeat("x", 4097)},
		{name: "confirmation-mismatch", first: "test-only-new-password", second: "different-new-password"},
		{name: "EOF", inputErr: io.EOF},
		{name: "cancelled", first: "test-only-new-password", second: "test-only-new-password", cancelled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancelled {
				cancel()
			}
			reads, resets := 0, 0
			var output bytes.Buffer
			err := setupPasswordWithPrompts(ctx, func(string) ([]byte, error) {
				reads++
				if test.inputErr != nil {
					return nil, test.inputErr
				}
				if reads == 1 {
					return []byte(test.first), nil
				}
				return []byte(test.second), nil
			}, func(context.Context, string) (string, error) { resets++; return "admin", nil }, &output)
			if err == nil || resets != 0 || output.Len() != 0 {
				t.Fatal("failed input reset account or emitted success", err, resets)
			}
		})
	}
}

func TestSetupPasswordCommittedWarningIsSuccess(t *testing.T) {
	var output bytes.Buffer
	err := setupPasswordWithPrompts(context.Background(), func(string) ([]byte, error) { return []byte("test-only-new-password"), nil }, func(context.Context, string) (string, error) {
		return "admin", &bootstrap.PasswordResetDurabilityWarning{}
	}, &output)
	if err != nil || !strings.Contains(output.String(), "admin") || !strings.Contains(output.String(), "คำเตือน") {
		t.Fatal("committed change was reported as failure", err)
	}
}

func TestSetupPasswordReportsSafeCallbackFailure(t *testing.T) {
	var output bytes.Buffer
	err := setupPasswordWithPrompts(context.Background(), func(string) ([]byte, error) { return []byte("test-only-new-password"), nil }, func(context.Context, string) (string, error) {
		return "", errors.New("safe local control error")
	}, &output)
	if err == nil || output.Len() != 0 {
		t.Fatal("failure emitted success")
	}
}

func TestSetupPasswordRejectsNonTerminalWithUsefulInstruction(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input-")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var prompts, output bytes.Buffer
	err = runSetupPassword(context.Background(), t.TempDir(), input, &prompts, &output)
	if err == nil || !strings.Contains(err.Error(), "ไม่ใช้ -T") || prompts.Len() != 0 || output.Len() != 0 {
		t.Fatal("non-terminal did not fail before consuming credentials", err)
	}
}

func TestGatewayMainContinuesWhenLocalControlIsUnavailable(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, ".admin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(config, ".admin"), 0755); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	reservation.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestGatewayMainHelperProcess$")
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GATEWAY_") || strings.HasPrefix(entry, "FCM_") || strings.HasPrefix(entry, "SETUP=") || strings.HasPrefix(entry, "LISTEN_ADDR=") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "GO_WANT_GATEWAY_MAIN=1", "SETUP=true", "LISTEN_ADDR="+address, "GATEWAY_CONFIG_DIR="+config, "GATEWAY_DATABASE_PATH="+filepath.Join(root, "gateway.db"))
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/setup/api/status")
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == 200 && strings.Contains(string(body), `"state":"setup"`) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("public Gateway did not start after private control failure")
}

func TestGatewayMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GATEWAY_MAIN") != "1" {
		return
	}
	os.Args = []string{"gateway"}
	main()
	os.Exit(0)
}
