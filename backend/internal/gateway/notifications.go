package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"erp-platform/gateway/internal/contracts"
)

// Push delivery is a Gateway connection capability, separate from the ERP's
// opaque notification history/read-state/summary business responses.
func (s *Server) notificationDeviceCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authorize(w, r); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "คำขอไม่ถูกต้อง")
		return
	}
	writeData(w, map[string]bool{"push_enabled": s.config.NotificationSender != nil})
}

func (s *Server) notificationHTTP(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.Path == "/api/notification-devices/register" || r.URL.Path == "/api/notification-devices/remove" {
		if r.Method != http.MethodPost || r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST", "คำขอไม่ถูกต้อง")
			return
		}
		var in contracts.NotificationDeviceInput
		if !decodeNotificationInput(w, r, &in) {
			return
		}
		if !contracts.ValidInstallationID(in.InstallationID) {
			fail(w, 400, "INVALID_REQUEST", "รหัสอุปกรณ์ไม่ถูกต้อง")
			return
		}
		centralToken := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))[1]
		if !s.limit(w, r, "notify-device:"+tokenHash(centralToken), 30, time.Minute) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/remove") {
			if e := s.config.Store.removeNotificationDevice(r.Context(), centralToken, in.InstallationID); e != nil {
				unavailable(w)
				return
			}
			writeData(w, map[string]bool{"removed": true})
			return
		}
		if s.config.NotificationSender == nil {
			fail(w, 503, "PUSH_UNAVAILABLE", "ส่วนกลางยังไม่เปิด Push notifications")
			return
		}
		if len(in.FID) != 22 || !contracts.ValidInstallationID(in.FID) || (in.Platform != "ios" && in.Platform != "android") {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลอุปกรณ์ไม่ถูกต้อง")
			return
		}
		// Resolve the current Local identity as well as the central session.
		resp, e := s.upstream(r.Context(), h, http.MethodGet, "/api/profile", token, nil)
		if e != nil {
			upstreamUnavailable(w)
			return
		}
		if resp.StatusCode != 200 {
			s.relayError(w, resp, false)
			return
		}
		// Only authenticated identity is needed by the Gateway device protocol;
		// do not couple FCM registration to the ERP business profile schema.
		var profile struct {
			ID json.RawMessage `json:"id"`
		}
		if readEnvelope(resp, &profile) != nil {
			upstreamUnavailable(w)
			return
		}
		session, e := s.config.Store.session(r.Context(), centralToken)
		if e != nil || identity(profile.ID) != identity(json.RawMessage(session.userID)) {
			expired(w)
			return
		}
		if !s.stillActive(w, r, h) {
			return
		}
		if e = s.config.Store.registerNotificationDevice(r.Context(), h, centralToken, in); e != nil {
			unavailable(w)
			return
		}
		writeData(w, map[string]bool{"registered": true})
		return
	}
	if r.URL.Path == "/api/notifications" {
		if r.Method != http.MethodGet {
			fail(w, 405, "METHOD_NOT_ALLOWED", "วิธีเรียกไม่ถูกต้อง")
			return
		}
		if _, e := contracts.ParseNotificationSelection(r.URL.RawQuery); e != nil {
			fail(w, 400, "INVALID_REQUEST", "ตัวเลือกไม่ถูกต้อง")
			return
		}
	}
	resp, e := s.upstream(r.Context(), h, r.Method, r.URL.RequestURI(), token, notificationRequestBody(w, r))
	if e != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayError(w, resp, false)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/notifications/") && r.URL.Path != "/api/notifications/summary" {
		if strings.HasSuffix(r.URL.Path, "/leave") {
			var v contracts.NotificationLeaveDetail
			if readEnvelope(resp, &v) != nil || !contracts.ValidNotificationID(v.ID) || v.TotalDays < 0 {
				upstreamUnavailable(w)
				return
			}
			if !s.stillActive(w, r, h) {
				return
			}
			writeData(w, v)
			return
		}
		var v contracts.Notification
		if readEnvelope(resp, &v) != nil || !contracts.ValidNotification(v) {
			upstreamUnavailable(w)
			return
		}
		if !s.stillActive(w, r, h) {
			return
		}
		writeData(w, v)
		return
	}
	if r.URL.Path == "/api/notifications" {
		var page contracts.NotificationPage
		if e = readEnvelope(resp, &page); e != nil || page.Items == nil || len(page.Items) > contracts.NotificationPageSize || page.UnreadCount < 0 || page.HasMore != (page.NextCursor != "") {
			upstreamUnavailable(w)
			return
		}
		for _, item := range page.Items {
			if !contracts.ValidNotification(item) {
				upstreamUnavailable(w)
				return
			}
		}
		if !s.stillActive(w, r, h) {
			return
		}
		writeData(w, page)
		return
	}
	var summary contracts.NotificationSummary
	if e = readEnvelope(resp, &summary); e != nil || summary.UnreadCount < 0 {
		upstreamUnavailable(w)
		return
	}
	summary.PushEnabled = summary.Enabled && s.config.NotificationSender != nil
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, summary)
}

func (s *Server) adminNotificationSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกไม่ถูกต้อง")
		return
	}
	rows, e := s.config.Store.db.QueryContext(r.Context(), "SELECT status,COUNT(*),MIN(created_at) FROM notify_jobs GROUP BY status")
	if e != nil {
		unavailable(w)
		return
	}
	counts := map[string]int{}
	oldest := int64(0)
	for rows.Next() {
		var status string
		var n int
		var created int64
		if e = rows.Scan(&status, &n, &created); e != nil {
			break
		}
		counts[status] = n
		if status == "pending" {
			oldest = created
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		unavailable(w)
		return
	}
	age := int64(0)
	if oldest > 0 {
		age = max(int64(0), time.Now().Unix()-oldest)
	}
	writeData(w, map[string]any{"provider_configured": s.config.NotificationSender != nil, "jobs": counts, "oldest_pending_seconds": age})
}
func notificationRequestBody(w http.ResponseWriter, r *http.Request) []byte {
	if r.Method != http.MethodPost {
		return nil
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if e != nil {
		return []byte("invalid")
	}
	return raw
}
func decodeNotificationInput(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "JSON_REQUIRED", "กรุณาส่ง JSON")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "INVALID_REQUEST", "ข้อมูลไม่ถูกต้อง")
		return false
	}
	return true
}

// RunNotificationWorker is called once per central Gateway process. The
// existing SQLite deployment supports one writer instance, not replicas.
func (s *Server) RunNotificationWorker(ctx context.Context) {
	if s.config.NotificationSender == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	after := ""
	for {
		hospitals, e := s.config.Store.notificationHospitals(ctx, after)
		if e == nil {
			if len(hospitals) < 5 {
				after = ""
			}
			for _, h := range hospitals {
				after = h.ID
				work, cancel := context.WithTimeout(ctx, 8*time.Second)
				e = s.transferNotifications(work, h)
				cancel()
				if e != nil && ctx.Err() == nil {
					log.Printf("notification transfer pending for hospital %s", h.Code)
				}
			}
			if len(hospitals) < 5 {
				after = ""
			}
		}
		jobs, e := s.config.Store.nextNotificationJobs(ctx)
		if e == nil {
			var workers sync.WaitGroup
			slots := make(chan struct{}, 4)
			for _, j := range jobs {
				if ctx.Err() != nil {
					break
				}
				slots <- struct{}{}
				workers.Add(1)
				go func(j notificationJob) {
					defer workers.Done()
					defer func() { <-slots }()
					s.deliverNotification(ctx, j)
				}(j)
			}
			workers.Wait()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) transferNotifications(ctx context.Context, h Hospital) error {
	resp, e := s.upstream(ctx, h, http.MethodGet, "/api/notification-outbox", "", nil)
	if e != nil {
		return e
	}
	var batch contracts.NotificationPushBatch
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil
	}
	if e = readEnvelope(resp, &batch); e != nil {
		return e
	}
	if !validPushBatch(batch) {
		return io.ErrUnexpectedEOF
	}
	if len(batch.Items) == 0 {
		return nil
	}
	current, e := s.config.Store.hospital(ctx, "id", h.ID)
	if e != nil || !active(current) {
		return e
	}
	if e = s.config.Store.ingestNotificationBatch(ctx, h, batch); e != nil {
		return e
	}
	ack := contracts.NotificationAck{IDs: []string{}}
	for _, item := range batch.Items {
		ack.IDs = append(ack.IDs, item.ID)
	}
	raw, _ := json.Marshal(ack)
	resp, e = s.upstream(ctx, h, http.MethodPost, "/api/notification-outbox/ack", "", raw)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (s *Server) deliverNotification(ctx context.Context, j notificationJob) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store := s.config.Store
	finish := func(status string, delay time.Duration) {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer finishCancel()
		if e := store.finishNotificationJob(finishCtx, j, status, delay); e != nil && ctx.Err() == nil {
			log.Print("notification queue update unavailable")
		}
	}
	h, e := store.hospital(ctx, "id", j.hospital)
	if e != nil {
		finish("pending", time.Minute)
		return
	}
	if !active(h) || time.Now().Unix()-j.created > 24*3600 {
		finish("cancelled", 0)
		return
	}
	ok, e := store.notificationJobToken(ctx, &j)
	if e != nil {
		finish("pending", time.Minute)
		return
	}
	if !ok {
		finish("cancelled", 0)
		return
	}
	// Check the current Local recipient/target immediately before an external
	// send. A deleted assignment, read alert or inactive account cancels the job.
	resp, e := s.upstream(ctx, h, http.MethodGet, "/api/notification-outbox/"+j.event, j.localToken, nil)
	if e != nil {
		finish("pending", time.Minute)
		return
	}
	if resp.StatusCode == 404 || resp.StatusCode == 401 || resp.StatusCode == 403 {
		resp.Body.Close()
		finish("cancelled", 0)
		return
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		finish("pending", time.Minute)
		return
	}
	var current contracts.NotificationPushItem
	if readEnvelope(resp, &current) != nil || current.UserID != j.payload.UserID || current.ID != j.event {
		finish("pending", time.Minute)
		return
	}
	if current.Notification.IsRead {
		finish("cancelled", 0)
		return
	}
	j.payload = current
	e = s.config.NotificationSender.Send(ctx, j.token, h.Code, j.payload, notificationJobKey(j))
	if e == nil {
		finish("accepted", 0)
		return
	}
	if p, ok := e.(*PushError); ok {
		log.Printf("notification_push_failed hospital=%s code=%s attempt=%d", h.Code, p.Code, j.attempts+1)
		if p.InvalidToken {
			_, _ = store.db.ExecContext(ctx, "UPDATE notify_devices SET enabled=0 WHERE installation=? AND token_hash=?", j.installation, j.tokenHash)
			finish("invalid_token", 0)
			return
		}
		if p.Permanent {
			finish("failed", 0)
			return
		}
	}
	if j.attempts >= 9 {
		finish("failed", 0)
		return
	}
	delay := time.Minute*time.Duration(1<<min(j.attempts, 6)) + time.Duration(j.id%17)*time.Second
	if p, ok := e.(*PushError); ok && p.RetryAfter > delay {
		delay = p.RetryAfter
	}
	finish("pending", delay)
}
