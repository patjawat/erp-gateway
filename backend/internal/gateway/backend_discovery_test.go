package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
)

type directoryFixture struct {
	mu            sync.Mutex
	code          string
	key           string
	marker        any
	markerPresent bool
	statusCode    int
	reportedCode  string
	version       string
	reachable     bool
	statusCalls   int
	goCalls       int
	erpCalls      int
	lastQuery     url.Values
	statusEntered chan struct{}
	statusRelease chan struct{}
}

func (f *directoryFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	key, marker, present := f.key, f.marker, f.markerPresent
	statusCode, reported, version, reachable := f.statusCode, f.reportedCode, f.version, f.reachable
	entered, release := f.statusEntered, f.statusRelease
	if r.URL.Path == "/api/status" {
		f.statusCalls++
	} else if r.URL.Path == "/depdrop/employee-by-id" {
		f.goCalls++
		f.lastQuery = r.URL.Query()
	} else if r.URL.Path == "/api/personnel" {
		f.erpCalls++
		f.lastQuery = r.URL.Query()
	}
	f.mu.Unlock()
	if r.Header.Get("X-Gateway-Key") != key || r.Header.Get("Cookie") != "" {
		t.Errorf("wrong installation credentials for %s", f.code)
		fail(w, 401, "GATEWAY_UNAUTHORIZED", "fixture")
		return
	}
	if r.URL.Path == "/api/status" {
		if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("discovery forwarded a bearer token or caller-controlled query")
		}
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		if release != nil {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if statusCode != 200 {
			fail(w, statusCode, "FIXTURE_ERROR", "private installation detail")
			return
		}
		value := map[string]any{"hospital_code": reported, "api_version": version, "erp_reachable": reachable}
		if present {
			value["backend"] = marker
		}
		writeData(w, value)
		return
	}
	if r.Header.Get("Authorization") != "Bearer upstream-token-"+f.code {
		t.Errorf("wrong user bearer for %s", f.code)
		fail(w, 401, "SESSION_EXPIRED", "fixture")
		return
	}
	if r.URL.Path != "/depdrop/employee-by-id" && r.URL.Path != "/api/personnel" {
		t.Errorf("discovery selected unexpected path: %s", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"results":[{"id":9,"fullname":"Staff %s","position_name":"Nurse"}],"total_count":25}`, f.code)
}

type directoryDiscoveryRig struct {
	app      *Server
	store    *Store
	handler  http.Handler
	sites    map[string]Hospital
	tokens   map[string]string
	fixtures map[string]*directoryFixture
}

func newDirectoryDiscoveryRig(t *testing.T) *directoryDiscoveryRig {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "directory.db"), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	r := &directoryDiscoveryRig{store: store, sites: map[string]Hospital{}, tokens: map[string]string{}, fixtures: map[string]*directoryFixture{}}
	approved := []string{}
	for _, code := range []string{"11447", "12448"} {
		key := strings.Repeat(code, 8)
		f := &directoryFixture{code: code, key: key, reportedCode: code, version: "1", reachable: true, statusCode: 200}
		if code == "12448" {
			f.markerPresent, f.marker = true, "erp_api_module"
		}
		r.fixtures[code] = f
		h := Hospital{Hospital: contracts.Hospital{Code: code, Name: "Hospital " + code}, APIURL: "https://" + code + ".example.invalid"}
		h, err = store.addHospital(context.Background(), h, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec("UPDATE hospitals SET approval_status='approved',enabled=1 WHERE id=?", h.ID); err != nil {
			t.Fatal(err)
		}
		h, err = store.hospital(context.Background(), "id", h.ID)
		if err != nil {
			t.Fatal(err)
		}
		token, err := store.createSession(context.Background(), h, contracts.Token{UserID: json.RawMessage("7"), AccessToken: "upstream-token-" + code, ExpiresAt: time.Now().Add(time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		r.sites[code], r.tokens[code] = h, token
		approved = append(approved, code+"="+h.APIURL)
	}
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		f := r.fixtures[code]
		if f == nil {
			return nil, errors.New("unexpected destination")
		}
		w := httptest.NewRecorder()
		f.serve(t, w, req)
		return w.Result(), nil
	})}
	r.app, err = New(Config{Store: store, AdminUsername: "admin", AdminPassword: "fixture", ApprovedUpstreams: approved, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	r.handler = r.app.Handler()
	return r
}

func (r *directoryDiscoveryRig) request(code, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "https://gateway.example.invalid"+path, nil)
	req.Header.Set("Authorization", "Bearer "+r.tokens[code])
	req.Header.Set("Cookie", "must-not-reach-upstream=1")
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}

func (r *directoryDiscoveryRig) renewSession(t *testing.T, code string) {
	t.Helper()
	h, err := r.store.hospital(context.Background(), "id", r.sites[code].ID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := r.store.createSession(context.Background(), h, contracts.Token{UserID: json.RawMessage("7"), AccessToken: "upstream-token-" + code, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	r.sites[code], r.tokens[code] = h, token
}

func TestDirectoryDiscoveryMixedBackendsPreservePublicContract(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	for _, code := range []string{"11447", "12448"} {
		for i := 0; i < 2; i++ {
			w := r.request(code, "/depdrop/employee-by-id?q=staff&page=2&exclude_emp_id=8&emp_id=999&backend=go&api_url=https://untrusted.invalid")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Staff "+code) || !strings.Contains(w.Body.String(), `"total_count":25`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), r.tokens[code]) || strings.Contains(w.Body.String(), "upstream-token") {
				t.Fatal("token leaked")
			}
		}
		f := r.fixtures[code]
		f.mu.Lock()
		if f.statusCalls != 1 {
			t.Errorf("%s discovery count=%d", code, f.statusCalls)
		}
		if (code == "11447" && (f.goCalls != 2 || f.erpCalls != 0)) || (code == "12448" && (f.goCalls != 0 || f.erpCalls != 2)) {
			t.Errorf("wrong backend: %+v", f.lastQuery)
		}
		wantQueries := 3
		if code == "12448" {
			// ERP owns business query validation. Gateway passes future selectors
			// unchanged; these values cannot choose an upstream or actor.
			wantQueries = 6
		}
		if len(f.lastQuery) != wantQueries || f.lastQuery.Get("q") != "staff" || f.lastQuery.Get("page") != "2" || f.lastQuery.Get("exclude_emp_id") != "8" {
			t.Errorf("query forwarding: %v", f.lastQuery)
		}
		f.mu.Unlock()
	}
}

func TestDirectoryDiscoveryRejectsUnknownOrMalformedMarkersWithoutFallback(t *testing.T) {
	for _, marker := range []any{nil, "", "go", "https://evil.invalid/path", true, 1, []string{"erp_api_module"}, map[string]string{"backend": "erp_api_module"}} {
		t.Run(fmt.Sprintf("%T_%v", marker, marker), func(t *testing.T) {
			r := newDirectoryDiscoveryRig(t)
			f := r.fixtures["12448"]
			f.marker = marker
			w := r.request("12448", "/depdrop/employee-by-id?page=1")
			if w.Code != 503 || !strings.Contains(w.Body.String(), "ERP_UNAVAILABLE") {
				t.Fatal(w.Code, w.Body.String())
			}
			if f.goCalls != 0 || f.erpCalls != 0 || strings.Contains(w.Body.String(), "private") {
				t.Fatal("failed discovery routed or leaked detail")
			}
			f.marker = "erp_api_module"
			if got := r.request("12448", "/depdrop/employee-by-id?page=1"); got.Code != 200 {
				t.Fatal(got.Code, got.Body.String())
			}
		})
	}
}

func TestDirectoryDiscoveryValidatesAuthenticatedStatusBeforeTrustingMarker(t *testing.T) {
	for _, kind := range []string{"wrong_hospital", "wrong_version", "unreachable", "unauthorized", "server_error"} {
		t.Run(kind, func(t *testing.T) {
			r := newDirectoryDiscoveryRig(t)
			f := r.fixtures["12448"]
			switch kind {
			case "wrong_hospital":
				f.reportedCode = "99999"
			case "wrong_version":
				f.version = "2"
			case "unreachable":
				f.reachable = false
			case "unauthorized":
				f.statusCode = 401
			case "server_error":
				f.statusCode = 500
			}
			w := r.request("12448", "/depdrop/employee-by-id")
			if w.Code != 503 || f.goCalls != 0 || f.erpCalls != 0 || strings.Contains(w.Body.String(), "private installation") {
				t.Fatal(w.Code, w.Body.String())
			}
			if session, err := r.store.session(context.Background(), r.tokens["12448"]); err != nil || session.revoked {
				t.Fatal("discovery failure lost session", err)
			}
		})
	}
}

func TestDirectoryDiscoveryDoesNotFallbackOnERPDirectoryFailure(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	prior := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/personnel" {
			w := httptest.NewRecorder()
			fail(w, 404, "NOT_FOUND", "fixture")
			return w.Result(), nil
		}
		return prior.RoundTrip(req)
	})
	w := r.request("12448", "/depdrop/employee-by-id")
	if w.Code != 404 || r.fixtures["12448"].goCalls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDirectoryDiscoveryCoalescesConcurrentRequestsAndCachesExpiry(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	f := r.fixtures["12448"]
	f.statusEntered, f.statusRelease = make(chan struct{}, 1), make(chan struct{})
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	r.app.directoryBackends.clock = func() time.Time { return time.Unix(0, now.Load()) }
	results := make(chan int, 16)
	for i := 0; i < cap(results); i++ {
		go func() { results <- r.request("12448", "/depdrop/employee-by-id").Code }()
	}
	select {
	case <-f.statusEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not start")
	}
	close(f.statusRelease)
	for i := 0; i < cap(results); i++ {
		if code := <-results; code != 200 {
			t.Fatal(code)
		}
	}
	f.mu.Lock()
	if f.statusCalls != 1 {
		t.Fatal("concurrent discovery count", f.statusCalls)
	}
	f.statusEntered, f.statusRelease = nil, nil
	f.mu.Unlock()
	now.Add(int64(directoryBackendTTL + time.Second))
	if w := r.request("12448", "/depdrop/employee-by-id"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusCalls != 2 {
		t.Fatal("cache did not expire", f.statusCalls)
	}
}

func TestDirectoryDiscoveryKeyRotationInvalidatesCachedBackend(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	if w := r.request("11447", "/depdrop/employee-by-id"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f := r.fixtures["11447"]
	f.key, f.markerPresent, f.marker = strings.Repeat("new-key", 8), true, "erp_api_module"
	h := r.sites["11447"]
	key, err := r.store.encrypt("hospital:"+h.ID, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.store.db.Exec("UPDATE hospitals SET gateway_key=? WHERE id=?", key, h.ID); err != nil {
		t.Fatal(err)
	}
	r.renewSession(t, "11447")
	w := r.request("11447", "/depdrop/employee-by-id")
	if w.Code != 200 || f.statusCalls != 2 || f.goCalls != 1 || f.erpCalls != 1 {
		t.Fatal(w.Code, f.statusCalls, f.goCalls, f.erpCalls)
	}
}

func TestDirectoryDiscoveryURLChangeDoesNotReusePreviousBackend(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	if w := r.request("11447", "/depdrop/employee-by-id"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f := r.fixtures["11447"]
	f.markerPresent, f.marker = true, "erp_api_module"
	prior := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "replacement.example.invalid" {
			return prior.RoundTrip(req)
		}
		w := httptest.NewRecorder()
		f.serve(t, w, req)
		return w.Result(), nil
	})
	policy, err := newUpstreamPolicy([]string{"11447=https://replacement.example.invalid", "12448=https://12448.example.invalid"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	r.app.egress = policy
	r.app.managementRevision = managementFingerprint(policy, nil)
	if _, err = r.store.db.Exec("UPDATE hospitals SET api_url=? WHERE id=?", "https://replacement.example.invalid", r.sites["11447"].ID); err != nil {
		t.Fatal(err)
	}
	r.renewSession(t, "11447")
	w := r.request("11447", "/depdrop/employee-by-id")
	if w.Code != 200 || f.statusCalls != 2 || f.goCalls != 1 || f.erpCalls != 1 {
		t.Fatal(w.Code, f.statusCalls, f.goCalls, f.erpCalls)
	}
}

func TestDirectoryDiscoveryRejectsMalformedStatusAndResponseBounds(t *testing.T) {
	for _, kind := range []string{"html", "malformed", "array", "typed_boolean", "oversized", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			r := newDirectoryDiscoveryRig(t)
			prior := r.app.client.Transport
			r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/api/status" {
					return prior.RoundTrip(req)
				}
				w := httptest.NewRecorder()
				w.Header().Set("Content-Type", "application/json")
				body := `{"data":{"hospital_code":"12448","api_version":"1","erp_reachable":true,"backend":"erp_api_module"}}`
				switch kind {
				case "html":
					w.Header().Set("Content-Type", "text/html")
				case "malformed":
					body = `{"data":`
				case "array":
					body = `{"data":[]}`
				case "typed_boolean":
					body = strings.Replace(body, `"erp_reachable":true`, `"erp_reachable":"true"`, 1)
				case "oversized":
					body = strings.TrimSuffix(body, "}}") + `,"padding":"` + strings.Repeat("x", 8192) + `"}}`
				case "redirect":
					w.Header().Set("Location", "https://untrusted.invalid/")
					w.WriteHeader(http.StatusFound)
				}
				_, _ = w.Write([]byte(body))
				return w.Result(), nil
			})
			w := r.request("12448", "/depdrop/employee-by-id")
			if w.Code != 503 || r.fixtures["12448"].goCalls != 0 || r.fixtures["12448"].erpCalls != 0 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestDirectoryBackendFingerprintIncludesSiteAndConfiguration(t *testing.T) {
	h := Hospital{Hospital: contracts.Hospital{ID: "a", Code: "11447", Name: "A", Province: "P"}, APIURL: "https://a.invalid", key: []byte("ciphertext")}
	first := directoryBackendFingerprint(h, "policy-a")
	variants := []Hospital{h, h, h, h, h, h}
	variants[0].ID = "b"
	variants[1].Code = "12448"
	variants[2].APIURL = "https://b.invalid"
	variants[3].key = []byte("new-ciphertext")
	variants[4].Name = "B"
	variants[5].Province = "Q"
	for _, v := range variants {
		if directoryBackendFingerprint(v, "policy-a") == first {
			t.Fatal("configuration collision")
		}
	}
	if directoryBackendFingerprint(h, "policy-b") == first {
		t.Fatal("policy revision not scoped")
	}
}

func TestDirectoryBackendCacheCancellationFailuresAndBounds(t *testing.T) {
	var cache directoryBackendCache
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.resolve(context.Background(), "same", func(context.Context) (string, error) { close(entered); <-release; return "/api/personnel", nil })
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.resolve(ctx, "same", func(context.Context) (string, error) { t.Error("waiter fetched"); return "", nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < directoryBackendCacheLimit+10; i++ {
		if _, err := cache.resolve(context.Background(), fmt.Sprintf("site-%d", i), func(context.Context) (string, error) { return "/depdrop/employee-by-id", nil }); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	if len(cache.entries) > directoryBackendCacheLimit {
		t.Fatal("unbounded cache", len(cache.entries))
	}
	cache.mu.Unlock()
	calls := 0
	for i := 0; i < 2; i++ {
		_, _ = cache.resolve(context.Background(), "failed", func(context.Context) (string, error) { calls++; return "", errors.New("failed") })
	}
	if calls != 2 {
		t.Fatal("failed discovery was cached")
	}
}

func TestDirectoryBackendCacheBoundsConcurrentDiscoveries(t *testing.T) {
	var cache directoryBackendCache
	entered := make(chan struct{}, directoryBackendFlightLimit)
	release := make(chan struct{})
	done := make(chan error, directoryBackendFlightLimit)
	for i := 0; i < directoryBackendFlightLimit; i++ {
		go func(key string) {
			_, err := cache.resolve(context.Background(), key, func(context.Context) (string, error) {
				entered <- struct{}{}
				<-release
				return goDirectoryPath, nil
			})
			done <- err
		}(fmt.Sprintf("flight-%d", i))
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < directoryBackendFlightLimit; i++ {
		select {
		case <-entered:
		case <-timer.C:
			close(release)
			t.Fatal("discovery flights did not start")
		}
	}
	_, err := cache.resolve(context.Background(), "over-limit", func(context.Context) (string, error) {
		t.Error("discovery exceeded concurrent bound")
		return "", nil
	})
	if !errors.Is(err, errDirectoryBackend) {
		t.Error("over-limit discovery was accepted", err)
	}
	close(release)
	for i := 0; i < directoryBackendFlightLimit; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
