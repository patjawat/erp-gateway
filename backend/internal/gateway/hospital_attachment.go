package gateway

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxHospitalAttachmentSize = 10 << 20

type hospitalAttachment struct {
	ID          string `json:"id"`
	HospitalID  string `json:"hospital_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Category    string `json:"category"`
	Size        int64  `json:"size"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Server) attachmentHospital(w http.ResponseWriter, r *http.Request, mutation bool) (Hospital, bool) {
	if _, ok := s.adminAuth(w, r, mutation); !ok {
		return Hospital{}, false
	}
	h, err := s.config.Store.hospital(r.Context(), "id", r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบโรงพยาบาล")
		} else {
			unavailable(w)
		}
		return Hospital{}, false
	}
	return h, true
}

func (s *Server) adminHospitalAttachments(w http.ResponseWriter, r *http.Request) {
	h, ok := s.attachmentHospital(w, r, false)
	if !ok {
		return
	}
	s.adminAttachmentPage(w, r, h)
}

func (s *Server) adminUploadHospitalAttachment(w http.ResponseWriter, r *http.Request) {
	h, ok := s.attachmentHospital(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxHospitalAttachmentSize+(64<<10))
	err := r.ParseMultipartForm(512 << 10)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "ATTACHMENT_TOO_LARGE", "ไฟล์แนบต้องมีขนาดไม่เกิน 10 MB")
		} else {
			fail(w, http.StatusBadRequest, "INVALID_ATTACHMENT", "กรุณาเลือกไฟล์แนบและส่งเป็น multipart/form-data")
		}
		return
	}
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 || len(r.MultipartForm.Value["category"]) != 1 {
		fail(w, http.StatusBadRequest, "INVALID_ATTACHMENT", "กรุณาแนบไฟล์ครั้งละ 1 ไฟล์และระบุประเภทเอกสาร")
		return
	}
	category := r.MultipartForm.Value["category"][0]
	switch category {
	case "document", "slip", "bill", "contract":
	default:
		fail(w, http.StatusBadRequest, "INVALID_ATTACHMENT_CATEGORY", "กรุณาเลือกประเภทเอกสาร สลิป บิล หรือสัญญา")
		return
	}
	header := r.MultipartForm.File["file"][0]
	filename := strings.TrimSpace(filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/")))
	if filename == "" || filename == "." || len(filename) > 255 || strings.IndexFunc(filename, unicode.IsControl) >= 0 {
		fail(w, http.StatusBadRequest, "INVALID_ATTACHMENT", "ชื่อไฟล์ไม่ถูกต้องหรือยาวเกิน 255 ไบต์")
		return
	}
	file, err := header.Open()
	if err != nil {
		unavailable(w)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxHospitalAttachmentSize+1))
	if err != nil {
		unavailable(w)
		return
	}
	if len(content) > maxHospitalAttachmentSize {
		fail(w, http.StatusRequestEntityTooLarge, "ATTACHMENT_TOO_LARGE", "ไฟล์แนบต้องมีขนาดไม่เกิน 10 MB")
		return
	}
	contentType, ok := hospitalAttachmentType(filename, content)
	if !ok {
		fail(w, http.StatusBadRequest, "UNSUPPORTED_ATTACHMENT", "รองรับเฉพาะ PDF และรูปภาพ JPEG, PNG, GIF หรือ WebP ที่มีนามสกุลตรงกับเนื้อหาไฟล์")
		return
	}
	id, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	item := hospitalAttachment{ID: id, HospitalID: h.ID, Filename: filename, ContentType: contentType, Category: category, Size: int64(len(content)), CreatedAt: time.Now().Unix()}
	encrypted, err := s.config.Store.encrypt("hospital-attachment:"+h.ID+":"+id, string(content))
	if err != nil {
		unavailable(w)
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO hospital_attachments(id,hospital_id,filename,content_type,category,size,content,created_at) VALUES(?,?,?,?,?,?,?,?)`, item.ID, item.HospitalID, item.Filename, item.ContentType, item.Category, item.Size, encrypted, item.CreatedAt)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)`, h.ID, h.Code, "upload_attachment", item.Filename, item.CreatedAt)
	}
	if err != nil {
		unavailable(w)
		return
	}
	if tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, item)
}

func hospitalAttachmentType(filename string, content []byte) (string, bool) {
	contentType := http.DetectContentType(content)
	ext := strings.ToLower(filepath.Ext(filename))
	switch contentType {
	case "application/pdf":
		return contentType, ext == ".pdf" && bytes.HasPrefix(content, []byte("%PDF-")) && bytes.Contains(content[max(0, len(content)-1024):], []byte("%%EOF"))
	case "image/jpeg", "image/png", "image/gif":
		validExt := (contentType == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg")) || (contentType == "image/png" && ext == ".png") || (contentType == "image/gif" && ext == ".gif")
		if !validExt {
			return "", false
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(content))
		return contentType, err == nil && config.Width > 0 && config.Height > 0
	case "image/webp":
		// WebP has no decoder in Go's standard library. Validate its RIFF
		// container and image chunk in addition to sniffing its content type.
		return contentType, ext == ".webp" && validHospitalWebP(content)
	default:
		return "", false
	}
}

func validHospitalWebP(content []byte) bool {
	if len(content) < 20 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(content[4:8]))+8 != uint64(len(content)) {
		return false
	}
	imageFound := false
	for offset := 12; offset < len(content); {
		if len(content)-offset < 8 {
			return false
		}
		size := uint64(binary.LittleEndian.Uint32(content[offset+4 : offset+8]))
		end := uint64(offset) + 8 + size
		if end > uint64(len(content)) {
			return false
		}
		switch string(content[offset : offset+4]) {
		case "VP8 ":
			imageFound = imageFound || size >= 10
		case "VP8L":
			imageFound = imageFound || (size >= 5 && content[offset+8] == 0x2f)
		case "ANMF":
			imageFound = imageFound || size >= 16
		}
		offset = int(end + size%2)
		if offset > len(content) {
			return false
		}
	}
	return imageFound
}

func (s *Server) adminDownloadHospitalAttachment(w http.ResponseWriter, r *http.Request) {
	h, ok := s.attachmentHospital(w, r, false)
	if !ok {
		return
	}
	var item hospitalAttachment
	var encrypted []byte
	err := s.config.Store.db.QueryRowContext(r.Context(), `SELECT id,filename,content_type,size,content FROM hospital_attachments WHERE hospital_id=? AND id=?`, h.ID, r.PathValue("attachmentID")).Scan(&item.ID, &item.Filename, &item.ContentType, &item.Size, &encrypted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบไฟล์แนบ")
		} else {
			unavailable(w)
		}
		return
	}
	content, err := s.config.Store.decrypt("hospital-attachment:"+h.ID+":"+item.ID, encrypted)
	if err != nil || int64(len(content)) != item.Size {
		unavailable(w)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.Filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, content)
}

func (s *Server) adminDeleteHospitalAttachment(w http.ResponseWriter, r *http.Request) {
	h, ok := s.attachmentHospital(w, r, true)
	if !ok {
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	var filename string
	err = tx.QueryRowContext(r.Context(), `SELECT filename FROM hospital_attachments WHERE hospital_id=? AND id=?`, h.ID, r.PathValue("attachmentID")).Scan(&filename)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบไฟล์แนบ")
		} else {
			unavailable(w)
		}
		return
	}
	_, err = tx.ExecContext(r.Context(), `DELETE FROM hospital_attachments WHERE hospital_id=? AND id=?`, h.ID, r.PathValue("attachmentID"))
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)`, h.ID, h.Code, "delete_attachment", filename, time.Now().Unix())
	}
	if err != nil {
		unavailable(w)
		return
	}
	if tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]bool{"deleted": true})
}
