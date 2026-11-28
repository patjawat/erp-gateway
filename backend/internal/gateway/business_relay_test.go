package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func relayRig(t *testing.T, serve func(http.ResponseWriter, *http.Request)) *directoryDiscoveryRig {
	t.Helper()
	r := newDirectoryDiscoveryRig(t)
	prior := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/status" {
			return prior.RoundTrip(req)
		}
		if req.URL.Host != "12448.example.invalid" || req.Header.Get("Authorization") != "Bearer upstream-token-12448" || req.Header.Get("X-Gateway-Key") != strings.Repeat("12448", 8) {
			t.Errorf("request escaped the session's upstream: %s", req.URL)
		}
		w := httptest.NewRecorder()
		serve(w, req)
		return w.Result(), nil
	})
	return r
}

func relayRequest(r *directoryDiscoveryRig, method, target, body string, headers http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://gateway.example.invalid"+target, strings.NewReader(body))
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Authorization", "Bearer "+r.tokens["12448"])
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}

func TestBusinessRelayPreservesNewEndpointUnknownFieldsAndStatus(t *testing.T) {
	requestBody := " {\"new_field\":{\"decimal\":\"1.000\",\"nullable\":null},\"repeated\":[1,2]} \n"
	responseBody := " {\"data\":{\"future\":true},\"page\":{\"next\":\"opaque\"},\"extra\":null} \n"
	r := relayRig(t, func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if req.Method != "PATCH" || req.URL.Path != "/api/new-business/record" || req.URL.RawQuery != "q=a%20b&q=c+z&future_filter=1&page_token=%2B%2F%3D" || string(body) != requestBody {
			t.Errorf("request changed: %s %s %q", req.Method, req.URL, body)
		}
		if req.Header.Get("If-Match") != `"v1"` || req.Header.Get("Idempotency-Key") != "operation-1" || req.Header.Get("X-New-Business-Option") != "opaque" {
			t.Error("end-to-end request headers lost")
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("X-New-Business-Result", "present")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, responseBody)
	})
	w := relayRequest(r, "PATCH", "/api/new-business/record?q=a%20b&q=c+z&future_filter=1&page_token=%2B%2F%3D", requestBody, http.Header{"Content-Type": {"application/json"}, "If-Match": {`"v1"`}, "Idempotency-Key": {"operation-1"}, "X-New-Business-Option": {"opaque"}})
	if w.Code != 201 || w.Body.String() != responseBody || w.Header().Get("ETag") != `"v2"` || w.Header().Get("X-New-Business-Result") != "present" {
		t.Fatalf("response changed: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestBusinessRelayPassesBusinessRoutesErrorsAndProfileWithoutEnrichment(t *testing.T) {
	for _, endpoint := range []string{"/api/profile", "/api/vehicles/options?new_filter=yes", "/api/leaves/approvals", "/api/documents/1/workspace", "/api/notifications/read-all", "/api/usage/activity"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, status := range []int{200, 204, 403, 409, 422, 429, 500, 503} {
				body := `{"error":{"code":"FUTURE_BUSINESS_ERROR","fields":{"new":"problem"}},"extra":[1]}`
				if status == 204 {
					body = ""
				}
				r := relayRig(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, body)
				})
				w := relayRequest(r, "POST", endpoint, `{"new_field":true}`, nil)
				if w.Code != status || w.Body.String() != body || w.Header().Get("Retry-After") != "120" {
					t.Fatalf("%d changed to %d %s", status, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestBusinessRelayPreservesMultipartAndBinaryRangeAndHead(t *testing.T) {
	multipart := "--new-boundary\r\nContent-Disposition: form-data; name=\"metadata\"\r\n\r\n{\"new\":true}\r\n--new-boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"original.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%PDF-opaque\r\n--new-boundary--\r\n"
	contentType := "multipart/form-data; boundary=new-boundary"
	r := relayRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" {
			body, _ := io.ReadAll(req.Body)
			if string(body) != multipart || req.Header.Get("Content-Type") != contentType {
				t.Error("multipart reconstructed")
			}
			w.WriteHeader(202)
			return
		}
		if req.Header.Get("Range") != "bytes=1-3" || req.Header.Get("If-Range") != `"file-version"` {
			t.Error("range request headers lost")
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Range", "bytes 1-3/8")
		w.Header().Set("Content-Length", "3")
		w.Header().Set("Content-Disposition", `attachment; filename="original.pdf"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(206)
		if req.Method != "HEAD" {
			_, _ = w.Write([]byte{0x00, 0xff, 0x02})
		}
	})
	if w := relayRequest(r, "POST", "/api/attendance/photos", multipart, http.Header{"Content-Type": {contentType}}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := relayRequest(r, method, "/api/documents/1/file", "", http.Header{"Range": {"bytes=1-3"}, "If-Range": {`"file-version"`}})
		if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 1-3/8" || w.Header().Get("Content-Length") != "3" || w.Header().Get("Content-Disposition") != `attachment; filename="original.pdf"` {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		if method == "HEAD" && w.Body.Len() != 0 || method == "GET" && !bytes.Equal(w.Body.Bytes(), []byte{0x00, 0xff, 0x02}) {
			t.Error("binary body changed")
		}
	}
}

func TestBusinessRelayStripsClientIdentityAndHopHeaders(t *testing.T) {
	r := relayRig(t, func(w http.ResponseWriter, req *http.Request) {
		for _, header := range []string{"Cookie", "Connection", "X-Hop", "Proxy-Authorization", "Forwarded", "X-Forwarded-Host", "X-ERP-Proxy-Secret", "X-ERP-TLS-Verify", "X-HTTP-Method-Override", "X-Original-URL"} {
			if req.Header.Get(header) != "" {
				t.Errorf("untrusted header forwarded: %s", header)
			}
		}
		w.Header().Set("Connection", "X-Private-Hop")
		w.Header().Set("X-Private-Hop", "private")
		w.Header().Set("Set-Cookie", "erp-session=private")
		w.Header().Set("X-ERP-Proxy-Secret", "private")
		w.WriteHeader(200)
	})
	w := relayRequest(r, "GET", "/api/future", "", http.Header{"Cookie": {"session=spoof"}, "Connection": {"X-Hop"}, "X-Hop": {"spoof"}, "Proxy-Authorization": {"secret"}, "Forwarded": {"host=evil"}, "X-Forwarded-Host": {"evil"}, "X-ERP-Proxy-Secret": {"spoof"}, "X-ERP-TLS-Verify": {"SUCCESS"}, "X-Gateway-Key": {"spoof"}, "X-HTTP-Method-Override": {"DELETE"}, "X-Original-URL": {"/api/management"}})
	for _, header := range []string{"Connection", "X-Private-Hop", "Set-Cookie", "X-ERP-Proxy-Secret"} {
		if w.Header().Get(header) != "" {
			t.Errorf("private upstream header leaked: %s", header)
		}
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBusinessRelayBlocksControlNamespacesAndTraversal(t *testing.T) {
	r := relayRig(t, func(http.ResponseWriter, *http.Request) { t.Error("control path reached business upstream") })
	for _, target := range []string{"/api/management/usage/summary", "/api/notification-outbox/1", "/api/status", "/api/version", "/api/internal/secret", "/api/admin/login", "/api/auth/external/complete", "/api/future/../management/usage/summary", "/api/future/%2e%2e/management/usage/summary", "/api/future/%252e%252e/management", "/api/future%2f..%2fmanagement/secret", "/api/future%5c..%5cmanagement"} {
		w := relayRequest(r, "GET", target, "", nil)
		if w.Code < 400 || w.Code >= 500 {
			t.Errorf("unsafe path %s => %d %s", target, w.Code, w.Body.String())
		}
	}
}

func TestBusinessRelayDoesNotFallbackAfterDiscoveryOrUpstreamFailure(t *testing.T) {
	r := relayRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://untrusted.invalid/private")
		w.WriteHeader(307)
		_, _ = io.WriteString(w, "upstream redirect")
	})
	w := relayRequest(r, "GET", "/api/vehicles/new-action", "", nil)
	if w.Code != 307 || w.Body.String() != "upstream redirect" {
		t.Fatal(w.Code, w.Body.String())
	}
	r = relayRig(t, func(http.ResponseWriter, *http.Request) { t.Error("discovery failure called business endpoint") })
	r.fixtures["12448"].marker = "unknown-backend"
	if w = relayRequest(r, "POST", "/api/vehicles", `{"new":true}`, nil); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBusinessRelayRechecksSessionBeforeReturningData(t *testing.T) {
	var r *directoryDiscoveryRig
	r = relayRig(t, func(w http.ResponseWriter, _ *http.Request) {
		if err := r.store.revoke(context.Background(), r.tokens["12448"]); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, "private data")
	})
	w := relayRequest(r, "GET", "/api/future", "", nil)
	if w.Code != 401 || strings.Contains(w.Body.String(), "private data") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBusinessRelayPreservesEscapedPathUnderRegisteredBase(t *testing.T) {
	r := newDirectoryDiscoveryRig(t)
	if _, err := r.store.db.Exec("UPDATE hospitals SET api_url=? WHERE id=?", "https://12448.example.invalid/installation", r.sites["12448"].ID); err != nil {
		t.Fatal(err)
	}
	policy, err := newUpstreamPolicy([]string{"12448=https://12448.example.invalid/installation"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	r.app.egress = policy
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if req.URL.Path == "/installation/api/status" {
			writeData(w, map[string]any{"hospital_code": "12448", "api_version": "1", "erp_reachable": true, "backend": "erp_api_module"})
		} else {
			if req.URL.EscapedPath() != "/installation/api/future/%61%20file" || req.URL.RawQuery != "q=%2f&q=%2F" {
				t.Error("registered prefix or escaping changed:", req.URL)
			}
			w.WriteHeader(202)
		}
		return w.Result(), nil
	})
	if w := relayRequest(r, "PUT", "/api/future/%61%20file?q=%2f&q=%2F", "opaque", nil); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBusinessRelayRequiresSessionForNewRoutesAndRejectsTransportAbuse(t *testing.T) {
	r := relayRig(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid request reached business upstream") })
	req := httptest.NewRequest("GET", "https://gateway.example.invalid/api/future", nil)
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"TRACE", "CONNECT"} {
		if w := relayRequest(r, method, "/api/future", "", nil); w.Code != 405 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	req = httptest.NewRequest("POST", "https://gateway.example.invalid/api/future", strings.NewReader("opaque"))
	req.Header.Set("Authorization", "Bearer "+r.tokens["12448"])
	req.ContentLength = businessRequestLimit + 1
	w = httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
}

type interruptedRelayBody struct{ sent bool }

func (body *interruptedRelayBody) Read(p []byte) (int, error) {
	if !body.sent {
		body.sent = true
		return copy(p, "partial private file"), nil
	}
	return 0, errors.New("interrupted upstream")
}
func (*interruptedRelayBody) Close() error { return nil }

func TestBusinessRelayAbortsInterruptedResponsesAndCancelsRequests(t *testing.T) {
	r := relayRig(t, func(http.ResponseWriter, *http.Request) {})
	prior := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/status" {
			return prior.RoundTrip(req)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: &interruptedRelayBody{}}, nil
	})
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Errorf("interrupted stream did not abort HTTP response: %v", recovered)
			}
		}()
		relayRequest(r, "GET", "/api/future/file", "", nil)
	}()
	var gotContext context.Context
	entered, finished := make(chan struct{}), make(chan struct{})
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		gotContext = req.Context()
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "https://gateway.example.invalid/api/future/file", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+r.tokens["12448"])
	w := httptest.NewRecorder()
	go func() {
		defer close(finished)
		r.handler.ServeHTTP(w, req)
	}()
	<-entered
	cancel()
	<-finished
	if gotContext == nil || !errors.Is(gotContext.Err(), context.Canceled) {
		t.Error("client cancellation lost")
	}
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBusinessRelayBindingRejectsCrossHospitalConfiguration(t *testing.T) {
	r := relayRig(t, func(http.ResponseWriter, *http.Request) { t.Error("changed destination reached business endpoint") })
	prior := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := prior.RoundTrip(req)
		if req.URL.Path == "/api/status" {
			if _, updateErr := r.store.db.Exec("UPDATE hospitals SET api_url=? WHERE id=?", "https://11447.example.invalid", r.sites["12448"].ID); updateErr != nil {
				t.Fatal(updateErr)
			}
		}
		return resp, err
	})
	w := relayRequest(r, "GET", "/api/future", "", nil)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Query and body hospital names never choose the registry destination.
	r = relayRig(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) })
	w = relayRequest(r, "POST", "/api/future?hospital_code=11447&api_url=https://untrusted.invalid", `{"hospital_code":"11447","api_url":"https://untrusted.invalid"}`, nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
}
