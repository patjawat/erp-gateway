package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepairUploadStrictRoutingAndSafeResponse(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	calls := 0
	status := 200
	code := ""
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/api/repairs/10/attachments" || req.Method != "POST" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[a.Code] || req.Header.Get("Cookie") != "" {
			t.Fatal("wrong destination")
		}
		w := httptest.NewRecorder()
		if status != 200 {
			fail(w, status, code, "private message")
			return w.Result(), nil
		}
		writeData(w, contracts.RepairAttachment{ID: "1", FileName: "test.png", MimeType: "image/png", DownloadURL: "/api/repairs/10/attachments/1"})
		return w.Result(), nil
	})
	input := contracts.RepairUpload{RequestID: "upload-test-00000001", FileName: "test.png", DataBase64: "aGVsbG8="}
	w := r.request(t, "POST", "/api/repairs/10/attachments", input, token, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before := calls
	for _, bad := range []map[string]string{
		{"request_id": "upload-test-00000001", "file_name": "<script>.png", "data_base64": "aA=="},
		{"request_id": "upload-test-00000001", "file_name": "a.png", "data_base64": "aA==", "ref": "other"},
		{"request_id": "short", "file_name": "a.png", "data_base64": "aA=="},
	} {
		w = r.request(t, "POST", "/api/repairs/10/attachments", bad, token, false)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != before {
		t.Fatal("invalid upstream")
	}
	for _, e := range []struct {
		status int
		code   string
	}{{409, "UPLOAD_CONFLICT"}, {409, "UPLOAD_LIMIT"}, {503, "REPAIR_UPLOAD_DISABLED"}} {
		status = e.status
		code = e.code
		w = r.request(t, "POST", "/api/repairs/10/attachments", input, token, false)
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
