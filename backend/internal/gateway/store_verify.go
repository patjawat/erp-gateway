package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// VerifyStoreKey probes existing data before any bootstrap migration or write.
// It intentionally refuses databases without a valid sentinel instead of
// treating an unknown existing file as a fresh install.
func VerifyStoreKey(path string, key []byte) error {
	if len(key) != 32 {
		return errors.New("encryption key must be exactly 32 bytes")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("existing gateway database is missing or incomplete")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var check []byte
	if err = db.QueryRow("SELECT value FROM secrets_check WHERE id=1").Scan(&check); err != nil {
		return errors.New("existing database has no readable encryption sentinel; restore a consistent backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	n := aead.NonceSize()
	if len(check) < n {
		return errors.New("existing encryption sentinel is invalid")
	}
	plain, err := aead.Open(nil, check[:n], check[n:], []byte("store-check"))
	if err != nil || string(plain) != "erp-platform-gateway" {
		return errors.New("cannot unlock existing gateway database with this key")
	}
	return nil
}
