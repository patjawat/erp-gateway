package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupPolicyDefaultsRemainEditable(t *testing.T) {
	p := defaultBackupPolicy()
	if p.Version != 1 || p.Revision != 1 || p.Schedule.Kind != "interval" || p.Schedule.IntervalMinutes != 15 || p.Schedule.Timezone != "Asia/Bangkok" || p.Destination != "local" || p.LocalRetention.Latest != 3 || p.DriveRetention.Latest != 24 || p.DriveRetention.Daily != 7 || p.DriveRetention.Weekly != 4 || p.DriveRetention.Monthly != 3 {
		t.Fatal("backup defaults changed")
	}
	p.Schedule.IntervalMinutes = 7
	p.LocalRetention = backupRetention{KeepAll: true}
	if err := p.validate(); err != nil {
		t.Fatalf("custom values should be allowed: %v", err)
	}
}

func TestBackupPolicyNextRunUsesTimezoneAndSelectedWeekdays(t *testing.T) {
	after := time.Date(2026, 11, 26, 2, 30, 0, 0, time.UTC) // Thursday 09:30 in Bangkok.
	tests := []struct {
		name, kind, at string
		days           []int
		after, want    time.Time
	}{
		{"interval", "interval", "", nil, after, after.Add(15 * time.Minute)},
		{"daily today", "daily", "10:00", nil, after, time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)},
		{"daily exact tick goes next day", "daily", "10:00", nil, time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC), time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)},
		{"weekly Monday", "weekly", "10:00", []int{1}, after, time.Date(2026, 11, 30, 3, 0, 0, 0, time.UTC)},
		{"weekly next selected day", "weekly", "10:00", []int{0, 4}, time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC), time.Date(2026, 11, 29, 3, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := defaultBackupPolicy()
			p.Schedule.Kind = test.kind
			p.Schedule.Time = test.at
			p.Schedule.Weekdays = test.days
			if got := p.next(test.after); !got.Equal(test.want) {
				t.Fatalf("next run=%s want %s", got, test.want)
			}
		})
	}
	p := defaultBackupPolicy()
	p.Schedule.Kind = "daily"
	p.Schedule.Time = "10:00"
	p.Schedule.Timezone = "UTC"
	if got := p.next(after); !got.Equal(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("saved timezone ignored")
	}
}

func TestBackupPolicyRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*BackupPolicy)
	}{
		{"unsupported schema", func(p *BackupPolicy) { p.Version = 2 }},
		{"missing revision", func(p *BackupPolicy) { p.Revision = 0 }},
		{"invalid timezone", func(p *BackupPolicy) { p.Schedule.Timezone = "Unknown/Timezone" }},
		{"interval zero", func(p *BackupPolicy) { p.Schedule.IntervalMinutes = 0 }},
		{"interval too high", func(p *BackupPolicy) { p.Schedule.IntervalMinutes = 10081 }},
		{"daily invalid time", func(p *BackupPolicy) { p.Schedule.Kind = "daily"; p.Schedule.Time = "25:00" }},
		{"weekly no days", func(p *BackupPolicy) { p.Schedule.Kind = "weekly"; p.Schedule.Weekdays = nil }},
		{"weekly invalid day", func(p *BackupPolicy) { p.Schedule.Kind = "weekly"; p.Schedule.Weekdays = []int{7} }},
		{"weekly negative day", func(p *BackupPolicy) { p.Schedule.Kind = "weekly"; p.Schedule.Weekdays = []int{-1} }},
		{"weekly duplicate day", func(p *BackupPolicy) { p.Schedule.Kind = "weekly"; p.Schedule.Weekdays = []int{1, 1} }},
		{"unsupported destination", func(p *BackupPolicy) { p.Destination = "s3" }},
		{"empty local retention", func(p *BackupPolicy) { p.LocalRetention = backupRetention{} }},
		{"negative drive retention", func(p *BackupPolicy) { p.DriveRetention.Latest = -1 }},
		{"zero stale threshold", func(p *BackupPolicy) { p.StaleMinutes = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := defaultBackupPolicy()
			test.change(&p)
			if err := p.validate(); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func TestBackupPolicyHotApplyPersistsAndRejectsStaleRevision(t *testing.T) {
	s, _ := cloudPolicyFixture(t)
	old := s.state.Policy
	updated := old
	updated.Enabled = true
	updated.Schedule.IntervalMinutes = 60
	updated.StaleMinutes = 180
	updated.LocalRetention = backupRetention{KeepAll: true}
	before := time.Now()
	if err := s.applyPolicy(updated); err != nil {
		t.Fatal(err)
	}
	if s.state.Policy.Revision != old.Revision+1 || !s.state.Policy.Enabled || s.state.Policy.Schedule.IntervalMinutes != 60 || !s.state.Policy.LocalRetention.KeepAll || s.state.NextRun.Before(before.Add(59*time.Minute)) || s.state.NextRun.After(time.Now().Add(61*time.Minute)) {
		t.Fatal("saved settings were not applied immediately")
	}
	body, err := os.ReadFile(filepath.Join(s.dir, "state.enc"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted backupState
	if err = s.unseal(body, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Policy.Revision != 2 || !persisted.Policy.Enabled || persisted.Policy.Schedule.IntervalMinutes != 60 || !persisted.Policy.LocalRetention.KeepAll {
		t.Fatal("hot settings did not persist")
	}
	if err = s.applyPolicy(old); err == nil {
		t.Fatal("stale edit revision overwrote policy")
	}
	if s.state.Policy.Revision != 2 || s.state.Policy.Schedule.IntervalMinutes != 60 {
		t.Fatal("stale edit changed active policy")
	}
	drive := s.state.Policy
	drive.Destination = "drive"
	if err = s.applyPolicy(drive); err == nil {
		t.Fatal("unconnected Drive destination accepted")
	}
	if s.state.Policy.Destination != "local" {
		t.Fatal("invalid destination changed active policy")
	}
}

func TestLocalRetentionPreservesNewestAndPinned(t *testing.T) {
	s, pinned := cloudPolicyFixture(t)
	pinned.Pinned = true
	pinned.CreatedAt = time.Now().Add(-48 * time.Hour)
	if err := s.putRecord(pinned); err != nil {
		t.Fatal(err)
	}
	middle := backupRecord{ID: backupID(), CreatedAt: time.Now().Add(-24 * time.Hour), Status: "completed", LocalAvailable: true}
	newest := backupRecord{ID: backupID(), CreatedAt: time.Now(), Status: "completed", LocalAvailable: true}
	for _, record := range []backupRecord{middle, newest} {
		if err := os.MkdirAll(s.recordPath(record.ID), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.recordPath(record.ID), "archive"), []byte("encrypted fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.putRecord(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.retainLocal(context.Background(), backupRetention{Latest: 1}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []backupRecord{pinned, newest} {
		if _, err := os.Stat(s.recordPath(record.ID)); err != nil {
			t.Fatal("newest or pinned backup removed")
		}
	}
	if _, err := os.Stat(s.recordPath(middle.ID)); !os.IsNotExist(err) {
		t.Fatal("expired unpinned local files remain")
	}
	got, err := s.getRecord(middle.ID)
	if err != nil || got.LocalAvailable {
		t.Fatal("removed local backup remains advertised as downloadable")
	}
}

func TestRetentionDailyBucketKeepsOneRepresentative(t *testing.T) {
	policy := backupRetention{Daily: 1}
	newest := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	buckets, seen := map[string]int{}, map[string]bool{}
	for i, at := range []time.Time{newest, newest.Add(-time.Hour), newest.Add(-24 * time.Hour)} {
		got := retained(backupRecord{CreatedAt: at}, i, policy, buckets, seen)
		if want := i == 0; got != want {
			t.Fatalf("daily retention index %d keep=%t want=%t", i, got, want)
		}
	}
}
