package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentNativeGatewayRejectsForgedMutationResults(t *testing.T) {
	for _, tc := range []struct {
		kind, target string
		result       contracts.DocumentNativeResult
	}{
		{"registrations", "", contracts.DocumentNativeResult{ID: "1", NotificationStatus: "not_requested", Number: 0, Year: 2569}},
		{"registrations", "", contracts.DocumentNativeResult{ID: "1", NotificationStatus: "not_requested", Number: 12, Year: 0}},
		{"tasks", "", contracts.DocumentNativeResult{ID: "1", NotificationStatus: "not_requested", Count: 0}},
		{"tasks", "", contracts.DocumentNativeResult{ID: "1", NotificationStatus: "not_requested", Count: 2, IDs: []string{"1", "1"}}},
		{"tasks", "", contracts.DocumentNativeResult{ID: "2", NotificationStatus: "not_requested", Count: 1, IDs: []string{"1"}}},
		{"templates", "3", contracts.DocumentNativeResult{ID: "3", NotificationStatus: "not_requested"}},
		{"routes", "3", contracts.DocumentNativeResult{ID: "4", Deleted: true, NotificationStatus: "not_requested"}},
	} {
		if validDocumentNative(&tc.result, contracts.DocumentNativeRoute{Kind: tc.kind, Target: tc.target}) {
			t.Fatal("forged result accepted", tc)
		}
	}
	valid := contracts.DocumentNativeResult{ID: "1", IDs: []string{"1", "2"}, Count: 2, NotificationStatus: "not_requested"}
	if !validDocumentNative(&valid, contracts.DocumentNativeRoute{Kind: "tasks"}) {
		t.Fatal("valid batch result rejected")
	}
}

func TestDocumentNativeGatewayForcesAttachmentDisposition(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/documents/1/files/") {
			return previous.RoundTrip(req)
		}
		body, m, name := "%PDF-1.7\nfixture", "application/pdf", "book.pdf"
		if strings.HasSuffix(req.URL.Path, "/3") {
			body = "PK\x03\x04fixture"
			m = "application/zip"
			name = "files.zip"
		}
		if strings.HasSuffix(req.URL.Path, "/4") {
			body = "<html><script>fixture only</script></html>"
			m = "application/octet-stream"
			name = "notes.html"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{m}, "Content-Disposition": []string{mime.FormatMediaType("inline", map[string]string{"filename": name})}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	for _, path := range []string{"/api/documents/1/files/2?download=1", "/api/documents/1/files/3", "/api/documents/1/files/4"} {
		w := r.request(t, "GET", path, nil, token, false)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		disposition, _, e := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
		if e != nil || disposition != "attachment" {
			t.Fatal(path, w.Header(), e)
		}
		if strings.HasSuffix(path, "/4") && w.Header().Get("Content-Type") != "application/octet-stream" {
			t.Fatal("generic HTML became inline MIME", w.Header())
		}
	}
}
