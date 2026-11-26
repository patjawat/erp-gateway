package backupcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListSnapshotsUsesOneScopedProviderPage(t *testing.T) {
	dataset := strings.Repeat("a", 32)
	first, second := strings.Repeat("1", 64), strings.Repeat("2", 64)
	listRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Error("missing owner authorization")
		}
		if r.URL.Path == "/files/app-folder" {
			_, _ = w.Write([]byte(`{"id":"app-folder","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}`))
			return
		}
		if r.URL.Path != "/files" || r.Method != "GET" {
			t.Error("unexpected request")
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("q")
		var child string
		switch {
		case strings.Contains(q, "'app-folder' in parents") && strings.Contains(q, "name = 'repository'"):
			child = "repository-folder"
		case strings.Contains(q, "'repository-folder' in parents") && strings.Contains(q, "name = '"+dataset+"'"):
			child = "dataset-folder"
		case strings.Contains(q, "'dataset-folder' in parents") && strings.Contains(q, "name = 'snapshots'"):
			child = "snapshots-folder"
		case strings.Contains(q, "'snapshots-folder' in parents"):
			listRequests++
			if r.URL.Query().Get("pageSize") != "2" || r.URL.Query().Get("orderBy") != "createdTime desc,name" || !strings.Contains(q, "trashed = false") {
				t.Error("snapshot list must be bounded, ordered and scoped")
			}
			if token := r.URL.Query().Get("pageToken"); token == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]string{"name": first, "createdTime": "2026-11-26T05:00:00Z"}}, "nextPageToken": "provider-page-2"})
			} else if token == "provider-page-2" {
				_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]string{"name": second, "createdTime": "2026-11-25T05:00:00Z"}}})
			} else {
				t.Error("incorrect provider cursor")
				w.WriteHeader(400)
			}
			return
		default:
			t.Errorf("query escaped parent scope: %s", q)
			w.WriteHeader(400)
			return
		}
		if r.URL.Query().Get("pageSize") != "2" {
			t.Error("folder resolution must be bounded")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]string{"id": child, "mimeType": "application/vnd.google-apps.folder"}}})
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), DriveURL: server.URL}
	credentials := Credentials{ClientID: "fixture-client", ClientSecret: "fixture-secret", FolderID: "app-folder", AccessToken: "fixture-access"}
	page, err := c.ListSnapshots(context.Background(), credentials, dataset, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != first || page.NextCursor == "" || listRequests != 1 || !page.Items[0].CreatedAt.Equal(time.Date(2026, 11, 26, 5, 0, 0, 0, time.UTC)) {
		t.Fatal("initial load fetched more than the first page or lost metadata")
	}
	next, err := c.ListSnapshots(context.Background(), credentials, dataset, page.NextCursor, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != second || next.NextCursor != "" || listRequests != 2 {
		t.Fatalf("next page: %+v %v", next, err)
	}
	if _, err = c.ListSnapshots(context.Background(), credentials, strings.Repeat("b", 32), page.NextCursor, 2); err == nil {
		t.Fatal("cursor from another dataset accepted")
	}
	credentials.FolderID = "other-folder"
	if _, err = c.ListSnapshots(context.Background(), credentials, dataset, page.NextCursor, 2); err == nil {
		t.Fatal("cursor from another folder accepted")
	}
}

func TestListSnapshotsRejectsInvalidBoundariesAndDuplicateFolders(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/files/app-folder" {
			_, _ = w.Write([]byte(`{"id":"app-folder","mimeType":"application/vnd.google-apps.folder","appProperties":{"erp_gateway_backup":"v1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"files":[{"id":"one","mimeType":"application/vnd.google-apps.folder"},{"id":"two","mimeType":"application/vnd.google-apps.folder"}]}`))
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), DriveURL: server.URL}
	credentials := Credentials{ClientID: "fixture-client", ClientSecret: "fixture-secret", FolderID: "app-folder", AccessToken: "fixture-access"}
	for _, limit := range []int{-1, 101} {
		if _, err := c.ListSnapshots(context.Background(), credentials, strings.Repeat("a", 32), "", limit); err == nil {
			t.Fatal("unbounded page size accepted")
		}
	}
	if _, err := c.ListSnapshots(context.Background(), credentials, "../other", "", 20); err == nil {
		t.Fatal("unsafe dataset accepted")
	}
	if requests != 0 {
		t.Fatal("invalid request contacted Drive")
	}
	if _, err := c.ListSnapshots(context.Background(), credentials, strings.Repeat("a", 32), "", 20); err == nil {
		t.Fatal("ambiguous repository folders accepted")
	}
}
