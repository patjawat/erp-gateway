package bootstrap

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type slowPairReader struct {
	entered, release chan struct{}
	started          bool
	body             io.Reader
}

func (r *slowPairReader) Read(b []byte) (int, error) {
	if !r.started {
		r.started = true
		close(r.entered)
		<-r.release
	}
	return r.body.Read(b)
}
func TestSlowUnauthenticatedPairDoesNotHoldTransferLock(t *testing.T) {
	s := controlBackupFixture(t)
	w := httptest.NewRecorder()
	s.migrationHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")), "receive")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() { close(release); <-done }()
	reader := &slowPairReader{entered: entered, release: release, body: strings.NewReader("{}")}
	request := httptest.NewRequest(http.MethodPost, "/backup/transfer/pair", reader)
	request.Header.Set("Content-Type", "application/json")
	go func() { s.transportHTTP(httptest.NewRecorder(), request); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("pair reader did not start")
	}
	if !s.transferMu.TryLock() {
		t.Fatal("unauthenticated slow body monopolized migration controls")
	}
	s.transferMu.Unlock()
}
