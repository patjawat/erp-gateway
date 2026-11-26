package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Repair struct {
	ID                string `json:"id"`
	RepairNumber      string `json:"repair_number"`
	Title             string `json:"title"`
	Status            string `json:"status"`
	StatusLabel       string `json:"status_label"`
	CreatedAt         string `json:"created_at"`
	DeviceTypeID      string `json:"device_type_id"`
	DeviceType        string `json:"device_type"`
	AssetNumber       string `json:"asset_number"`
	RepairGroup       string `json:"repair_group"`
	RepairGroupLabel  string `json:"repair_group_label"`
	Location          string `json:"location"`
	Phone             string `json:"phone"`
	Urgency           string `json:"urgency"`
	Problem           string `json:"problem,omitempty"`
	Note              string `json:"note"`
	RequestRepairDate string `json:"request_repair_date"`
}

type RepairHistory struct {
	Items        []Repair       `json:"items"`
	Page         int            `json:"page"`
	PerPage      int            `json:"per_page"`
	TotalCount   int            `json:"total_count"`
	HasMore      bool           `json:"has_more"`
	SummaryTotal int            `json:"summary_total"`
	StatusCounts map[string]int `json:"status_counts"`
	FiscalYears  []int          `json:"fiscal_years,omitempty"`
}

type RepairOption struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

type RepairOptions struct {
	UploadEnabled   bool           `json:"upload_enabled"`
	MaxUploadBytes  int            `json:"max_upload_bytes"`
	MaxAttachments  int            `json:"max_attachments"`
	UploadMIMETypes []string       `json:"upload_mime_types"`
	DeviceTypes     []RepairOption `json:"device_types"`
	RepairGroups    []RepairOption `json:"repair_groups"`
	Urgencies       []RepairOption `json:"urgencies"`
	CreateEnabled   bool           `json:"create_enabled"`
}

type RepairAsset struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	DeviceTypeID string `json:"device_type_id"`
	DeviceType   string `json:"device_type"`
	RepairGroup  string `json:"repair_group"`
}

type RepairAssets struct {
	Items   []RepairAsset `json:"items"`
	HasMore bool          `json:"has_more"`
	Page    int           `json:"page,omitempty"`
	PerPage int           `json:"per_page,omitempty"`
}

type RepairCreate struct {
	RequestID         string `json:"request_id"`
	Title             string `json:"title"`
	DeviceTypeID      string `json:"device_type_id"`
	AssetNumber       string `json:"asset_number"`
	RepairGroup       string `json:"repair_group"`
	Location          string `json:"location"`
	Phone             string `json:"phone"`
	Urgency           string `json:"urgency"`
	Problem           string `json:"problem,omitempty"`
	Note              string `json:"note"`
	RequestRepairDate string `json:"request_repair_date"`
}

func RepairPagination(rawQuery string) (page, perPage int, err error) {
	return LeavePagination(rawQuery)
}

// RepairHistoryQuery never accepts ownership or hospital selectors.
func RepairHistoryQuery(raw string) (page, perPage int, query url.Values, err error) {
	query, err = url.ParseQuery(raw)
	if err != nil || len(raw) > 2048 {
		return 0, 0, nil, errors.New("invalid repair query")
	}
	paging := url.Values{}
	for key, values := range query {
		if len(values) != 1 {
			return 0, 0, nil, errors.New("duplicate repair query")
		}
		value := strings.TrimSpace(values[0])
		switch key {
		case "page", "per_page":
			paging.Set(key, value)
		case "q":
			if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 100 || strings.ContainsRune(value, 0) {
				return 0, 0, nil, errors.New("invalid repair search")
			}
			query.Set(key, value)
		case "fiscal_year":
			year, parseErr := strconv.Atoi(value)
			if parseErr != nil || len(value) != 4 || year < 2443 || year > 2744 {
				return 0, 0, nil, errors.New("invalid repair fiscal year")
			}
			query.Set(key, strconv.Itoa(year))
		case "status":
			switch value {
			case "", "pending", "receive", "in_progress", "success", "cancel":
			default:
				return 0, 0, nil, errors.New("invalid repair status")
			}
			query.Set(key, value)
		default:
			return 0, 0, nil, errors.New("invalid repair query")
		}
	}
	page, perPage, err = LeavePagination(paging.Encode())
	return
}

// The caller's hospital and employee are always chosen from the session.
func RepairAssetQuery(rawQuery string) (url.Values, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(rawQuery) > 2048 {
		return nil, errors.New("invalid repair asset selection")
	}
	for key, values := range query {
		if len(values) != 1 || (key != "q" && key != "code" && key != "repair_group" && key != "page" && key != "per_page") {
			return nil, errors.New("invalid repair asset selection")
		}
		if key == "page" || key == "per_page" {
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || (key == "page" && n > 1000000) || (key == "per_page" && n > 100) {
				return nil, errors.New("invalid repair asset page")
			}
			query.Set(key, strconv.Itoa(n))
			continue
		}
		value := strings.TrimSpace(values[0])
		limit := 100
		if key == "repair_group" {
			limit = 1
			if value != "" && value != "1" && value != "2" && value != "3" {
				return nil, errors.New("invalid repair asset selection")
			}
		}
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || strings.ContainsRune(value, 0) || (key == "code" && value == "") {
			return nil, errors.New("invalid repair asset selection")
		}
		query.Set(key, value)
	}
	if query.Has("code") && query.Get("q") != "" {
		return nil, errors.New("exact asset code cannot be combined with text search")
	}
	return query, nil
}

func RepairAssetPagination(query url.Values) (int, int, error) {
	paging := url.Values{}
	for _, key := range []string{"page", "per_page"} {
		if values, ok := query[key]; ok {
			paging[key] = values
		}
	}
	return LeavePagination(paging.Encode())
}

var repairRequestID = regexp.MustCompile(`^[a-zA-Z0-9-]{16,64}$`)

// ValidateRepairCreate normalizes a flat Mobile request before either Go tier
// processes it. Record ownership, status and audit fields cannot be supplied.
func ValidateRepairCreate(value *RepairCreate) error {
	if value == nil {
		return errors.New("invalid repair request")
	}
	for _, field := range []struct {
		value    *string
		max      int
		required bool
	}{
		{&value.RequestID, 64, true}, {&value.Title, 255, true},
		{&value.DeviceTypeID, 100, true}, {&value.AssetNumber, 100, false},
		{&value.RepairGroup, 1, true}, {&value.Location, 1000, true},
		{&value.Phone, 100, true}, {&value.Urgency, 8, true},
		{&value.Problem, 4000, false}, {&value.Note, 4000, false}, {&value.RequestRepairDate, 10, false},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if !utf8.ValidString(*field.value) || utf8.RuneCountInString(*field.value) > field.max || strings.ContainsRune(*field.value, 0) || (field.required && *field.value == "") {
			return errors.New("invalid repair request")
		}
	}
	if !repairRequestID.MatchString(value.RequestID) || (value.RepairGroup != "1" && value.RepairGroup != "2" && value.RepairGroup != "3") {
		return errors.New("invalid repair request")
	}
	switch value.Urgency {
	case "low", "medium", "high", "critical":
	default:
		return errors.New("invalid repair request")
	}
	if value.RequestRepairDate != "" {
		// A retry can arrive after the desired date has passed. The local write
		// source checks the date only after looking up this request's existing
		// record, so a lost success response remains recoverable after midnight.
		if _, err := time.Parse(time.DateOnly, value.RequestRepairDate); err != nil {
			return errors.New("invalid repair request")
		}
	}
	return nil
}
