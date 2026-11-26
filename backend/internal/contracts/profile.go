package contracts

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxProfileSignatureBytes = 2 * 1024 * 1024
const MaxProfileUpdateJSON = 3 * 1024 * 1024

// ProfileUpdate intentionally excludes identity, hospital, employee ID and roles.
// Omitted signature_data preserves the current HR signature.
type ProfileUpdate struct {
	Email         string  `json:"email"`
	Phone         string  `json:"phone"`
	Address       string  `json:"address"`
	SignatureData *string `json:"signature_data,omitempty"`
}

func ValidateProfileUpdate(v *ProfileUpdate) error {
	v.Email, v.Phone, v.Address = strings.TrimSpace(v.Email), strings.TrimSpace(v.Phone), strings.TrimSpace(v.Address)
	for _, text := range []string{v.Email, v.Phone, v.Address} {
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return errors.New("invalid profile fields")
		}
	}
	if utf8.RuneCountInString(v.Email) > 255 || utf8.RuneCountInString(v.Phone) > 20 || utf8.RuneCountInString(v.Address) > 255 {
		return errors.New("invalid profile fields")
	}
	if v.Email != "" {
		parsed, err := mail.ParseAddress(v.Email)
		if err != nil || parsed.Address != v.Email || strings.ContainsAny(v.Email, "\r\n") {
			return errors.New("invalid profile email")
		}
	}
	for _, r := range v.Phone {
		if !unicode.IsDigit(r) && !strings.ContainsRune(" +()-.", r) {
			return errors.New("invalid profile phone")
		}
	}
	if v.SignatureData != nil && (!strings.HasPrefix(*v.SignatureData, "data:image/png;base64,") && !strings.HasPrefix(*v.SignatureData, "data:image/jpeg;base64,") || len(*v.SignatureData) > (MaxProfileSignatureBytes+2)/3*4+32) {
		return errors.New("invalid profile signature")
	}
	return nil
}
