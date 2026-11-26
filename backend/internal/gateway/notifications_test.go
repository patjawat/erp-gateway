package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
	"golang.org/x/oauth2"
)

func notifyGatewayFixture(t *testing.T) (*Store, []Hospital, []string) {
	t.Helper()
	ctx := context.Background()
	store, e := OpenStore(filepath.Join(t.TempDir(), "gateway.db"), bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	hospitals := []Hospital{}
	sessions := []string{}
	for _, code := range []string{"11447", "12448"} {
		h, e := store.addHospital(ctx, Hospital{Hospital: contracts.Hospital{Code: code, Name: "Fixture"}, APIURL: "https://hospital.test"}, strings.Repeat("k", 32))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.db.Exec("UPDATE hospitals SET enabled=1,approval_status='approved' WHERE id=?", h.ID); e != nil {
			t.Fatal(e)
		}
		h, e = store.hospital(ctx, "id", h.ID)
		if e != nil {
			t.Fatal(e)
		}
		session, e := store.createSession(ctx, h, contracts.Token{UserID: json.RawMessage(`42`), AccessToken: "local-test-token", ExpiresAt: time.Now().Add(time.Hour).Unix()})
		if e != nil {
			t.Fatal(e)
		}
		hospitals = append(hospitals, h)
		sessions = append(sessions, session)
	}
	return store, hospitals, sessions
}
func notifyPushFixture() contracts.NotificationPushItem {
	return contracts.NotificationPushItem{ID: "1", UserID: "42", UnreadCount: 3, Notification: contracts.Notification{ID: "1", Kind: "leave_approval", Title: "มีใบลารอคุณพิจารณา", Message: "เปิดแอปเพื่อตรวจสอบ", CreatedAt: time.Now().Format(time.RFC3339), Target: contracts.NotificationTarget{Service: "leave", ID: "10", Action: "approval"}}}
}
func TestNotificationQueueTenantIsolationReplayAndLogout(t *testing.T) {
	s, hospitals, sessions := notifyGatewayFixture(t)
	ctx := context.Background()
	in := contracts.NotificationDeviceInput{InstallationID: "installation_0001", FID: "abcdefghijklmnopqrstuv", Platform: "android"}
	if e := s.registerNotificationDevice(ctx, hospitals[0], sessions[0], in); e != nil {
		t.Fatal(e)
	}
	batch := contracts.NotificationPushBatch{Items: []contracts.NotificationPushItem{notifyPushFixture()}}
	for _, h := range hospitals {
		if e := s.ingestNotificationBatch(ctx, h, batch); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.ingestNotificationBatch(ctx, hospitals[0], batch); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.nextNotificationJobs(ctx)
	if e != nil || len(jobs) != 1 || jobs[0].hospital != hospitals[0].ID {
		t.Fatalf("tenant/replay: %+v %v", jobs, e)
	}
	j := jobs[0]
	ok, e := s.notificationJobToken(ctx, &j)
	if e != nil || !ok || j.token != in.FID {
		t.Fatal("device lookup", e)
	}
	var encrypted []byte
	if e = s.db.QueryRow("SELECT token FROM notify_devices").Scan(&encrypted); e != nil || bytes.Contains(encrypted, []byte(in.FID)) {
		t.Fatal("device identifier stored in plaintext")
	}
	if e = s.revoke(ctx, sessions[0]); e != nil {
		t.Fatal(e)
	}
	ok, e = s.notificationJobToken(ctx, &j)
	if e != nil || ok {
		t.Fatal("logged-out device still receives push")
	}
}
func TestNotificationDeviceRebindCancelsOldTenantJobs(t *testing.T) {
	s, h, sessions := notifyGatewayFixture(t)
	ctx := context.Background()
	in := contracts.NotificationDeviceInput{InstallationID: "installation_0001", FID: "abcdefghijklmnopqrstuv", Platform: "ios"}
	if e := s.registerNotificationDevice(ctx, h[0], sessions[0], in); e != nil {
		t.Fatal(e)
	}
	if e := s.ingestNotificationBatch(ctx, h[0], contracts.NotificationPushBatch{Items: []contracts.NotificationPushItem{notifyPushFixture()}}); e != nil {
		t.Fatal(e)
	}
	if e := s.registerNotificationDevice(ctx, h[1], sessions[1], in); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.nextNotificationJobs(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatal(e)
	}
	ok, e := s.notificationJobToken(ctx, &jobs[0])
	if e != nil || ok {
		t.Fatal("device switch did not isolate old tenant")
	}
}
func TestFCMUsesFIDSoundBadgeAndGenericPayload(t *testing.T) {
	var body map[string]any
	sender := &FCMSender{project: "test-project", tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake-test-auth"}), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "fcm.googleapis.com" || r.Header.Get("Authorization") != "Bearer fake-test-auth" {
			t.Fatal("unexpected FCM route")
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"name":"test-message"}`))}, nil
	})}}
	if e := sender.Send(context.Background(), "abcdefghijklmnopqrstuv", "11447", notifyPushFixture(), "stable-job"); e != nil {
		t.Fatal(e)
	}
	message := body["message"].(map[string]any)
	if message["fid"] != "abcdefghijklmnopqrstuv" || message["token"] != nil {
		t.Fatal("must use current FID API")
	}
	data := message["data"].(map[string]any)
	if data["hospital_code"] != "11447" || data["notification_id"] != "1" || data["reason"] != nil {
		t.Fatal("unsafe or missing routing metadata")
	}
	aps := message["apns"].(map[string]any)["payload"].(map[string]any)["aps"].(map[string]any)
	if aps["sound"] != "default" || aps["badge"] != float64(3) {
		t.Fatal("missing sound/badge")
	}
}
func TestFCMRetryAfterAndInvalidRegistration(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		invalid bool
	}{{429, `{}`, false}, {404, `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, true}, {400, `{"error":{"status":"INVALID_ARGUMENT"}}`, false}} {
		sender := &FCMSender{project: "test-project", tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"}), client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		e := sender.Send(context.Background(), "abcdefghijklmnopqrstuv", "11447", notifyPushFixture(), "job")
		p, ok := e.(*PushError)
		if !ok || p.InvalidToken != tc.invalid || p.RetryAfter != 2*time.Minute {
			t.Fatalf("status %d: %+v", tc.status, e)
		}
		if tc.status == 400 && !p.Permanent {
			t.Fatal("bad payload must not retry indefinitely")
		}
	}
}

type fixturePushSender struct {
	calls int
	item  contracts.NotificationPushItem
	err   error
}

func (s *fixturePushSender) Send(_ context.Context, _, _ string, item contracts.NotificationPushItem, _ string) error {
	s.calls++
	s.item = item
	return s.err
}
func TestPushDeliveryRechecksLocalAndUsesLatestUnreadCount(t *testing.T) {
	store, h, sessions := notifyGatewayFixture(t)
	ctx := context.Background()
	in := contracts.NotificationDeviceInput{InstallationID: "installation_0001", FID: "abcdefghijklmnopqrstuv", Platform: "ios"}
	if e := store.registerNotificationDevice(ctx, h[0], sessions[0], in); e != nil {
		t.Fatal(e)
	}
	item := notifyPushFixture()
	if e := store.ingestNotificationBatch(ctx, h[0], contracts.NotificationPushBatch{Items: []contracts.NotificationPushItem{item}}); e != nil {
		t.Fatal(e)
	}
	sender := &fixturePushSender{}
	app, err := New(Config{Store: store, AdminUsername: "fixtures", AdminPassword: "isolated fixture password", NotificationSender: sender, ApprovedUpstreams: []string{h[0].Code + "=" + h[0].APIURL}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/notification-outbox/1" || r.Header.Get("X-Gateway-Key") == "" || r.Header.Get("Authorization") != "Bearer local-test-token" {
			t.Fatal("missing delivery authorization")
		}
		current := item
		current.UnreadCount = 1
		body, _ := json.Marshal(map[string]any{"data": current})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.nextNotificationJobs(ctx)
	app.deliverNotification(ctx, jobs[0])
	if sender.calls != 1 || sender.item.UnreadCount != 1 {
		t.Fatal("did not revalidate latest state")
	}
	var status string
	store.db.QueryRow("SELECT status FROM notify_jobs").Scan(&status)
	if status != "accepted" {
		t.Fatal("FCM acceptance is not a delivery/read receipt", status)
	}
}
