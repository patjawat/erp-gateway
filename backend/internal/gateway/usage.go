package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const usageCohort = "active_employees_v1"

var usageIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)

type usageActivityInput struct {
	EventID        string `json:"event_id"`
	InstallationID string `json:"installation_id"`
	Platform       string `json:"platform"`
	AppVersion     string `json:"app_version"`
	AppBuild       string `json:"app_build"`
}
type usageSnapshot struct {
	SchemaVersion int    `json:"schema_version"`
	HospitalCode  string `json:"hospital_code"`
	Cohort        string `json:"cohort"`
	PeriodDay     string `json:"period_day"`
	Timezone      string `json:"timezone"`
	AsOf          int64  `json:"as_of"`
	CoverageSince int64  `json:"coverage_since"`
	Personnel     int64  `json:"personnel"`
	Eligible      int64  `json:"eligible"`
	EverUsed      int64  `json:"ever_used"`
	ActiveToday   int64  `json:"active_today"`
	Active7D      int64  `json:"active_7d"`
	Active30D     int64  `json:"active_30d"`
	Android30D    int64  `json:"android_30d"`
	IOS30D        int64  `json:"ios_30d"`
	Status        string `json:"status"`
}

func (s *Store) migrateUsage() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS usage_snapshots(hospital_id TEXT PRIMARY KEY REFERENCES hospitals(id),payload TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'unknown',checked_at INTEGER NOT NULL DEFAULT 0,as_of INTEGER NOT NULL DEFAULT 0,period_day TEXT NOT NULL DEFAULT '',personnel INTEGER,eligible INTEGER,ever_used INTEGER,active_today INTEGER,active_7d INTEGER,active_30d INTEGER,android_30d INTEGER,ios_30d INTEGER);
CREATE INDEX IF NOT EXISTS usage_snapshots_freshness ON usage_snapshots(period_day,checked_at,status);`)
	return err
}
func (s *Server) usageActivity(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var in usageActivityInput
	if !decode(w, r, &in) {
		return
	}
	if !usageIDPattern.MatchString(in.EventID) || !usageIDPattern.MatchString(in.InstallationID) || (in.Platform != "android" && in.Platform != "ios") || in.AppVersion == "" || len(in.AppVersion) > 64 || in.AppBuild == "" || len(in.AppBuild) > 32 || strings.ContainsAny(in.AppVersion+in.AppBuild, "\r\n\x00") {
		fail(w, 422, "INVALID_ACTIVITY", "ข้อมูลรุ่นแอปหรือการติดตั้งไม่ถูกต้อง")
		return
	}
	if !s.limit(w, r, "usage:"+h.ID+":"+tokenHash(token), 30, time.Hour) {
		return
	}
	body, _ := json.Marshal(in)
	resp, err := s.upstream(r.Context(), h, http.MethodPost, "/api/usage/activity", token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 {
			resp.Body.Close()
			expired(w)
		} else if resp.StatusCode == 404 {
			resp.Body.Close()
			fail(w, 503, "USAGE_NOT_SUPPORTED", "API หน่วยงานยังไม่รองรับสถิติการใช้งาน")
		} else {
			s.relayProfileError(w, resp)
		}
		return
	}
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if readEnvelope(resp, &ack) != nil || !ack.Accepted {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	writeData(w, ack)
}
func bangkokDay(now time.Time) string {
	return now.In(time.FixedZone("Asia/Bangkok", 7*60*60)).Format(time.DateOnly)
}
func validUsageSnapshot(v usageSnapshot, h Hospital, now time.Time) bool {
	if v.SchemaVersion != 1 || v.HospitalCode != h.Code || v.Cohort != usageCohort || v.Timezone != "Asia/Bangkok" || v.PeriodDay != bangkokDay(now) || v.AsOf > now.Add(time.Minute).Unix() || v.AsOf < now.Add(-45*time.Minute).Unix() || v.CoverageSince <= 0 || v.CoverageSince > v.AsOf || (v.Status != "ready" && v.Status != "partial") {
		return false
	}
	if v.Personnel < 0 || v.Eligible < 0 || v.Eligible > v.Personnel || v.EverUsed < 0 || v.EverUsed > v.Personnel || v.ActiveToday < 0 || v.ActiveToday > v.Active7D || v.Active7D > v.Active30D || v.Active30D > v.EverUsed || v.Android30D < 0 || v.IOS30D < 0 {
		return false
	}
	return true
}
func (s *Server) collectUsage(ctx context.Context, h Hospital) {
	callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := s.upstream(callCtx, h, http.MethodGet, "/api/management/usage/summary", "", nil)
	status := "unavailable"
	now := time.Now()
	if err == nil && resp.StatusCode == http.StatusOK {
		var v usageSnapshot
		if readEnvelope(resp, &v) == nil && validUsageSnapshot(v, h, now) {
			b, _ := json.Marshal(v)
			_, _ = s.config.Store.db.ExecContext(ctx, `INSERT INTO usage_snapshots(hospital_id,payload,status,checked_at,as_of,period_day,personnel,eligible,ever_used,active_today,active_7d,active_30d,android_30d,ios_30d) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(hospital_id) DO UPDATE SET payload=excluded.payload,status=excluded.status,checked_at=excluded.checked_at,as_of=excluded.as_of,period_day=excluded.period_day,personnel=excluded.personnel,eligible=excluded.eligible,ever_used=excluded.ever_used,active_today=excluded.active_today,active_7d=excluded.active_7d,active_30d=excluded.active_30d,android_30d=excluded.android_30d,ios_30d=excluded.ios_30d`, h.ID, string(b), v.Status, now.Unix(), v.AsOf, v.PeriodDay, v.Personnel, v.Eligible, v.EverUsed, v.ActiveToday, v.Active7D, v.Active30D, v.Android30D, v.IOS30D)
			return
		}
		status = "invalid"
	} else if resp != nil {
		if resp.StatusCode == 404 {
			status = "unsupported"
		}
		resp.Body.Close()
	}
	_, _ = s.config.Store.db.ExecContext(ctx, `INSERT INTO usage_snapshots(hospital_id,status,checked_at) VALUES(?,?,?) ON CONFLICT(hospital_id) DO UPDATE SET status=excluded.status,checked_at=excluded.checked_at`, h.ID, status, now.Unix())
}
func (s *Server) collectUsageBatch(ctx context.Context) {
	// Two in-flight sites at most; pages are internal bounded work batches,
	// independent of dashboard navigation and never sent to the browser.
	var last string
	for ctx.Err() == nil {
		rows, err := s.config.Store.db.QueryContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals WHERE enabled=1 AND approval_status='approved' AND code>? ORDER BY code LIMIT 25", last)
		if err != nil {
			return
		}
		list := make([]Hospital, 0, 25)
		for rows.Next() {
			h, e := scanHospital(rows)
			if e != nil {
				rows.Close()
				return
			}
			list = append(list, h)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(list) == 0 {
			return
		}
		var wg sync.WaitGroup
		slots := make(chan struct{}, 2)
		for _, h := range list {
			if !active(h) {
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(h Hospital) { defer wg.Done(); defer func() { <-slots }(); s.collectUsage(ctx, h) }(h)
		}
		wg.Wait()
		last = list[len(list)-1].Code
		if len(list) < 25 {
			return
		}
	}
}
func (s *Server) RunUsageWorker(ctx context.Context) {
	s.collectUsageBatch(ctx)
	// Add a small random start offset without a second unbounded timer per site.
	jitter := time.Duration(time.Now().UnixNano() % int64(time.Minute))
	timer := time.NewTimer(15*time.Minute + jitter)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.collectUsageBatch(ctx)
			timer.Reset(15*time.Minute + jitter)
		case <-s.usageRefresh:
			s.collectUsageBatch(ctx)
		}
	}
}
func (s *Server) adminUsageRefresh(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if !s.limit(w, r, "usage-refresh", 5, time.Hour) {
		return
	}
	select {
	case s.usageRefresh <- struct{}{}:
	default:
	}
	writeData(w, map[string]bool{"queued": true})
}
func (s *Store) usageSummary(ctx context.Context, total int64) (map[string]any, error) {
	now := time.Now()
	out := map[string]any{"status": "not_configured", "personnel": nil, "eligible": nil, "ever_used": nil, "active_today": nil, "active_7d": nil, "active_30d": nil, "adoption_30d": nil, "android_30d": nil, "ios_30d": nil, "as_of": nil, "coverage": map[string]any{"reporting": 0, "total": total}}
	var n, people, eligible, ever, today, week, month, android, ios, asof, partial int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(u.personnel),0),COALESCE(SUM(u.eligible),0),COALESCE(SUM(u.ever_used),0),COALESCE(SUM(u.active_today),0),COALESCE(SUM(u.active_7d),0),COALESCE(SUM(u.active_30d),0),COALESCE(SUM(u.android_30d),0),COALESCE(SUM(u.ios_30d),0),COALESCE(MIN(u.as_of),0),COALESCE(SUM(u.status='partial'),0) FROM usage_snapshots u JOIN hospitals h ON h.id=u.hospital_id WHERE u.status IN ('ready','partial') AND u.period_day=? AND u.as_of>=?`, bangkokDay(now), now.Add(-45*time.Minute).Unix()).Scan(&n, &people, &eligible, &ever, &today, &week, &month, &android, &ios, &asof, &partial)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		var known int64
		if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_snapshots WHERE payload!=''").Scan(&known); err != nil {
			return nil, err
		}
		if known > 0 {
			out["status"] = "stale"
		}
		return out, nil
	}
	status := "ready"
	if n < total || partial > 0 {
		status = "partial"
	}
	out["status"] = status
	out["personnel"] = people
	out["eligible"] = eligible
	out["ever_used"] = ever
	out["active_today"] = today
	out["active_7d"] = week
	out["active_30d"] = month
	out["android_30d"] = android
	out["ios_30d"] = ios
	out["as_of"] = asof
	out["coverage"] = map[string]int64{"reporting": n, "total": total}
	if people > 0 {
		out["adoption_30d"] = float64(month) * 100 / float64(people)
	}
	return out, nil
}
