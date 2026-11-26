// Package backupcloud connects the owner's Google Drive to encrypted Gateway
// repositories. Callers must encrypt Credentials at rest and consume OAuth
// attempts once, bound to the authenticated owner and their expiration.
package backupcloud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const driveScope = "https://www.googleapis.com/auth/drive.file"

var (
	ErrReconnect = errors.New("Google Drive authorization expired or was revoked; reconnect the owner account")
	ErrQuota     = errors.New("Google Drive storage or request quota exceeded; existing backups were retained")
)

// Credentials are secret material, never a browser-facing settings DTO.
type Credentials struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	RedirectURL  string    `json:"redirect_url"`
	FolderID     string    `json:"folder_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

type Attempt struct {
	State     string    `json:"state"`
	Verifier  string    `json:"-"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Client endpoint overrides exist for integration tests. They are code-owned,
// never populated from a browser request or recovery archive.
type Client struct {
	HTTP     *http.Client
	AuthURL  string
	TokenURL string
	DriveURL string
}

var defaultClient = Client{}

func Begin(clientID, redirectURL string) (Attempt, error) {
	return defaultClient.Begin(clientID, redirectURL)
}
func Exchange(ctx context.Context, credentials Credentials, code, verifier string) (Credentials, error) {
	return defaultClient.Exchange(ctx, credentials, code, verifier)
}
func Refresh(ctx context.Context, credentials Credentials) (Credentials, error) {
	return defaultClient.Refresh(ctx, credentials)
}
func EnsureFolder(ctx context.Context, credentials Credentials) (Credentials, error) {
	return defaultClient.EnsureFolder(ctx, credentials)
}

func singleLine(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

// ValidateRedirect accepts Google's HTTPS web-app callbacks and HTTP loopback
// callbacks. Private LAN IPs over plain HTTP are deliberately not loopback.
func ValidateRedirect(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" || !singleLine(value, 2048) {
		return errors.New("Google callback must be a registered HTTPS or localhost URL without credentials, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return errors.New("Google callback requires HTTPS or a registered localhost/SSH-tunnel callback")
	}
	return nil
}

func randomURLToken(bytes int) (string, error) {
	body := make([]byte, bytes)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func (c Client) Begin(clientID, redirectURL string) (Attempt, error) {
	if !singleLine(clientID, 512) {
		return Attempt{}, errors.New("specify the owner's Google OAuth client ID")
	}
	if err := ValidateRedirect(redirectURL); err != nil {
		return Attempt{}, err
	}
	state, err := randomURLToken(32)
	if err != nil {
		return Attempt{}, err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return Attempt{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	endpoint := c.AuthURL
	if endpoint == "" {
		endpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return Attempt{}, errors.New("invalid configured OAuth authorization endpoint")
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", driveScope)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("include_granted_scopes", "true")
	u.RawQuery = q.Encode()
	return Attempt{State: state, Verifier: verifier, URL: u.String(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}, nil
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func validClient(credentials Credentials) error {
	if !singleLine(credentials.ClientID, 512) || !singleLine(credentials.ClientSecret, 2048) {
		return errors.New("specify the owner's Google OAuth client ID and secret")
	}
	return nil
}

func (c Client) Exchange(ctx context.Context, credentials Credentials, code, verifier string) (Credentials, error) {
	if err := validClient(credentials); err != nil {
		return credentials, err
	}
	if err := ValidateRedirect(credentials.RedirectURL); err != nil {
		return credentials, err
	}
	if !singleLine(code, 4096) || !singleLine(verifier, 128) || len(verifier) < 43 || strings.ContainsAny(verifier, " &+=/?") {
		return credentials, errors.New("invalid Google authorization response")
	}
	return c.token(ctx, credentials, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {credentials.RedirectURL}, "code_verifier": {verifier}})
}

func (c Client) Refresh(ctx context.Context, credentials Credentials) (Credentials, error) {
	if err := validClient(credentials); err != nil {
		return credentials, err
	}
	if credentials.AccessToken != "" && credentials.Expiry.After(time.Now().Add(time.Minute)) {
		return credentials, nil
	}
	if !singleLine(credentials.RefreshToken, 16384) {
		return credentials, ErrReconnect
	}
	return c.token(ctx, credentials, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {credentials.RefreshToken}})
}

func (c Client) token(ctx context.Context, credentials Credentials, values url.Values) (Credentials, error) {
	values.Set("client_id", credentials.ClientID)
	values.Set("client_secret", credentials.ClientSecret)
	endpoint := c.TokenURL
	if endpoint == "" {
		endpoint = "https://oauth2.googleapis.com/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return credentials, errors.New("invalid configured Google token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return credentials, ctx.Err()
		}
		return credentials, errors.New("cannot connect to Google authorization service")
	}
	defer response.Body.Close()
	body, err := readLimited(response.Body)
	if err != nil {
		return credentials, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 403 {
			return credentials, ErrReconnect
		}
		return credentials, fmt.Errorf("Google authorization service unavailable (HTTP %d)", response.StatusCode)
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if json.Unmarshal(body, &result) != nil || !singleLine(result.AccessToken, 16384) || result.ExpiresIn < 1 || result.ExpiresIn > 86400 || (result.TokenType != "" && !strings.EqualFold(result.TokenType, "Bearer")) {
		return credentials, errors.New("Google returned an invalid authorization response")
	}
	if result.RefreshToken != "" {
		if !singleLine(result.RefreshToken, 16384) {
			return credentials, errors.New("Google returned an invalid refresh credential")
		}
		credentials.RefreshToken = result.RefreshToken
	}
	if credentials.RefreshToken == "" {
		return credentials, errors.New("Google did not provide offline access; reconnect and approve offline consent")
	}
	credentials.AccessToken = result.AccessToken
	credentials.Expiry = time.Now().UTC().Add(time.Duration(result.ExpiresIn) * time.Second)
	return credentials, nil
}

func readLimited(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return nil, errors.New("cannot read bounded Google service response")
	}
	return body, nil
}
