package bootstrap

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestCloseWaitsForPreviouslyRetiredRuntime(t *testing.T) {
	retiredClosing := make(chan struct{})
	releaseRetired := make(chan struct{})
	currentClosed := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRetired) }) }
	builds := 0
	m := managerForTest(t, func(RuntimeConfig, []byte) (Runtime, error) {
		builds++
		closeRuntime := func() { close(currentClosed) }
		if builds == 1 {
			closeRuntime = func() {
				close(retiredClosing)
				<-releaseRetired
			}
		}
		return Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Close: closeRuntime}, nil
	})
	t.Cleanup(release)
	m.options.ValidatePolicy = testSettingsPolicy
	cookie, csrf := setupSessionForTest(t, m)
	requireHTTPResult(t, request(t, m, "/setup/api/activate", testInput(t), cookie, csrf), 200)
	settings, err := m.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplySettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-retiredClosing:
	case <-time.After(5 * time.Second):
		t.Fatal("previous runtime did not start retiring")
	}
	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	select {
	case <-currentClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("current runtime did not close")
	}
	requireHTTPResult(t, request(t, m, "/setup/api/status", nil, nil, ""), 503)
	if err := m.ApplySettings(context.Background(), settings); err == nil {
		t.Fatal("closing manager accepted a new runtime installation")
	}
	select {
	case <-done:
		t.Fatal("manager returned while the previous runtime still held resources")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not finish after all runtimes closed")
	}
}
