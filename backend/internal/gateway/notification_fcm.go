package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"erp-platform/gateway/internal/contracts"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type PushSender interface {
	Send(context.Context, string, string, contracts.NotificationPushItem, string) error
}
type PushError struct {
	InvalidToken, Permanent bool
	RetryAfter              time.Duration
	Code                    string
}

func (e *PushError) Error() string { return "push provider did not accept notification" }

type FCMSender struct {
	project string
	client  *http.Client
	tokens  oauth2.TokenSource
}

// Only the central Gateway loads this credential. It is never returned by an
// API, copied into Mobile, or distributed to hospital installations.
func NewFCMSender(ctx context.Context, path string) (*FCMSender, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("cannot open FCM credentials")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if e != nil || len(raw) > 64*1024 {
		return nil, errors.New("invalid FCM credentials")
	}
	var c struct{ Type, ProjectID, TokenURI string }
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("invalid FCM credentials")
	}
	c.Type = values["type"]
	c.ProjectID = values["project_id"]
	c.TokenURI = values["token_uri"]
	if c.Type != "service_account" || !regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`).MatchString(c.ProjectID) || c.TokenURI != "https://oauth2.googleapis.com/token" {
		return nil, errors.New("FCM requires a Google service account credential")
	}
	jwt, e := google.JWTConfigFromJSON(raw, "https://www.googleapis.com/auth/firebase.messaging")
	if e != nil {
		return nil, errors.New("invalid FCM service account key")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	return &FCMSender{project: c.ProjectID, client: client, tokens: jwt.TokenSource(ctx)}, nil
}
func (s *FCMSender) Send(ctx context.Context, token, hospital string, item contracts.NotificationPushItem, key string) error {
	auth, e := s.tokens.Token()
	if e != nil {
		return &PushError{RetryAfter: time.Minute, Code: "AUTHENTICATION_UNAVAILABLE"}
	}
	sum := sha256.Sum256([]byte(key))
	collapse := hex.EncodeToString(sum[:])
	message := map[string]any{"message": map[string]any{
		"fid": token, "notification": map[string]string{"title": item.Notification.Title, "body": item.Notification.Message},
		"data":    map[string]string{"hospital_code": hospital, "notification_id": item.ID, "service": item.Notification.Target.Service, "target_id": item.Notification.Target.ID, "action": item.Notification.Target.Action},
		"android": map[string]any{"priority": "high", "ttl": "86400s", "notification": map[string]any{"channel_id": "hospital_erp_notifications", "sound": "default", "tag": hospital + "_" + item.ID, "notification_count": item.UnreadCount}},
		"apns":    map[string]any{"headers": map[string]string{"apns-push-type": "alert", "apns-priority": "10", "apns-collapse-id": collapse, "apns-expiration": strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)}, "payload": map[string]any{"aps": map[string]any{"sound": "default", "badge": item.UnreadCount}}},
	}}
	body, _ := json.Marshal(message)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://fcm.googleapis.com/v1/projects/"+s.project+"/messages:send", bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, e := s.client.Do(req)
	if e != nil {
		return &PushError{Code: "NETWORK_UNAVAILABLE"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16384))
		return nil
	}
	var value struct {
		Error struct {
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&value)
	p := &PushError{Code: "HTTP_" + strconv.Itoa(resp.StatusCode)}
	for _, d := range value.Error.Details {
		if d.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && regexp.MustCompile(`^[A-Z_]{1,60}$`).MatchString(d.Code) {
			p.Code = d.Code
		}
		if d.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && d.Code == "UNREGISTERED" {
			p.InvalidToken = true
		}
	}
	if resp.StatusCode == 400 {
		p.Permanent = true
	}
	if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
		p.RetryAfter = time.Duration(min(seconds, 86400)) * time.Second
	} else if date, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
		p.RetryAfter = min(time.Until(date), 24*time.Hour)
	}
	return p
}
