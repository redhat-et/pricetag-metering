package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func testCfg() config.Config {
	return config.Config{
		UserHeader:   "X-Forwarded-User",
		GroupsHeader: "X-Forwarded-Groups",
		DefaultGroup: "default",
	}
}

// caller must resolve the swapped identity for scope math during view-as.
func TestCaller_ImpersonationScope(t *testing.T) {
	cfg := testCfg()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(cfg.UserHeader, "viewed-user")
	r.Header.Set(realUserHeader, "boss")
	if c := caller(r, cfg); c != "viewed-user" {
		t.Errorf("caller must be the swapped identity for scope math, got %q", c)
	}
}

// ApplyScope: an anonymous caller (no store, no user) must be rejected.
func TestApplyScope_AnonymousRejected(t *testing.T) {
	cfg := testCfg()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	if _, ok := ApplyScope(w, r, nil, cfg, ""); ok {
		t.Error("anonymous caller must be rejected")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous rejection should be 401, got %d", w.Code)
	}
}
