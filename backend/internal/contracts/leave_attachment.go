package contracts

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const MaxLeaveAttachments = 3
const MaxLeaveAttachmentBytes = 5 * 1024 * 1024
const MaxLeaveInputJSON = 22 * 1024 * 1024

type LeaveUpload struct {
	FileName   string `json:"file_name"`
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

type LeaveAttachment struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	Size     int    `json:"size"`
}

func ValidLeaveUpload(v LeaveUpload) bool {
	if !utf8.ValidString(v.FileName) || len(v.FileName) == 0 || utf8.RuneCountInString(v.FileName) > 180 || strings.ContainsAny(v.FileName, "/\\\x00\r\n") || strings.TrimSpace(v.FileName) != v.FileName {
		return false
	}
	ext := strings.ToLower(filepath.Ext(v.FileName))
	if !(v.MimeType == "application/pdf" && ext == ".pdf" || v.MimeType == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg") || v.MimeType == "image/png" && ext == ".png") {
		return false
	}
	if len(v.DataBase64) == 0 || len(v.DataBase64) > base64.StdEncoding.EncodedLen(MaxLeaveAttachmentBytes) {
		return false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(v.DataBase64)
	return err == nil && len(raw) > 0 && len(raw) <= MaxLeaveAttachmentBytes
}

func ValidLeaveAttachment(v LeaveAttachment) bool {
	if len(v.ID) != 32 || strings.Trim(v.ID, "0123456789abcdef") != "" || v.Size < 1 || v.Size > MaxLeaveAttachmentBytes {
		return false
	}
	return utf8.ValidString(v.FileName) && utf8.RuneCountInString(v.FileName) <= 180 && v.FileName != "" && !strings.ContainsAny(v.FileName, "/\\\x00\r\n") && (v.MimeType == "image/jpeg" || v.MimeType == "image/png" || v.MimeType == "application/pdf")
}
