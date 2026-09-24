package handler

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/dashboard"
)

// placeholder hosts keep the page readable (in local development or any
// deployment that forgot the env) while making it obvious the URLs are
// stand-ins.
const (
	welcomeGatewayFallback   = "https://gateway.example.com"
	welcomeDashboardFallback = "https://dashboard.example.com"
)

// ServeWelcome renders the public onboarding page. It is intentionally
// unauthenticated — it is what you send to a new user before they have a
// key. The page template carries {{...}} placeholders instead of cluster
// hosts; the real endpoints are substituted here, at serve time, from the
// deployment environment (config.Welcome), so no cluster-specific URL ever
// enters the repository.
func (h *DashboardHandler) ServeWelcome(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(dashboard.FS, "welcome.html")
	if err != nil {
		http.Error(w, "welcome page not found", http.StatusInternalServerError)
		return
	}
	gateway := h.cfg.Welcome.GatewayURL
	if gateway == "" {
		gateway = welcomeGatewayFallback
	}
	dash := h.cfg.Welcome.DashboardURL
	if dash == "" {
		dash = welcomeDashboardFallback
	}
	page := strings.NewReplacer(
		"{{GATEWAY_URL}}", gateway,
		"{{DASHBOARD_URL}}", dash,
		"{{QUOTA_LABEL}}", quotaLabel(h.cfg.MonthlyTokenQuota),
	).Replace(string(data))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(page))
}

// quotaLabel renders a token quota compactly for the welcome page prose:
// 10000000000 -> "10B", 100000000 -> "100M".
func quotaLabel(q float64) string {
	var v float64
	var suffix string
	switch {
	case q >= 1e9:
		v, suffix = q/1e9, "B"
	case q >= 1e6:
		v, suffix = q/1e6, "M"
	case q >= 1e3:
		v, suffix = q/1e3, "K"
	default:
		v, suffix = q, ""
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if s == "" {
		s = "0"
	}
	return s + suffix
}
