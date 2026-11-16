package handler

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/dashboard"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// DashboardCountryGate enforces the dashboard country policy.
//
// It wraps the session-authenticated routes (every route mounted behind
// RequireAuth) so the whole dashboard surface — pages and their backing JSON
// APIs — is gated in one place. Inference, key minting and ingestion never
// pass through it: keys keep working while the dashboard is region-gated.
//
// The gate fails open. A directory or policy read error is logged and the
// request is allowed: this policy is a staged rollout ("coming soon"), not a
// security boundary, and a database blip must not lock out every user.
type DashboardCountryGate struct {
	store *storage.Store
	cfg   config.Config
	page  []byte
	ttl   time.Duration

	mu       sync.Mutex
	blocked  map[string]struct{}
	loadedAt time.Time
}

// NewDashboardCountryGate builds the gate and loads the blocked-country page.
func NewDashboardCountryGate(store *storage.Store, cfg config.Config) *DashboardCountryGate {
	page, err := fs.ReadFile(dashboard.FS, "blocked.html")
	if err != nil {
		slog.Error("dashboard policy page missing", "error", err)
		page = []byte("<!doctype html><title>Coming soon</title><p>The dashboard is not available in your region yet. Coming soon.</p>")
	}
	return &DashboardCountryGate{store: store, cfg: cfg, page: page, ttl: 30 * time.Second}
}

// Invalidate drops the cached policy so an admin change applies immediately.
func (g *DashboardCountryGate) Invalidate() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadedAt = time.Time{}
}

// blockedCountries returns the cached policy set. On a read error the last
// good set is kept, and with no last good set the request is allowed.
func (g *DashboardCountryGate) blockedCountries(ctx context.Context) map[string]struct{} {
	if g == nil || g.store == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loadedAt.IsZero() && time.Since(g.loadedAt) < g.ttl {
		return g.blocked
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	countries, err := g.store.DashboardBlockedCountries(readCtx)
	if err != nil {
		slog.Error("dashboard country policy unavailable — allowing request", "error", err)
		return g.blocked
	}
	g.blocked = storage.NormalizeDashboardCountries(countries)
	g.loadedAt = time.Now()
	return g.blocked
}

// Wrap gates one session-authenticated handler.
func (g *DashboardCountryGate) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if g == nil || g.store == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// RequireAuth sets the identity header before this runs. Requests
		// without it are not session requests; the auth layer decides.
		user := strings.TrimSpace(r.Header.Get(g.cfg.UserHeader))
		if user == "" {
			next(w, r)
			return
		}
		blockedSet := g.blockedCountries(r.Context())
		if len(blockedSet) == 0 {
			// No country is blocked: skip the per-user lookups entirely.
			next(w, r)
			return
		}
		profile, err := g.store.PartnerUserByMaaSUsername(r.Context(), user)
		if err != nil {
			slog.Warn("dashboard country check skipped: directory lookup failed", "user", user, "error", err)
			next(w, r)
			return
		}
		country := storage.DashboardCountryOf(profile.Tags)
		if country == "" {
			// No country on file is allowed: the policy only blocks what the
			// directory explicitly places in a blocked region.
			next(w, r)
			return
		}
		if _, blocked := blockedSet[country]; !blocked {
			next(w, r)
			return
		}
		// Super-admins are exempt: they own the policy and must never be able
		// to lock themselves (and the policy tab) out of the dashboard. This
		// runs only for identities the policy would otherwise deny.
		if IsPartnerSuperAdmin(r.Context(), g.cfg, g.store, r) {
			next(w, r)
			return
		}
		g.deny(w, r)
	}
}

// deny answers a blocked identity. Browser navigations get the "coming soon"
// page; the dashboard's own JSON calls get a machine-readable 403 so the UI can
// render the same message without a redirect loop.
func (g *DashboardCountryGate) deny(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	api := strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.HasPrefix(r.URL.Path, "/me/") ||
		(r.Method != http.MethodGet && r.Method != http.MethodHead)
	if api {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"dashboard_unavailable_region","message":"The dashboard is not available in your region yet. Coming soon."}`)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(g.page)
}
