package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var hospitalAttachmentPDF = []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n")

func uploadHospitalAttachment(t *testing.T, rig *testRig, path, filename, category string, content []byte, authenticated, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("category", category); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://gateway.example.invalid"+path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if authenticated {
		req.AddCookie(rig.cookie)
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", rig.csrf)
	}
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, req)
	return w
}

func TestHospitalAttachmentLifecycleAndIsolation(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	b := r.add(t, "12448")
	r.enable(t, a)
	path := "/admin/api/hospitals/" + a.ID + "/attachments"
	w := uploadHospitalAttachment(t, r, path, "สัญญา 2026.pdf", "contract", hospitalAttachmentPDF, true, true)
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data hospitalAttachment `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	item := envelope.Data
	if item.ID == "" || item.HospitalID != a.ID || item.Filename != "สัญญา 2026.pdf" || item.Category != "contract" || item.ContentType != "application/pdf" || item.Size != int64(len(hospitalAttachmentPDF)) {
		t.Fatalf("wrong attachment metadata: %+v", item)
	}
	var stored []byte
	if err := r.app.config.Store.db.QueryRow(`SELECT content FROM hospital_attachments WHERE id=?`, item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, hospitalAttachmentPDF) {
		t.Fatal("attachment content was stored without encryption")
	}
	w = r.request(t, http.MethodGet, path, nil, "", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), item.ID) || strings.Contains(w.Body.String(), "%PDF-") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	w = r.request(t, http.MethodGet, path+"/"+item.ID, nil, "", true)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), hospitalAttachmentPDF) || w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != item.Filename {
		t.Fatalf("wrong download filename: %q", w.Header().Get("Content-Disposition"))
	}
	otherPath := "/admin/api/hospitals/" + b.ID + "/attachments/" + item.ID
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		w = r.request(t, method, otherPath, nil, "", true)
		if w.Code != http.StatusNotFound {
			t.Fatalf("cross-hospital %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		w = r.request(t, method, path+"/"+item.ID, nil, "", false)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	w = r.request(t, http.MethodDelete, path+"/"+item.ID, nil, "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	w = r.request(t, http.MethodGet, path+"/"+item.ID, nil, "", true)
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted download: %d", w.Code)
	}
	w = r.request(t, http.MethodGet, path, nil, "", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"attachments":[]`) {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}
	var auditCount int
	if err := r.app.config.Store.db.QueryRow(`SELECT count(*) FROM audit WHERE hospital_id=? AND action IN ('upload_attachment','delete_attachment')`, a.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("attachment audit count: %d %v", auditCount, err)
	}
	current, err := r.app.config.Store.hospital(t.Context(), "id", a.ID)
	if err != nil || !current.Enabled || current.ApprovalStatus != "approved" {
		t.Fatalf("attachments changed hospital approval: %+v %v", current, err)
	}
}

func TestHospitalAttachmentRejectsInvalidUploads(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	path := "/admin/api/hospitals/" + h.ID + "/attachments"
	for _, tc := range []struct {
		name, filename, category string
		content                  []byte
		authenticated, csrf      bool
		status                   int
	}{
		{"login required", "receipt.pdf", "bill", hospitalAttachmentPDF, false, false, http.StatusUnauthorized},
		{"csrf required", "receipt.pdf", "bill", hospitalAttachmentPDF, true, false, http.StatusForbidden},
		{"category invalid", "receipt.pdf", "other", hospitalAttachmentPDF, true, true, http.StatusBadRequest},
		{"mismatched extension", "receipt.png", "bill", hospitalAttachmentPDF, true, true, http.StatusBadRequest},
		{"html rejected", "receipt.pdf", "bill", []byte("<html><script>alert(1)</script></html>"), true, true, http.StatusBadRequest},
		{"empty rejected", "receipt.pdf", "bill", nil, true, true, http.StatusBadRequest},
		{"truncated pdf", "receipt.pdf", "bill", []byte("%PDF-1.7\n"), true, true, http.StatusBadRequest},
		{"svg rejected", "receipt.svg", "slip", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), true, true, http.StatusBadRequest},
		{"oversized rejected", "receipt.pdf", "document", bytes.Repeat([]byte{'x'}, maxHospitalAttachmentSize+1), true, true, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := uploadHospitalAttachment(t, r, path, tc.filename, tc.category, tc.content, tc.authenticated, tc.csrf)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d %s", tc.status, w.Code, w.Body.String())
			}
		})
	}
	var count int
	if err := r.app.config.Store.db.QueryRow(`SELECT count(*) FROM hospital_attachments`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid upload persisted: %d %v", count, err)
	}
	w := uploadHospitalAttachment(t, r, "/admin/api/hospitals/missing/attachments", "receipt.pdf", "bill", hospitalAttachmentPDF, true, true)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing hospital: %d %s", w.Code, w.Body.String())
	}
}

func TestHospitalAttachmentImageTypes(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	path := "/admin/api/hospitals/" + h.ID + "/attachments"
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.White)
	var jpg, pngFile, gifFile bytes.Buffer
	if err := jpeg.Encode(&jpg, picture, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pngFile, picture); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifFile, picture, nil); err != nil {
		t.Fatal(err)
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filename, contentType string
		content               []byte
	}{
		{"slip.JPG", "image/jpeg", jpg.Bytes()},
		{"slip.png", "image/png", pngFile.Bytes()},
		{"slip.gif", "image/gif", gifFile.Bytes()},
		{"slip.webp", "image/webp", webp},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			w := uploadHospitalAttachment(t, r, path, tc.filename, "slip", tc.content, true, true)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contentType) {
				t.Fatalf("image upload: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHospitalAttachmentDeleteRequiresCSRF(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	path := "/admin/api/hospitals/" + h.ID + "/attachments"
	w := uploadHospitalAttachment(t, r, path, "receipt.pdf", "bill", hospitalAttachmentPDF, true, true)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var envelope struct {
		Data hospitalAttachment `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "https://gateway.example.invalid"+path+"/"+envelope.Data.ID, nil)
	req.AddCookie(r.cookie)
	w = httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("delete without CSRF: %d %s", w.Code, w.Body.String())
	}
	w = r.request(t, http.MethodGet, path+"/"+envelope.Data.ID, nil, "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("rejected delete removed attachment: %d", w.Code)
	}
}
