package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestOrgCacheSeparatesCallersAndHonoursBypass(t *testing.T) {
	cfg := config.Config{
		DashboardCacheEnabled:    true,
		DashboardCacheTTLSeconds: 60,
		UserHeader:               "X-Forwarded-User",
	}
	c := newOrgResponseCache(cfg)
	calls := 0
	next := func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[]}`))
	}
	h := c.wrap(next)

	do := func(user, cacheControl string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/org/usage?range=7d", nil)
		req.Header.Set("X-Forwarded-User", user)
		if cacheControl != "" {
			req.Header.Set("Cache-Control", cacheControl)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}

	if rec := do("alice", ""); rec.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rec.Code)
	}
	if rec := do("alice", ""); rec.Code != http.StatusOK {
		t.Fatalf("cached call status = %d", rec.Code)
	}
	if calls != 1 {
		t.Fatalf("same caller+query must hit the cache: calls = %d", calls)
	}
	do("bob", "")
	if calls != 2 {
		t.Fatalf("a different caller must not share an entry: calls = %d", calls)
	}
	do("alice", "no-cache")
	if calls != 3 {
		t.Fatalf("Cache-Control: no-cache must bypass the cache: calls = %d", calls)
	}
}

func TestOrgCacheDoesNotStoreErrors(t *testing.T) {
	cfg := config.Config{
		DashboardCacheEnabled:    true,
		DashboardCacheTTLSeconds: 60,
		UserHeader:               "X-Forwarded-User",
	}
	c := newOrgResponseCache(cfg)
	calls := 0
	h := c.wrap(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/org/charts?range=7d", nil)
	req.Header.Set("X-Forwarded-User", "alice")
	h(httptest.NewRecorder(), req)
	h(httptest.NewRecorder(), req)
	if calls != 2 {
		t.Fatalf("non-200 responses must never be cached: calls = %d", calls)
	}
}
