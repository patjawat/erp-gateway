package contracts

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxRepairUploadBytes = 5 * 1024 * 1024
const MaxRepairUploadJSON = 8 * 1024 * 1024
const MaxRepairAttachments = 10

type RepairUpload struct {
	RequestID  string `json:"request_id"`
	FileName   string `json:"file_name"`
	DataBase64 string `json:"data_base64"`
}

func ValidateRepairUpload(v *RepairUpload) error {
	if v == nil || !repairRequestID.MatchString(v.RequestID) || len(v.DataBase64) == 0 || len(v.DataBase64) > ((MaxRepairUploadBytes+2)/3)*4 {
		return errors.New("invalid upload")
	}
	v.FileName = strings.TrimSpace(v.FileName)
	if v.FileName == "" || !utf8.ValidString(v.FileName) || utf8.RuneCountInString(v.FileName) > 100 || strings.ContainsAny(v.FileName, "/\\\x00\r\n") {
		return errors.New("invalid filename")
	}
	for _, r := range v.FileName {
		if !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == ' ' || strings.ContainsRune("._-()", r)) {
			return errors.New("invalid filename")
		}
	}
	return nil
}
