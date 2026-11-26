package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepairAttachmentBinaryProxy(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	mimeBody := "GIF89a synthetic fixture"
	status := 200
	code := ""
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/api/repairs/10/attachments/1" || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[a.Code] {
			t.Fatal("unsafe forwarding")
		}
		w := httptest.NewRecorder()
		if status != 200 {
			fail(w, status, code, "private upstream")
			return w.Result(), nil
		}
		w.WriteString(mimeBody)
		return w.Result(), nil
	})
	w := r.request(t, "GET", "/api/repairs/10/attachments/1", nil, token, false)
	if w.Code != 200 || w.Body.String() != mimeBody || w.Header().Get("Content-Type") != "image/gif" {
		t.Fatal(w.Code, w.Body.String())
	}
	mimeBody = "<script>bad</script>"
	w = r.request(t, "GET", "/api/repairs/10/attachments/1", nil, token, false)
	if w.Code != 503 {
		t.Fatal("unsafe MIME accepted", w.Code)
	}
	for _, e := range []struct {
		status int
		code   string
	}{{404, "REPAIR_NOT_FOUND"}, {503, "REPAIR_ATTACHMENTS_DISABLED"}} {
		status = e.status
		code = e.code
		w = r.request(t, "GET", "/api/repairs/10/attachments/1", nil, token, false)
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	before := calls
	w = r.request(t, "POST", "/api/repairs/10/attachments/1", nil, token, false)
	if w.Code != 405 || calls != before {
		t.Fatal("unsupported write forwarded")
	}
}
