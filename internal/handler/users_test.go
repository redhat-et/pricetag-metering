package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestUsersHandlerValidatesRequiredAndAdditionalTags(t *testing.T) {
	h := &UsersHandler{cfg: config.Config{RequiredUserTags: []string{"email", "first_name", "last_name"}}}
	if err := h.validateCreate("rh-user-1", map[string]string{
		"email": "alice@example.com", "first_name": "Alice", "last_name": "Example",
		"manager_uuid": "manager-1",
	}); err != nil {
		t.Fatalf("validateCreate: %v", err)
	}
	if err := h.validateCreate("rh-user-1", map[string]string{"email": "alice@example.com"}); err == nil {
		t.Fatal("missing mandatory tags should be rejected")
	}
}

func TestUsersHandlerRejectsUnsafeIDs(t *testing.T) {
	h := &UsersHandler{cfg: config.Config{RequiredUserTags: []string{"email"}}}
	if err := h.validateCreate("alice/example", map[string]string{"email": "alice@example.com"}); err == nil {
		t.Fatal("path separator should be rejected")
	}
}

func TestParseUsageWindow(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/usage/users?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z", nil)
	from, to, err := parseUsageWindow(r)
	if err != nil {
		t.Fatalf("parseUsageWindow: %v", err)
	}
	if from.Year() != 2026 || to.Month() != 10 {
		t.Fatalf("window = %s - %s", from, to)
	}
}

func TestRequirePartnerAPIAuthFailsClosedWhenUnconfigured(t *testing.T) {
	reached := false
	h := RequirePartnerAPIAuth("", func(http.ResponseWriter, *http.Request) { reached = true })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable || reached {
		t.Fatalf("status=%d reached=%v", w.Code, reached)
	}
}
