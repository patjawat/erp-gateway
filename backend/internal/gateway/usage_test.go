package gateway

import (
	"context"
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"math"
	"strings"
	"testing"
	"time"
)

func TestUsageSummaryWeightsOnlyFreshCompatibleCohorts(t *testing.T) {
	r := rig(t)
	store := r.app.config.Store
	now := time.Now()
	for i, code := range []string{"11447", "12448"} {
		h, err := store.addHospital(context.Background(), Hospital{Hospital: contracts.Hospital{Code: code, Name: code}, APIURL: "https://" + code + ".example.invalid"}, strings.Repeat(code, 8))
		if err != nil {
			t.Fatal(err)
		}
		people, active := 10, 5
		if i == 1 {
			people, active = 100, 10
		}
		_, err = store.db.Exec(`INSERT INTO usage_snapshots(hospital_id,payload,status,checked_at,as_of,period_day,personnel,eligible,ever_used,active_today,active_7d,active_30d,android_30d,ios_30d) VALUES(?,'{}','ready',?,?,?,?,?,?,?,?,?,?,?)`, h.ID, now.Unix(), now.Unix(), bangkokDay(now), people, people, active, active, active, active, active*2, active)
		if err != nil {
			t.Fatal(err)
		}
	}
	v, err := store.usageSummary(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	want := 15.0 * 100 / 110
	if v["status"] != "ready" || math.Abs(v["adoption_30d"].(float64)-want) > 0.001 {
		t.Fatalf("weighted summary: %+v", v)
	}
	response := r.request(t, "GET", "/admin/api/hospitals?limit=1", nil, "", true)
	var page struct {
		Data struct {
			Hospitals []Hospital `json:"hospitals"`
			Next      *string    `json:"next_cursor"`
		} `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(page.Data.Hospitals) != 1 || page.Data.Next == nil || page.Data.Hospitals[0].Usage == nil || page.Data.Hospitals[0].Usage.Adoption30D == nil || *page.Data.Hospitals[0].Usage.Adoption30D != 50 {
		t.Fatal("missing per-site cached metric", response.Code, response.Body.String())
	}
	if _, err = store.db.Exec("UPDATE usage_snapshots SET status='unavailable'"); err != nil {
		t.Fatal(err)
	}
	v, err = store.usageSummary(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if v["status"] != "stale" || v["personnel"] != nil || v["adoption_30d"] != nil {
		t.Fatalf("offline became zero: %+v", v)
	}
	response = r.request(t, "GET", "/admin/api/hospitals?limit=1", nil, "", true)
	json.Unmarshal(response.Body.Bytes(), &page)
	if page.Data.Hospitals[0].Usage.Status != "stale" || page.Data.Hospitals[0].Usage.Personnel != nil || page.Data.Hospitals[0].Usage.Adoption30D != nil {
		t.Fatal("stale per-site metric became zero", response.Body.String())
	}
}

func TestUsageSnapshotRejectsWrongSiteWindowAndImpossiblePeople(t *testing.T) {
	now := time.Now()
	h := Hospital{Hospital: contracts.Hospital{Code: "11447"}}
	valid := usageSnapshot{SchemaVersion: 1, HospitalCode: h.Code, Cohort: usageCohort, PeriodDay: bangkokDay(now), Timezone: "Asia/Bangkok", AsOf: now.Unix(), CoverageSince: now.Add(-time.Hour).Unix(), Personnel: 100, Eligible: 90, EverUsed: 70, ActiveToday: 10, Active7D: 30, Active30D: 50, Android30D: 65, IOS30D: 40, Status: "ready"}
	if !validUsageSnapshot(valid, h, now) {
		t.Fatal("valid multi-device cohort rejected")
	}
	for _, change := range []func(*usageSnapshot){func(v *usageSnapshot) { v.HospitalCode = "12448" }, func(v *usageSnapshot) { v.Active30D = 101 }, func(v *usageSnapshot) { v.Cohort = "sessions" }, func(v *usageSnapshot) { v.PeriodDay = bangkokDay(now.AddDate(0, 0, -1)) }, func(v *usageSnapshot) { v.AsOf = now.Add(-time.Hour).Unix() }} {
		v := valid
		change(&v)
		if validUsageSnapshot(v, h, now) {
			t.Fatalf("invalid snapshot accepted: %+v", v)
		}
	}
}
