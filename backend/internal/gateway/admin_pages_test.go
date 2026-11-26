package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestAdminPagesBoundedFilteredAndScoped(t *testing.T) {
	r := rig(t)
	for i := 0; i < 31; i++ {
		_, err := r.app.config.Store.addHospital(context.Background(), Hospital{APIURL: "https://unused.example", Hospital: contracts.Hospital{Code: fmt.Sprintf("%05d", i+10000), Name: fmt.Sprintf("Hospital %02d", i)}}, strings.Repeat("k", 32))
		if err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Data struct {
			Hospitals []Hospital `json:"hospitals"`
			Next      *string    `json:"next_cursor"`
			Total     int        `json:"total"`
		}
	}
	first := r.request(t, "GET", "/admin/api/hospitals?limit=10", nil, "", true)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	if err := json.Unmarshal(first.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data.Hospitals) != 10 || out.Data.Total != 31 || out.Data.Next == nil {
		t.Fatalf("first page = %+v", out.Data)
	}
	cursor := *out.Data.Next
	last := out.Data.Hospitals[9].Code
	second := r.request(t, "GET", "/admin/api/hospitals?limit=10&cursor="+url.QueryEscape(cursor), nil, "", true)
	if err := json.Unmarshal(second.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Hospitals[0].Code <= last {
		t.Fatal("cursor duplicated rows")
	}
	if w := r.request(t, "GET", "/admin/api/hospitals?q=Hospital&cursor="+url.QueryEscape(cursor), nil, "", true); w.Code != 400 {
		t.Fatal("filter change accepted stale cursor", w.Code)
	}
	if w := r.request(t, "GET", "/admin/api/hospitals?limit=10000", nil, "", true); w.Code != 400 {
		t.Fatal("unbounded limit accepted")
	}
	search := r.request(t, "GET", "/admin/api/hospitals?q=Hospital%2003", nil, "", true)
	json.Unmarshal(search.Body.Bytes(), &out)
	if out.Data.Total != 1 || len(out.Data.Hospitals) != 1 {
		t.Fatal(search.Body.String())
	}
	if w := r.request(t, "GET", "/admin/api/hospitals", nil, "", false); w.Code != 401 {
		t.Fatal("admin scope bypassed")
	}
	var audits struct {
		Data struct {
			Events []Audit `json:"events"`
			Total  int     `json:"total"`
			Next   *string `json:"next_cursor"`
		}
	}
	w := r.request(t, "GET", "/admin/api/audit?limit=7&action=create", nil, "", true)
	json.Unmarshal(w.Body.Bytes(), &audits)
	if len(audits.Data.Events) != 7 || audits.Data.Total != 31 || audits.Data.Next == nil {
		t.Fatal(w.Body.String())
	}
	w = r.request(t, "GET", "/admin/api/dashboard", nil, "", true)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"personnel":null`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"total":31`)) {
		t.Fatal(w.Body.String())
	}
}

func TestVerifyStoreKeyDoesNotRewriteExistingDatabase(t *testing.T) {
	r := rig(t)
	if _, err := r.app.config.Store.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyStoreKey(r.dbPath, bytes.Repeat([]byte{8}, 32)); err == nil {
		t.Fatal("wrong key accepted")
	}
	after, err := os.ReadFile(r.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("wrong-key probe changed database")
	}
	if err = VerifyStoreKey(r.dbPath, bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
}
