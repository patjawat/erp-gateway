package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"erp-platform/gateway/internal/backupcloud"
)

type backupSchedule struct {
	Kind            string `json:"kind"`
	IntervalMinutes int    `json:"interval_minutes"`
	Time            string `json:"time"`
	Weekdays        []int  `json:"weekdays"`
	Timezone        string `json:"timezone"`
}
type backupRetention struct {
	Latest  int  `json:"latest"`
	Daily   int  `json:"daily"`
	Weekly  int  `json:"weekly"`
	Monthly int  `json:"monthly"`
	KeepAll bool `json:"keep_all"`
}
type BackupPolicy struct {
	Version        int             `json:"version"`
	Revision       int             `json:"revision"`
	Enabled        bool            `json:"enabled"`
	Schedule       backupSchedule  `json:"schedule"`
	Destination    string          `json:"destination"`
	LocalRetention backupRetention `json:"local_retention"`
	DriveRetention backupRetention `json:"drive_retention"`
	AlertsEnabled  bool            `json:"alerts_enabled"`
	StaleMinutes   int             `json:"stale_minutes"`
}

func defaultBackupPolicy() BackupPolicy {
	return BackupPolicy{Version: 1, Revision: 1, Schedule: backupSchedule{Kind: "interval", IntervalMinutes: 15, Time: "02:00", Timezone: "Asia/Bangkok", Weekdays: []int{0}}, Destination: "local", LocalRetention: backupRetention{Latest: 3}, DriveRetention: backupRetention{Latest: 24, Daily: 7, Weekly: 4, Monthly: 3}, AlertsEnabled: true, StaleMinutes: 45}
}
func (p BackupPolicy) validate() error {
	if p.Version != 1 || p.Revision < 1 {
		return errors.New("รูปแบบการตั้งค่าสำรองไม่รองรับ")
	}
	if p.Destination != "local" && p.Destination != "drive" {
		return errors.New("เลือกเก็บในเครื่องหรือ Google Drive")
	}
	if _, e := time.LoadLocation(p.Schedule.Timezone); e != nil {
		return errors.New("เขตเวลาไม่ถูกต้อง")
	}
	switch p.Schedule.Kind {
	case "interval":
		if p.Schedule.IntervalMinutes < 1 || p.Schedule.IntervalMinutes > 10080 {
			return errors.New("กำหนดรอบสำรอง 1 ถึง 10080 นาที")
		}
	case "daily", "weekly":
		if len(p.Schedule.Time) != 5 {
			return errors.New("ระบุเวลา HH:mm")
		}
		if _, e := time.Parse("15:04", p.Schedule.Time); e != nil {
			return errors.New("ระบุเวลา HH:mm")
		}
		if p.Schedule.Kind == "weekly" {
			if len(p.Schedule.Weekdays) < 1 || len(p.Schedule.Weekdays) > 7 {
				return errors.New("เลือกวันสำรอง")
			}
			seen := map[int]bool{}
			for _, d := range p.Schedule.Weekdays {
				if d < 0 || d > 6 || seen[d] {
					return errors.New("วันสำรองไม่ถูกต้อง")
				}
				seen[d] = true
			}
		}
	default:
		return errors.New("รูปแบบตารางสำรองไม่ถูกต้อง")
	}
	for _, r := range []backupRetention{p.LocalRetention, p.DriveRetention} {
		if r.Latest < 0 || r.Daily < 0 || r.Weekly < 0 || r.Monthly < 0 || r.Latest > 10000 || r.Daily > 3660 || r.Weekly > 520 || r.Monthly > 120 {
			return errors.New("จำนวนชุดสำรองเกินขอบเขต")
		}
		if !r.KeepAll && r.Latest+r.Daily+r.Weekly+r.Monthly < 1 {
			return errors.New("ต้องเก็บอย่างน้อยหนึ่งชุดที่ตรวจสอบแล้ว")
		}
	}
	if p.StaleMinutes < 1 || p.StaleMinutes > 525600 {
		return errors.New("ช่วงแจ้งเตือนเกินขอบเขต")
	}
	if p.StaleMinutes < p.scheduleGapMinutes() {
		return errors.New("ช่วงแจ้งเตือนต้องไม่น้อยกว่าช่วงห่างสูงสุดของรอบสำรอง")
	}
	return nil
}
func (p BackupPolicy) scheduleGapMinutes() int {
	switch p.Schedule.Kind {
	case "interval":
		return p.Schedule.IntervalMinutes
	case "daily":
		return 1440
	case "weekly":
		selected := map[int]bool{}
		for _, day := range p.Schedule.Weekdays {
			selected[day] = true
		}
		maxGap := 0
		for day := range selected {
			gap := 1
			for gap < 7 && !selected[(day+gap)%7] {
				gap++
			}
			if gap > maxGap {
				maxGap = gap
			}
		}
		return maxGap * 1440
	}
	return 0
}
func (p BackupPolicy) next(after time.Time) time.Time {
	if p.Schedule.Kind == "interval" {
		return after.Add(time.Duration(p.Schedule.IntervalMinutes) * time.Minute)
	}
	loc, e := time.LoadLocation(p.Schedule.Timezone)
	if e != nil {
		return time.Time{}
	}
	at, e := time.Parse("15:04", p.Schedule.Time)
	if e != nil {
		return time.Time{}
	}
	now := after.In(loc)
	for i := 0; i < 9; i++ {
		day := now.AddDate(0, 0, i)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), 0, 0, loc)
		if !candidate.After(after) {
			continue
		}
		if p.Schedule.Kind == "daily" {
			return candidate
		}
		for _, d := range p.Schedule.Weekdays {
			if int(candidate.Weekday()) == d {
				return candidate
			}
		}
	}
	return time.Time{}
}
func (s *backupService) applyPolicy(p BackupPolicy) error {
	s.mu.Lock()
	previous := s.state.Policy
	s.mu.Unlock()
	if p.StaleMinutes == previous.StaleMinutes && previous.StaleMinutes == 3*previous.scheduleGapMinutes() {
		p.StaleMinutes = 3 * p.scheduleGapMinutes()
	}
	if e := p.validate(); e != nil {
		return e
	}
	if !s.policyMu.TryLock() {
		return errors.New("กำลังจัดเก็บหรือตรวจไฟล์สำรอง กรุณาลองบันทึกอีกครั้งเมื่อเสร็จ")
	}
	defer s.policyMu.Unlock()

	if s.m != nil {
		s.m.mu.Lock()
		fenced := s.m.fenced
		s.m.mu.Unlock()
		if fenced {
			return errors.New("พักการตั้งค่าระหว่างย้ายเครื่อง")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Revision != s.state.Policy.Revision {
		return errors.New("การตั้งค่าเปลี่ยนแล้ว กรุณาโหลดใหม่")
	}
	if p.Destination == "drive" && s.state.Drive.RefreshToken == "" {
		return errors.New("เชื่อม Google Drive ก่อนเปิดการสำรองนอกเครื่อง")
	}
	old := s.state
	p.Revision++
	s.state.Policy = p
	s.state.NextRun = p.next(time.Now())
	if e := s.saveLocked(); e != nil {
		s.state = old
		return e
	}
	return nil
}
func (s *backupService) scheduler() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-t.C:
			s.m.mu.Lock()
			paused := s.m.fenced || s.m.current == nil
			s.m.mu.Unlock()
			s.mu.Lock()
			p := s.state.Policy
			due := s.state.NextRun
			busy := s.active != nil
			if p.Enabled && !paused && !busy && due.IsZero() {
				s.state.NextRun = p.next(now)
				s.saveLocked()
				s.mu.Unlock()
				continue
			}
			if !p.Enabled || paused || busy || due.After(now) || due.IsZero() {
				s.mu.Unlock()
				continue
			}
			s.state.NextRun = p.next(now)
			s.saveLocked()
			s.mu.Unlock()
			s.startJob("capturing", func(ctx context.Context, j *backupJob) error {
				r, e := s.capture(ctx, "", false)
				if e != nil {
					return e
				}
				j.BackupID = r.ID
				s.publishBackup(r.ID)
				return s.protect(ctx, r)
			})
		}
	}
}
func (s *backupService) protect(ctx context.Context, r backupRecord) (resultErr error) {
	captureDirectory := filepath.Join(s.recordPath(r.ID), "capture")
	// Retention, upload and credential persistence may all fail. None may leave
	// the unencrypted captured keys/credentials behind on that error path.
	defer func() {
		if err := os.RemoveAll(captureDirectory); err != nil {
			resultErr = errors.Join(resultErr, errors.New("ลบพื้นที่ชั่วคราวของไฟล์สำรองไม่สำเร็จ กรุณาตรวจพื้นที่จัดเก็บ"))
		}
	}()
	s.mu.Lock()
	p, original := s.state.Policy, s.state.Drive
	dataset, secret := s.state.Dataset, s.state.Secret
	s.mu.Unlock()
	creds := original
	var cloudErr error
	if p.Destination == "drive" {
		s.phase("uploading")
		creds, cloudErr = backupcloud.Refresh(ctx, creds)
		var remoteSnapshot string
		if cloudErr == nil {
			var prepared *backupcloud.Prepared
			prepared, cloudErr = backupcloud.Prepare(ctx, creds, s.dir)
			if cloudErr == nil {
				repo, clean, err := s.repository(prepared.Repository+"/"+dataset, secret)
				if err == nil {
					repo.Env = append(repo.Env, prepared.Env...)
					if _, openErr := repo.ID(ctx); openErr != nil {
						err = repo.Init(ctx)
					}
					if err == nil {
						remoteSnapshot, err = repo.Backup(ctx, captureDirectory)
					}
					if err == nil {
						err = s.verifyRemoteBackup(ctx, repo, remoteSnapshot, dataset, cloudScope(original, dataset))
					}
					clean()
				}
				cloudErr = err
				// A failed upload can still have rotated Google's refresh token.
				if refreshed, err := prepared.Credentials(); err == nil {
					creds = refreshed
				} else {
					cloudErr = errors.Join(cloudErr, err)
				}
				if err := prepared.Close(); err != nil {
					cloudErr = errors.Join(cloudErr, errors.New("ลบข้อมูลเชื่อมต่อ Google Drive ชั่วคราวไม่สำเร็จ"))
				}
			}
		}
		s.mu.Lock()
		if s.state.Drive == original {
			s.state.Drive = creds
			if err := s.saveLocked(); err != nil {
				cloudErr = errors.Join(cloudErr, errors.New("บันทึกข้อมูลเชื่อมต่อ Google Drive ที่ต่ออายุแล้วไม่สำเร็จ"))
			}
		} else {
			// Do not silently reconnect a destination the owner disconnected or
			// overwrite a newly authorized account with this operation's token.
			cloudErr = errors.Join(cloudErr, errors.New("การเชื่อมต่อ Google Drive เปลี่ยนระหว่างสำรอง กรุณาลองใหม่"))
		}
		s.mu.Unlock()
		if cloudErr == nil {
			now := time.Now().UTC()
			r.OffsiteAt, r.RemoteSnapshotID = &now, remoteSnapshot
			r.RemoteScope = cloudScope(original, dataset)
			cloudErr = s.putRecord(r)
		}
		if cloudErr != nil {
			r.Error = "สำรองในเครื่องสำเร็จ แต่ส่ง Google Drive ไม่สำเร็จ กรุณาตรวจการเชื่อมต่อและพื้นที่"
			if err := s.putRecord(r); err != nil {
				return errors.Join(errors.New(r.Error), err)
			}
		}
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	s.mu.Lock()
	current, connection := s.state.Policy, s.state.Drive
	s.mu.Unlock()
	if err := s.retainLocal(ctx, current.LocalRetention); err != nil {
		return err
	}
	if p.Destination == "drive" && current.Destination == "drive" && cloudErr == nil && connection == creds {
		if err := s.retainDrive(ctx, current.DriveRetention); err != nil {
			return err
		}
	}
	if cloudErr != nil {
		return errors.New(r.Error)
	}
	return nil
}
func retained(r backupRecord, rank int, policy backupRetention, buckets map[string]int, seen map[string]bool) bool {
	at := r.CreatedAt.UTC()
	y, w := at.ISOWeek()
	keys := []string{"d:" + at.Format("2006-01-02"), fmt.Sprintf("w:%d-%d", y, w), "m:" + at.Format("2006-01")}
	limits := []int{policy.Daily, policy.Weekly, policy.Monthly}
	keep := false
	for i, key := range keys {
		kind := key[:1]
		if !seen[key] {
			seen[key] = true
			buckets[kind]++
			if buckets[kind] <= limits[i] {
				keep = true
			}
		}
	}
	return policy.KeepAll || r.Pinned || rank == 0 || rank < policy.Latest || keep
}
func (s *backupService) eachRecord(ctx context.Context, fn func(backupRecord, int) error) error {
	var before int64 = 1 << 62
	rank := 0
	for {
		rows, e := s.db.QueryContext(ctx, "SELECT seq,record FROM backups WHERE seq<? ORDER BY seq DESC LIMIT 100", before)
		if e != nil {
			return e
		}
		type entry struct {
			seq int64
			r   backupRecord
		}
		page := []entry{}
		for rows.Next() {
			var x entry
			var b string
			if e = rows.Scan(&x.seq, &b); e == nil {
				e = json.Unmarshal([]byte(b), &x.r)
			}
			if e != nil {
				rows.Close()
				return e
			}
			page = append(page, x)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(page) == 0 {
			return nil
		}
		for _, x := range page {
			before = x.seq
			if e = fn(x.r, rank); e != nil {
				return e
			}
			rank++
		}
	}
}
func (s *backupService) retainLocal(ctx context.Context, p backupRetention) error {
	if p.KeepAll {
		return nil
	}
	buckets := map[string]int{}
	seen := map[string]bool{}
	rank := 0
	return s.eachRecord(ctx, func(r backupRecord, _ int) error {
		if !r.LocalAvailable {
			return nil
		}
		s.mu.Lock()
		leased := s.downloads[r.ID] > 0
		s.mu.Unlock()
		if leased {
			return nil
		}
		keep := retained(r, rank, p, buckets, seen)
		rank++
		if keep {
			return nil
		}
		if e := os.RemoveAll(s.recordPath(r.ID)); e != nil {
			return e
		}
		r.LocalAvailable = false
		return s.putRecord(r)
	})
}
func (s *backupService) retainDrive(ctx context.Context, p backupRetention) (resultErr error) {
	s.mu.Lock()
	original := s.state.Drive
	secret, dataset := s.state.Secret, s.state.Dataset
	s.mu.Unlock()
	scope := cloudScope(original, dataset)
	var catalogued bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM backups WHERE json_extract(record,'$.offsite_at') IS NOT NULL AND json_extract(record,'$.remote_scope')=? AND coalesce(json_extract(record,'$.remote_snapshot_id'),'')<>'')", scope).Scan(&catalogued); err != nil {
		return err
	}
	if !catalogued {
		return nil
	}
	credentials, err := backupcloud.Refresh(ctx, original)
	if err != nil {
		return err
	}
	var prepared *backupcloud.Prepared
	defer func() {
		if prepared != nil {
			if refreshed, err := prepared.Credentials(); err == nil {
				credentials = refreshed
			} else {
				resultErr = errors.Join(resultErr, err)
			}
			if err := prepared.Close(); err != nil {
				resultErr = errors.Join(resultErr, errors.New("ลบข้อมูลเชื่อมต่อ Google Drive ชั่วคราวไม่สำเร็จ"))
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.state.Drive == original {
			s.state.Drive = credentials
			if err := s.saveLocked(); err != nil {
				resultErr = errors.Join(resultErr, errors.New("บันทึกข้อมูลเชื่อมต่อ Google Drive ที่ต่ออายุแล้วไม่สำเร็จ"))
			}
		} else {
			resultErr = errors.Join(resultErr, errors.New("การเชื่อมต่อ Google Drive เปลี่ยนระหว่างจัดเก็บชุดสำรอง"))
		}
	}()
	prepared, err = backupcloud.Prepare(ctx, credentials, s.dir)
	if err != nil {
		return err
	}
	repo, clean, err := s.repository(prepared.Repository+"/"+dataset, secret)
	if err != nil {
		return err
	}
	defer clean()
	repo.Env = append(repo.Env, prepared.Env...)
	snapshots, err := repo.Snapshots(ctx)
	if err != nil {
		return err
	}
	actual := make(map[string]bool, len(snapshots))
	for _, snapshot := range snapshots {
		actual[snapshot.ID] = true
	}
	buckets, seen := map[string]int{}, map[string]bool{}
	known, keepSet := map[string]bool{}, map[string]bool{}
	rank := 0
	if err = s.eachRecord(ctx, func(r backupRecord, _ int) error {
		if r.OffsiteAt == nil || r.RemoteSnapshotID == "" || r.RemoteScope != scope {
			return nil
		}
		id := r.RemoteSnapshotID
		if !actual[id] {
			// Preserve the historical upload time, but never advertise a
			// vanished object or let it consume a live retention slot.
			r.RemoteSnapshotID = ""
			return s.putRecord(r)
		}
		if known[id] {
			if r.Pinned {
				keepSet[id] = true
			}
			return nil
		}
		known[id] = true
		if retained(r, rank, p, buckets, seen) {
			keepSet[id] = true
		}
		rank++
		return nil
	}); err != nil {
		return err
	}
	keep := make([]string, 0, len(snapshots))
	// A restored catalog may not know older/pinned remote snapshots. Preserve them.
	for _, snapshot := range snapshots {
		if !known[snapshot.ID] {
			keepSet[snapshot.ID] = true
		}
		if keepSet[snapshot.ID] {
			keep = append(keep, snapshot.ID)
		}
	}
	if p.KeepAll || len(keep) == 0 {
		return nil
	}
	if err = repo.Forget(ctx, keep); err != nil {
		return err
	}
	if err = s.eachRecord(ctx, func(r backupRecord, _ int) error {
		if r.RemoteScope == scope && r.RemoteSnapshotID != "" && actual[r.RemoteSnapshotID] && !keepSet[r.RemoteSnapshotID] {
			r.RemoteSnapshotID = ""
			return s.putRecord(r)
		}
		return nil
	}); err != nil {
		return err
	}
	return repo.Prune(ctx)
}
func cloudScope(c backupcloud.Credentials, dataset string) string {
	sum := sha256.Sum256([]byte(c.ClientID + "\x00" + c.FolderID + "\x00" + dataset))
	return hex.EncodeToString(sum[:])
}
func parseBackupCursor(v string) (int64, error) {
	if v == "" {
		return 1 << 62, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 1 {
		return 0, errors.New("cursor ไม่ถูกต้อง")
	}
	return n, nil
}
func backupSuffix(path string) string {
	for _, p := range []string{"/admin/api/backups", "/setup/api/backups"} {
		if strings.HasPrefix(path, p) {
			return strings.Trim(strings.TrimPrefix(path, p), "/")
		}
	}
	return "!"
}
