package gateway

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type hospitalUsageView struct {
	Status      string   `json:"status"`
	AsOf        *int64   `json:"as_of"`
	Personnel   *int64   `json:"personnel"`
	Active30D   *int64   `json:"active_30d"`
	Adoption30D *float64 `json:"adoption_30d"`
	Android30D  *int64   `json:"android_30d"`
	IOS30D      *int64   `json:"ios_30d"`
}

// Attach one bounded cache query to the existing page transaction. No ERP/API
// fan-out occurs while browsing customers and no individual activity is exposed.
func (s *Server) attachUsagePage(ctx context.Context, tx *sql.Tx, items []Hospital) error {
	if len(items) == 0 {
		return nil
	}
	marks, args, index := make([]string, len(items)), make([]any, len(items)), make(map[string]int, len(items))
	for i, h := range items {
		marks[i] = "?"
		args[i] = h.ID
		index[h.ID] = i
		items[i].Usage = &hospitalUsageView{Status: "not_reported"}
	}
	rows, err := tx.QueryContext(ctx, `SELECT hospital_id,status,as_of,period_day,personnel,active_30d,android_30d,ios_30d FROM usage_snapshots WHERE hospital_id IN (`+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var id, status, day string
		var asof int64
		var people, active, android, ios sql.NullInt64
		if err = rows.Scan(&id, &status, &asof, &day, &people, &active, &android, &ios); err != nil {
			return err
		}
		v := items[index[id]].Usage
		v.Status = status
		if asof > 0 {
			value := asof
			v.AsOf = &value
		}
		fresh := (status == "ready" || status == "partial") && day == bangkokDay(now) && asof >= now.Add(-45*time.Minute).Unix() && asof <= now.Add(time.Minute).Unix() && people.Valid && active.Valid
		if !fresh {
			if asof > 0 {
				v.Status = "stale"
			}
			continue
		}
		p, a := people.Int64, active.Int64
		v.Personnel = &p
		v.Active30D = &a
		if p > 0 {
			ratio := float64(a) * 100 / float64(p)
			v.Adoption30D = &ratio
		}
		if android.Valid {
			n := android.Int64
			v.Android30D = &n
		}
		if ios.Valid {
			n := ios.Int64
			v.IOS30D = &n
		}
	}
	return rows.Err()
}
