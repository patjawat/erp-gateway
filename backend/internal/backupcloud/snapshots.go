package backupcloud

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var (
	datasetPattern  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	snapshotPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// SnapshotRef lists the encrypted repository object's name. CreatedAt is the
// provider's object creation time, not an authenticated data-capture time;
// inspect the selected encrypted snapshot before presenting restore details.
type SnapshotRef struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type SnapshotPage struct {
	Items      []SnapshotRef `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type snapshotCursor struct {
	Scope    string `json:"scope"`
	Provider string `json:"provider"`
}

func ListSnapshots(ctx context.Context, credentials Credentials, dataset, cursor string, limit int) (SnapshotPage, error) {
	return defaultClient.ListSnapshots(ctx, credentials, dataset, cursor, limit)
}

// ListSnapshots resolves a fixed repository path and requests exactly one
// bounded Drive page. It does not enumerate/decrypt the repository's entire
// snapshot catalog, create folders or follow every provider page on entry.
func (c Client) ListSnapshots(ctx context.Context, credentials Credentials, dataset, cursor string, limit int) (SnapshotPage, error) {
	page := SnapshotPage{Items: []SnapshotRef{}}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || !datasetPattern.MatchString(dataset) || !folderIDPattern.MatchString(credentials.FolderID) {
		return page, errors.New("invalid snapshot dataset, folder or page size")
	}
	scopeDigest := sha256.Sum256([]byte(credentials.ClientID + "\x00" + credentials.FolderID + "\x00" + dataset))
	scope := base64.RawURLEncoding.EncodeToString(scopeDigest[:])
	providerToken := ""
	if cursor != "" {
		if len(cursor) > 8192 {
			return page, errors.New("invalid snapshot cursor")
		}
		body, err := base64.RawURLEncoding.DecodeString(cursor)
		var decoded snapshotCursor
		if err != nil || json.Unmarshal(body, &decoded) != nil || decoded.Scope != scope || decoded.Provider == "" || len(decoded.Provider) > 4096 {
			return page, errors.New("snapshot cursor no longer matches the selected account, folder or dataset")
		}
		providerToken = decoded.Provider
	}
	// Recovery must not silently create a replacement folder on the wrong
	// account/client: existing application ownership is checked first.
	if _, err := c.EnsureFolder(ctx, credentials); err != nil {
		return page, err
	}
	parent := credentials.FolderID
	for _, name := range []string{"repository", dataset, "snapshots"} {
		child, err := c.childFolder(ctx, credentials, parent, name)
		if err != nil {
			return page, err
		}
		if child == "" {
			return page, nil
		}
		parent = child
	}
	query := url.Values{
		"q":        {"trashed = false and '" + parent + "' in parents and mimeType != 'application/vnd.google-apps.folder'"},
		"pageSize": {strconv.Itoa(limit)},
		"orderBy":  {"createdTime desc,name"},
		"fields":   {"files(name,createdTime),nextPageToken"},
	}
	if providerToken != "" {
		query.Set("pageToken", providerToken)
	}
	var result struct {
		Files []struct {
			Name        string    `json:"name"`
			CreatedTime time.Time `json:"createdTime"`
		} `json:"files"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := c.drive(ctx, credentials, http.MethodGet, "/files", query, nil, &result); err != nil {
		return page, err
	}
	if len(result.Files) > limit || len(result.NextPageToken) > 4096 {
		return page, errors.New("Google returned an invalid snapshot page")
	}
	seen := map[string]bool{}
	for _, file := range result.Files {
		if !snapshotPattern.MatchString(file.Name) || seen[file.Name] {
			continue
		}
		if file.CreatedTime.IsZero() {
			return page, errors.New("Google returned an invalid snapshot creation time")
		}
		seen[file.Name] = true
		page.Items = append(page.Items, SnapshotRef{ID: file.Name, CreatedAt: file.CreatedTime})
	}
	if result.NextPageToken != "" {
		body, _ := json.Marshal(snapshotCursor{Scope: scope, Provider: result.NextPageToken})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(body)
	}
	return page, nil
}

func (c Client) childFolder(ctx context.Context, credentials Credentials, parent, name string) (string, error) {
	query := url.Values{
		"q":        {"trashed = false and '" + parent + "' in parents and mimeType = 'application/vnd.google-apps.folder' and name = '" + name + "'"},
		"pageSize": {"2"},
		"orderBy":  {"createdTime,name"},
		"fields":   {"files(id,mimeType),nextPageToken"},
	}
	var result struct {
		Files         []driveFile `json:"files"`
		NextPageToken string      `json:"nextPageToken"`
	}
	if err := c.drive(ctx, credentials, http.MethodGet, "/files", query, nil, &result); err != nil {
		return "", err
	}
	if len(result.Files) > 1 || result.NextPageToken != "" {
		return "", errors.New("Google Drive contains duplicate backup folders; resolve duplicates before recovery")
	}
	if len(result.Files) == 0 {
		return "", nil
	}
	file := result.Files[0]
	if !folderIDPattern.MatchString(file.ID) || file.MimeType != "application/vnd.google-apps.folder" {
		return "", errors.New("Google returned an invalid backup folder")
	}
	return file.ID, nil
}
