package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type updateRig struct {
	server   *Server
	handler  http.Handler
	signing  ed25519.PrivateKey
	sites    []Hospital
	tokens   []string
	manifest updateManifest
}

func newUpdateRig(t *testing.T, n int) *updateRig {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("GATEWAY_UPDATE_SIGNING_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("GATEWAY_UPDATE_REPOSITORIES", "registry.example.test/hospital-api")
	store, e := OpenStore(filepath.Join(t.TempDir(), "gateway.db"), bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	if e = store.migrateUpdates(); e != nil {
		t.Fatal(e)
	}
	approved := []string{}
	for i := 1; i <= n; i++ {
		code := fmt.Sprintf("%05d", 10000+i)
		approved = append(approved, code+"=https://hospital"+code+".test")
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		code := strings.TrimSuffix(strings.TrimPrefix(r.URL.Host, "hospital"), ".test")
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"hospital_code":"` + code + `","api_version":"1","erp_reachable":true}}`))}, nil
	})}
	s, e := New(Config{Store: store, AdminUsername: "admin", AdminPassword: "long test password for updater", ApprovedUpstreams: approved, Client: client})
	if e != nil {
		t.Fatal(e)
	}
	s.admins[tokenHash("admin-token")] = adminSession{csrf: "csrf", expires: time.Now().Add(time.Hour)}
	mux := http.NewServeMux()
	s.registerUpdateRoutes(mux)
	rig := &updateRig{server: s, handler: mux, signing: priv}
	for i := 1; i <= n; i++ {
		code := fmt.Sprintf("%05d", 10000+i)
		h, e := store.addHospital(context.Background(), Hospital{Hospital: contracts.Hospital{Code: code, Name: "Synthetic site " + code}, APIURL: "https://hospital" + code + ".test"}, strings.Repeat("k", 40))
		if e != nil {
			t.Fatal(e)
		}
		_, e = store.db.Exec("UPDATE hospitals SET approval_status='approved',enabled=1 WHERE id=?", h.ID)
		if e != nil {
			t.Fatal(e)
		}
		h.ApprovalStatus = "approved"
		h.Enabled = true
		rig.sites = append(rig.sites, h)
		w := rig.call(t, "POST", "/admin/api/hospitals/"+h.ID+"/updater-token", map[string]any{}, "", true)
		var out struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.Token == "" {
			t.Fatal(w.Code, w.Body.String())
		}
		rig.tokens = append(rig.tokens, out.Data.Token)
		rig.poll(t, i-1)
	}
	rig.manifest = updateManifest{ID: "api-2.8.0-amd64", Version: "2.8.0", Image: "registry.example.test/hospital-api@sha256:" + strings.Repeat("a", 64), Architecture: "amd64", MinUpdaterVersion: "1.0.0", RollbackSafe: true}
	rig.manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, rig.manifest.signingBytes()))
	w := rig.call(t, "POST", "/admin/api/updates/releases", rig.manifest, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return rig
}

func (r *updateRig) call(t *testing.T, method, path string, body any, token string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "https://gateway.example.test"+path, bytes.NewReader(b))
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if admin {
		req.AddCookie(&http.Cookie{Name: adminCookie, Value: "admin-token"})
		req.Header.Set("X-CSRF-Token", "csrf")
	}
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}
func (r *updateRig) poll(t *testing.T, i int) *httptest.ResponseRecorder {
	return r.call(t, "POST", "/agent/v1/poll", map[string]any{"hospital_code": r.sites[i].Code, "architecture": "amd64", "updater_version": "1.0.0", "actual_image": "registry.example.test/hospital-api@sha256:" + strings.Repeat("b", 64)}, r.tokens[i], false)
}
func (r *updateRig) dispatch(t *testing.T, ids []string) *httptest.ResponseRecorder {
	return r.call(t, "POST", "/admin/api/updates/jobs", map[string]any{"release_id": r.manifest.ID, "scope": "selected", "hospital_ids": ids, "max_parallel": 2}, "", true)
}
func decodeUpdateJob(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			Job map[string]any `json:"job"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Body.String())
	}
	return out.Data.Job
}

func TestUpdateManifestPolicyAndPublisherDoesNotDispatch(t *testing.T) {
	r := newUpdateRig(t, 1)
	bad := r.manifest
	bad.Image = "evil.example.test/api@sha256:" + strings.Repeat("a", 64)
	if verifyUpdateManifest(bad) == nil {
		t.Fatal("unapproved repository accepted")
	}
	bad = r.manifest
	bad.RollbackSafe = false
	if verifyUpdateManifest(bad) == nil {
		t.Fatal("tampered rollback policy accepted")
	}
	bad = r.manifest
	bad.Image = "registry.example.test/hospital-api:latest"
	if verifyUpdateManifest(bad) == nil {
		t.Fatal("mutable image accepted")
	}
	var n int
	r.server.config.Store.db.QueryRow("SELECT COUNT(*) FROM update_jobs").Scan(&n)
	if n != 0 {
		t.Fatal("publishing created deployment jobs")
	}
	pubToken := strings.Repeat("p", 43)
	t.Setenv("GATEWAY_RELEASE_PUBLISH_TOKEN_SHA256", tokenHash(pubToken))
	m := r.manifest
	m.ID = "ci-release"
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(r.signing, m.signingBytes()))
	w := r.call(t, "POST", "/release/v1/catalog", m, "wrong", false)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = r.call(t, "POST", "/release/v1/catalog", m, pubToken, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r.server.config.Store.db.QueryRow("SELECT COUNT(*) FROM update_jobs").Scan(&n)
	if n != 0 {
		t.Fatal("CI publication dispatched a rollout")
	}
}

func TestUpdatePilotLeaseSiteIsolationAndIdempotentReports(t *testing.T) {
	r := newUpdateRig(t, 2)
	w := r.dispatch(t, []string{r.sites[0].ID, r.sites[1].ID})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var pilotSite string
	r.server.config.Store.db.QueryRow("SELECT hospital_id FROM update_jobs ORDER BY seq LIMIT 1").Scan(&pilotSite)
	pilot, other := 0, 1
	if r.sites[1].ID == pilotSite {
		pilot, other = 1, 0
	}
	if decodeUpdateJob(t, r.poll(t, other)) != nil {
		t.Fatal("non-pilot started before pilot success")
	}
	job := decodeUpdateJob(t, r.poll(t, pilot))
	if job == nil {
		t.Fatal("pilot missing")
	}
	if decodeUpdateJob(t, r.poll(t, pilot)) != nil {
		t.Fatal("job replayed while leased")
	}
	payload := map[string]any{"lease_token": job["lease_token"], "sequence": 1, "stage": "awaiting_external", "message": "local healthy", "actual_image": r.manifest.Image}
	path := "/agent/v1/jobs/" + job["id"].(string) + "/report"
	if w = r.call(t, "POST", path, payload, r.tokens[other], false); w.Code != 409 {
		t.Fatal("cross-site report accepted", w.Code)
	}
	w = r.call(t, "POST", path, payload, r.tokens[pilot], false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"succeeded"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = r.call(t, "POST", path, payload, r.tokens[pilot], false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"terminal":true`) {
		t.Fatal("identical lost acknowledgement retry failed", w.Code, w.Body.String())
	}
	payload["message"] = "different payload"
	if w = r.call(t, "POST", path, payload, r.tokens[pilot], false); w.Code != 409 {
		t.Fatal("same revision with changed payload accepted", w.Code)
	}
	if decodeUpdateJob(t, r.poll(t, other)) == nil {
		t.Fatal("batch did not unlock after independent verification")
	}
}

func TestUpdateExpiredLeasePausesWithoutReplay(t *testing.T) {
	r := newUpdateRig(t, 1)
	w := r.dispatch(t, []string{r.sites[0].ID})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	job := decodeUpdateJob(t, r.poll(t, 0))
	r.server.config.Store.db.Exec("UPDATE update_jobs SET lease_until=? WHERE id=?", time.Now().Unix()-1, job["id"])
	if decodeUpdateJob(t, r.poll(t, 0)) != nil {
		t.Fatal("expired in-flight job was replayed")
	}
	var status, state string
	r.server.config.Store.db.QueryRow("SELECT j.status,b.state FROM update_jobs j JOIN update_rollouts b ON b.id=j.rollout_id WHERE j.id=?", job["id"]).Scan(&status, &state)
	if status != "blocked" || state != "paused" {
		t.Fatal(status, state)
	}
	w = r.call(t, "POST", "/agent/v1/jobs/"+job["id"].(string)+"/report", map[string]any{"lease_token": job["lease_token"], "sequence": 1, "stage": "applying", "message": "late", "actual_image": ""}, r.tokens[0], false)
	if w.Code != 409 {
		t.Fatal("expired lease report accepted", w.Code)
	}
}

func TestUpdateGlobalConcurrencyCapAndPagination(t *testing.T) {
	r := newUpdateRig(t, 6)
	for _, h := range r.sites {
		w := r.dispatch(t, []string{h.ID})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i := 0; i < 5; i++ {
		if decodeUpdateJob(t, r.poll(t, i)) == nil {
			t.Fatalf("site %d did not get pilot", i)
		}
	}
	if decodeUpdateJob(t, r.poll(t, 5)) != nil {
		t.Fatal("global concurrency cap exceeded")
	}
	w := r.call(t, "GET", "/admin/api/updates/jobs?limit=2", nil, "", true)
	var out struct {
		Data struct {
			Jobs  []map[string]any `json:"jobs"`
			Next  string           `json:"next_cursor"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data.Jobs) != 2 || out.Data.Next == "" || out.Data.Total != 6 {
		t.Fatal(w.Code, w.Body.String())
	}
	first := out.Data.Jobs[0]["id"]
	w = r.call(t, "GET", "/admin/api/updates/jobs?limit=2&cursor="+out.Data.Next, nil, "", true)
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data.Jobs) != 2 || out.Data.Jobs[0]["id"] == first {
		t.Fatal("cursor did not advance")
	}
}

func TestUpdaterRejectsPlaintextAndRotatedCredential(t *testing.T) {
	r := newUpdateRig(t, 1)
	req := httptest.NewRequest("POST", "http://gateway.example.test/agent/v1/poll", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+r.tokens[0])
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("plaintext agent request accepted", w.Code)
	}
	w = r.call(t, "POST", "/admin/api/hospitals/"+r.sites[0].ID+"/updater-token", map[string]any{}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = r.poll(t, 0); w.Code != 401 {
		t.Fatal("revoked credential accepted", w.Code)
	}
}

func TestUpdateLocalHealthCannotBypassGatewayVerification(t *testing.T) {
	r := newUpdateRig(t, 2)
	if w := r.dispatch(t, []string{r.sites[0].ID, r.sites[1].ID}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var pilotSite string
	r.server.config.Store.db.QueryRow("SELECT hospital_id FROM update_jobs ORDER BY seq LIMIT 1").Scan(&pilotSite)
	pilot, other := 0, 1
	if r.sites[1].ID == pilotSite {
		pilot, other = 1, 0
	}
	job := decodeUpdateJob(t, r.poll(t, pilot))
	r.server.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})
	w := r.call(t, "POST", "/agent/v1/jobs/"+job["id"].(string)+"/report", map[string]any{"lease_token": job["lease_token"], "sequence": 1, "stage": "awaiting_external", "message": "local healthy", "actual_image": r.manifest.Image}, r.tokens[pilot], false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"awaiting_external"`) || !strings.Contains(w.Body.String(), `"terminal":false`) {
		t.Fatal("local-only evidence accepted as success", w.Code, w.Body.String())
	}
	if decodeUpdateJob(t, r.poll(t, other)) != nil {
		t.Fatal("fleet advanced without Gateway verification")
	}
}

func TestUpdateFailureBlocksPendingSitesUntilNewExplicitRollout(t *testing.T) {
	r := newUpdateRig(t, 2)
	if w := r.dispatch(t, []string{r.sites[0].ID, r.sites[1].ID}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var pilotSite string
	r.server.config.Store.db.QueryRow("SELECT hospital_id FROM update_jobs ORDER BY seq LIMIT 1").Scan(&pilotSite)
	pilot, other := 0, 1
	if r.sites[1].ID == pilotSite {
		pilot, other = 1, 0
	}
	job := decodeUpdateJob(t, r.poll(t, pilot))
	w := r.call(t, "POST", "/agent/v1/jobs/"+job["id"].(string)+"/report", map[string]any{"lease_token": job["lease_token"], "sequence": 1, "stage": "rolled_back", "message": "prior healthy API restored", "actual_image": "registry.example.test/hospital-api@sha256:" + strings.Repeat("b", 64)}, r.tokens[pilot], false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if decodeUpdateJob(t, r.poll(t, other)) != nil {
		t.Fatal("fleet continued after failed pilot")
	}
	var status string
	r.server.config.Store.db.QueryRow("SELECT status FROM update_jobs WHERE hospital_id=?", r.sites[other].ID).Scan(&status)
	if status != "blocked" {
		t.Fatal("pending job did not explain paused rollout", status)
	}
	if w = r.dispatch(t, []string{r.sites[other].ID}); w.Code != 200 {
		t.Fatal("reviewed new explicit rollout could not be scheduled", w.Code, w.Body.String())
	}
}
