package contracts

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Summaries count requests, not leave days, over the full selected scope. Only
// months represented in the current bounded page are returned. Zero year/month
// is the explicit bucket for a missing or invalid start date.
type LeaveHistoryMonthSummary struct {
	Year       int                     `json:"year"`
	Month      int                     `json:"month"`
	Total      int                     `json:"total"`
	LeaveTypes []LeaveHistoryTypeCount `json:"leave_types"`
}

type LeaveHistoryTypeCount struct {
	LeaveTypeID string `json:"leave_type_id"`
	LeaveType   string `json:"leave_type"`
	Count       int    `json:"count"`
}

// LeaveHistoryMonthKey never substitutes a creation date for an unknown start.
func LeaveHistoryMonthKey(start string) string {
	date, err := time.Parse(time.DateOnly, start)
	if err != nil || date.Year() < 1 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d", date.Year(), date.Month())
}

func ValidLeaveHistoryMonthSummaries(summaries []LeaveHistoryMonthSummary, items []Leave, total int) bool {
	if summaries == nil {
		return true // Older connected APIs may omit aggregate metadata.
	}
	expected := map[string]bool{}
	for _, item := range items {
		expected[LeaveHistoryMonthKey(item.StartDate)] = true
	}
	if len(summaries) != len(expected) || len(summaries) > 100 || total < 0 {
		return false
	}
	seen := map[string]bool{}
	groups, combined := 0, 0
	for _, summary := range summaries {
		key := ""
		if summary.Year != 0 || summary.Month != 0 {
			if summary.Year < 1 || summary.Year > 9999 || summary.Month < 1 || summary.Month > 12 {
				return false
			}
			key = fmt.Sprintf("%04d-%02d", summary.Year, summary.Month)
		}
		if !expected[key] || seen[key] || summary.Total < 1 || summary.Total > total-combined || len(summary.LeaveTypes) == 0 {
			return false
		}
		seen[key], combined = true, combined+summary.Total
		ids, sum := map[string]bool{}, 0
		for _, kind := range summary.LeaveTypes {
			groups++
			if groups > 1000 || len(kind.LeaveTypeID) > 255 || !utf8.ValidString(kind.LeaveTypeID) || strings.TrimSpace(kind.LeaveType) == "" || ids[kind.LeaveTypeID] || kind.Count < 1 || kind.Count > summary.Total-sum {
				return false
			}
			ids[kind.LeaveTypeID], sum = true, sum+kind.Count
		}
		if sum != summary.Total {
			return false
		}
	}
	return true
}
