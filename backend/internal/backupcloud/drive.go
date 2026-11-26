package backupcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var folderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

type driveFile struct {
	ID            string            `json:"id"`
	MimeType      string            `json:"mimeType"`
	Trashed       bool              `json:"trashed"`
	AppProperties map[string]string `json:"appProperties"`
}

func ownedFolder(file driveFile) bool {
	return folderIDPattern.MatchString(file.ID) && file.MimeType == "application/vnd.google-apps.folder" && !file.Trashed && file.AppProperties["erp_gateway_backup"] == "v1"
}

func (c Client) EnsureFolder(ctx context.Context, credentials Credentials) (Credentials, error) {
	if err := validClient(credentials); err != nil {
		return credentials, err
	}
	if !singleLine(credentials.AccessToken, 16384) {
		return credentials, ErrReconnect
	}
	fields := "id,mimeType,trashed,appProperties"
	if credentials.FolderID != "" {
		if !folderIDPattern.MatchString(credentials.FolderID) {
			return credentials, errors.New("invalid application backup folder ID")
		}
		var file driveFile
		if err := c.drive(ctx, credentials, http.MethodGet, "/files/"+credentials.FolderID, url.Values{"fields": {fields}}, nil, &file); err != nil {
			return credentials, err
		}
		if !ownedFolder(file) || file.ID != credentials.FolderID {
			return credentials, errors.New("selected Google Drive folder is not an application-owned backup folder")
		}
		return credentials, nil
	}
	var list struct {
		Files []driveFile `json:"files"`
	}
	query := url.Values{"q": {"trashed = false and mimeType = 'application/vnd.google-apps.folder' and appProperties has { key='erp_gateway_backup' and value='v1' }"}, "spaces": {"drive"}, "pageSize": {"2"}, "orderBy": {"createdTime"}, "fields": {"files(" + fields + ")"}}
	if err := c.drive(ctx, credentials, http.MethodGet, "/files", query, nil, &list); err != nil {
		return credentials, err
	}
	for _, file := range list.Files {
		if ownedFolder(file) {
			credentials.FolderID = file.ID
			return credentials, nil
		}
	}
	var created driveFile
	request := map[string]any{"name": "ERP Gateway Backups", "mimeType": "application/vnd.google-apps.folder", "appProperties": map[string]string{"erp_gateway_backup": "v1"}}
	if err := c.drive(ctx, credentials, http.MethodPost, "/files", url.Values{"fields": {fields}}, request, &created); err != nil {
		return credentials, err
	}
	if !ownedFolder(created) {
		return credentials, errors.New("Google did not confirm an application-owned backup folder")
	}
	credentials.FolderID = created.ID
	return credentials, nil
}

type Storage struct {
	Limit        string `json:"limit"`
	Usage        string `json:"usage"`
	UsageInDrive string `json:"usageInDrive"`
}

func Quota(ctx context.Context, credentials Credentials) (Storage, error) {
	return defaultClient.Quota(ctx, credentials)
}
func (c Client) Quota(ctx context.Context, credentials Credentials) (Storage, error) {
	var result struct {
		Storage Storage `json:"storageQuota"`
	}
	err := c.drive(ctx, credentials, http.MethodGet, "/about", url.Values{"fields": {"storageQuota"}}, nil, &result)
	return result.Storage, err
}

func (c Client) drive(ctx context.Context, credentials Credentials, method, path string, query url.Values, input, out any) error {
	endpoint := c.DriveURL
	if endpoint == "" {
		endpoint = "https://www.googleapis.com/drive/v3"
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/") + path)
	if err != nil {
		return errors.New("invalid configured Google Drive endpoint")
	}
	u.RawQuery = query.Encode()
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return errors.New("invalid Google Drive request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot create Google Drive request")
	}
	if !singleLine(credentials.AccessToken, 16384) {
		return ErrReconnect
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cannot connect to Google Drive")
	}
	defer response.Body.Close()
	raw, err := readLimited(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == 401 {
			return ErrReconnect
		}
		var failure struct {
			Error struct {
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		for _, item := range failure.Error.Errors {
			if item.Reason == "storageQuotaExceeded" || item.Reason == "rateLimitExceeded" || item.Reason == "userRateLimitExceeded" || item.Reason == "dailyLimitExceeded" {
				return ErrQuota
			}
		}
		return fmt.Errorf("Google Drive request failed (HTTP %d); check account access and connection", response.StatusCode)
	}
	if json.Unmarshal(raw, out) != nil {
		return errors.New("Google Drive returned an invalid response")
	}
	return nil
}
