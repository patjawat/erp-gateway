package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPADeepLinksAndCSPNonce(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(`<base href="/admin/"><app-root ngCspNonce="__GATEWAY_CSP_NONCE__"></app-root><style>p{color:red}</style>`)}, "main-HASH.js": {Data: []byte("compiled app")}}
	handler := spaHandler(root)
	first := ""
	for _, url := range []string{"/", "/hospitals", "/hospitals/123"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<app-root") || strings.Contains(w.Body.String(), "__GATEWAY_CSP_NONCE__") {
			t.Fatalf("SPA deep link %s: %d %s", url, w.Code, w.Body.String())
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "style-src 'self' 'nonce-") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(w.Body.String(), "<style nonce=") {
			t.Fatalf("missing nonce policy: %s %s", csp, w.Body.String())
		}
		if first != "" && first == csp {
			t.Fatal("nonce repeated across documents")
		}
		first = csp
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/settings", nil))
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD must return HTML headers without a body")
	}
}

func TestSPAAssetsAndFailuresDoNotFallBackToLegacy(t *testing.T) {
	handler := spaHandler(fstest.MapFS{"index.html": {Data: []byte("angular app")}, "main-HASH.js": {Data: []byte("compiled app")}})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/main-HASH.js", 200},
		{http.MethodGet, "/missing.js", 404},
		{http.MethodGet, "/fonts/missing.ttf", 404},
		{http.MethodGet, "/api/hospitals", 404},
		{http.MethodGet, "/../index.html", 404},
		{http.MethodPost, "/hospitals", 405},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		if w.Code != test.status {
			t.Errorf("%s %s: got %d want %d", test.method, test.path, w.Code, test.status)
		}
	}
	w := httptest.NewRecorder()
	spaHandler(fstest.MapFS{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "has not been built") {
		t.Fatal("unbuilt Angular must return an explicit unavailable response")
	}
}

func TestLegacySetupAssetsRemainAvailable(t *testing.T) {
	w := httptest.NewRecorder()
	LegacyHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="setup-view"`) {
		t.Fatal("first-run setup assets must remain available")
	}
}
