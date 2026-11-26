package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestMedicalCertificateGatewayKeepsTenantAndPrivateDownload(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("Cookie") != "" {
			t.Fatal("wrong credential")
		}
		out := httptest.NewRecorder()
		out.Header().Set("Content-Type", "application/pdf")
		out.Write([]byte("%PDF-1.4\n" + code + "\n%%EOF\n"))
		return out.Result(), nil
	})
	path := "/api/leaves/81/attachments/" + strings.Repeat("a", 32)
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		out := r.request(t, "GET", path, nil, tenant.token, false)
		if out.Code != 200 || out.Header().Get("Content-Type") != "application/pdf" || out.Header().Get("Cache-Control") != "no-store, private" || !strings.Contains(out.Body.String(), tenant.code) || strings.Contains(out.Body.String(), tenant.other) {
			t.Fatal("unsafe download", out.Code)
		}
	}
	before := calls
	if out := r.request(t, "GET", path, nil, "", false); out.Code != 401 {
		t.Fatal("anonymous access", out.Code)
	}
	if out := r.request(t, "GET", path+"?emp_id=99", nil, ta, false); out.Code != 404 {
		t.Fatal("caller identity accepted", out.Code)
	}
	if calls != before {
		t.Fatal("invalid requests reached upstream")
	}
}

func TestMedicalCertificateGatewayForwardsBoundedPayloadBeyondOldFormLimit(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	payload := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 150*1024)...)
	payload = append(payload, []byte("\n%%EOF\n")...)
	input := gatewayLeaveInput()
	input.Attachments = []contracts.LeaveUpload{{FileName: "medical.pdf", MimeType: "application/pdf", DataBase64: base64.StdEncoding.EncodeToString(payload)}}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		var forwarded contracts.LeaveInput
		if json.NewDecoder(request.Body).Decode(&forwarded) != nil || len(forwarded.Attachments) != 1 || forwarded.Attachments[0].DataBase64 != input.Attachments[0].DataBase64 {
			t.Fatal("medical payload changed")
		}
		out := httptest.NewRecorder()
		writeData(out, contracts.Leave{ID: "81", StartDate: input.StartDate, EndDate: input.EndDate, TotalDays: .5, AttachmentCount: 1, Attachments: []contracts.LeaveAttachment{{ID: strings.Repeat("a", 32), FileName: "medical.pdf", MimeType: "application/pdf", Size: len(payload)}}})
		return out.Result(), nil
	})
	if out := r.request(t, "POST", "/api/leaves", input, token, false); out.Code != 200 {
		t.Fatal("bounded upload rejected", out.Code, out.Body.String())
	}
}
