package backupcloud

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Prepared is an ephemeral rclone configuration for one repository operation.
// Always call Credentials even after a failed operation, persist the returned
// credentials encrypted, and only then Close the plaintext staging directory.
type Prepared struct {
	Repository string
	Env        []string
	directory  string
	configFile string
	original   Credentials
	closeOnce  sync.Once
	closeErr   error
}

func Prepare(ctx context.Context, credentials Credentials, privateDir string) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validClient(credentials); err != nil {
		return nil, err
	}
	if !folderIDPattern.MatchString(credentials.FolderID) {
		return nil, errors.New("connect an application-owned Google Drive folder first")
	}
	if !singleLine(credentials.AccessToken, 16384) || !singleLine(credentials.RefreshToken, 16384) {
		return nil, ErrReconnect
	}
	info, err := os.Lstat(privateDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("rclone configuration requires a private existing staging directory")
	}
	directory, err := os.MkdirTemp(privateDir, ".rclone-")
	if err != nil {
		return nil, err
	}
	remove := func() { _ = os.RemoveAll(directory) }
	if err = os.Chmod(directory, 0700); err != nil {
		remove()
		return nil, err
	}
	configFile := filepath.Join(directory, "rclone.conf")
	token, err := json.Marshal(struct {
		AccessToken  string    `json:"access_token"`
		TokenType    string    `json:"token_type"`
		RefreshToken string    `json:"refresh_token"`
		Expiry       time.Time `json:"expiry"`
	}{credentials.AccessToken, "Bearer", credentials.RefreshToken, credentials.Expiry})
	if err != nil {
		remove()
		return nil, err
	}
	config := "[gateway]\ntype = drive\nclient_id = " + credentials.ClientID + "\nclient_secret = " + credentials.ClientSecret + "\nscope = drive.file\nroot_folder_id = " + credentials.FolderID + "\ntoken = " + string(token) + "\n"
	if err = os.WriteFile(configFile, []byte(config), 0600); err != nil {
		remove()
		return nil, err
	}
	return &Prepared{Repository: "rclone:gateway:repository", Env: []string{"RCLONE_CONFIG=" + configFile, "RCLONE_CACHE_DIR=" + filepath.Join(directory, "cache"), "RCLONE_RETRIES=2", "RCLONE_LOW_LEVEL_RETRIES=3", "RCLONE_CONTIMEOUT=15s", "RCLONE_TIMEOUT=60s", "RCLONE_LOG_LEVEL=ERROR", "PATH=/usr/local/bin:/usr/bin:/bin"}, directory: directory, configFile: configFile, original: credentials}, nil
}

func (p *Prepared) Credentials() (Credentials, error) {
	result := p.original
	file, err := os.Open(p.configFile)
	if err != nil {
		return result, errors.New("cannot read refreshed Google Drive credentials")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return result, errors.New("invalid temporary Google Drive configuration")
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64<<10)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if section != "[gateway]" || !ok || strings.TrimSpace(key) != "token" {
			continue
		}
		var token struct {
			AccessToken  string    `json:"access_token"`
			RefreshToken string    `json:"refresh_token"`
			Expiry       time.Time `json:"expiry"`
			TokenType    string    `json:"token_type"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(value)), &token) != nil || !singleLine(token.AccessToken, 16384) || (token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer")) {
			return result, errors.New("invalid refreshed Google Drive credentials")
		}
		if token.RefreshToken != "" {
			if !singleLine(token.RefreshToken, 16384) {
				return result, errors.New("invalid refreshed Google Drive credential")
			}
			result.RefreshToken = token.RefreshToken
		}
		result.AccessToken, result.Expiry = token.AccessToken, token.Expiry
		return result, nil
	}
	return result, errors.New("temporary Google Drive configuration has no refreshed token")
}

func (p *Prepared) Close() error {
	p.closeOnce.Do(func() { p.closeErr = os.RemoveAll(p.directory) })
	return p.closeErr
}
