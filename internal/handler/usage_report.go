package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/storage"
)

type UsageReportHandler struct {
	store *storage.Store
}

func NewUsageReportHandler(store *storage.Store) *UsageReportHandler {
	return &UsageReportHandler{store: store}
}

// HandleUserUsage serves the SSO partner usage report. The contract is
// GET /api/v1/usage/users?user_ids=...&from=...&to=.... Usernames are not
// accepted here: callers must use stable IDs and receive the corresponding
// tags with the report.
func (h *UsageReportHandler) HandleUserUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	if r.URL.Path == "/api/v1/usage/users" || r.URL.Path == "/api/v1/usage/users/" {
		h.handleUsersReport(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *UsageReportHandler) handleUsersReport(w http.ResponseWriter, r *http.Request) {
	userIDs := queryList(r.URL.Query()["user_ids"])
	userIDs = append(userIDs, queryList(r.URL.Query()["user_id"])...)
	userIDs = append(userIDs, queryList(r.URL.Query()["users"])...)
	userIDs = uniqueStrings(userIDs)
	if len(userIDs) == 0 {
		http.Error(w, "user_ids is required", http.StatusBadRequest)
		return
	}
	if len(userIDs) > 100 {
		http.Error(w, "at most 100 users may be requested", http.StatusBadRequest)
		return
	}
	from, to, err := parseUsageWindow(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report, err := h.store.GetUsersUsageReport(r.Context(), userIDs, from, to)
	if err != nil {
		if strings.Contains(err.Error(), "one or more users do not exist") {
			http.Error(w, "one or more users were not found", http.StatusNotFound)
			return
		}
		http.Error(w, "usage report unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, report)
}

func parseUsageWindow(r *http.Request) (time.Time, time.Time, error) {
	fromValue := r.URL.Query().Get("from")
	if fromValue == "" {
		fromValue = r.URL.Query().Get("start")
	}
	toValue := r.URL.Query().Get("to")
	if toValue == "" {
		toValue = r.URL.Query().Get("end")
	}
	from, err := parseUsageTime(fromValue)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := parseUsageTime(toValue)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	if to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("usage window may not exceed 366 days")
	}
	return from.UTC(), to.UTC(), nil
}

func parseUsageTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("value is required")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD")
	}
	return parsed, nil
}

func queryList(values []string) []string {
	var result []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
