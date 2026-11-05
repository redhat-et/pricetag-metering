package handler

import (
	"html"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/dashboard"
)

// The gateway fallback remains a visible local-development placeholder. The
// dashboard fallback is derived from the request host because welcome and
// dashboard are served by the same application and therefore share an origin.
const (
	welcomeGatewayFallback   = "https://gateway.example.com"
	welcomeDashboardFallback = "https://dashboard.example.com"
)

// ServeWelcome renders the public welcome page. It is intentionally
// unauthenticated — it is what you send to a new user before they have a
// key. The EnMaaS content lives in enmaas-welcome.html, the same fragment
// maintained for the Source page, and is injected into the welcome.html
// shell at serve time. The production gateway host in that fragment is
// replaced with the configured gateway so local and non-production
// deployments do not send users to production.
func (h *DashboardHandler) ServeWelcome(w http.ResponseWriter, r *http.Request) {
	page, err := fs.ReadFile(dashboard.FS, "welcome.html")
	if err != nil {
		http.Error(w, "welcome page not found", http.StatusInternalServerError)
		return
	}
	fragment, err := fs.ReadFile(dashboard.FS, "enmaas-welcome.html")
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
		// Host and X-Forwarded-Proto are request-controlled values. Never put
		// them into public HTML or use them to construct a clickable origin.
		// Deployments should set WELCOME_DASHBOARD_URL explicitly.
		dash = welcomeDashboardFallback
	}
	fragmentHTML := strings.ReplaceAll(
		string(fragment),
		"https://api.enmaas.devshift.net",
		html.EscapeString(gateway),
	)
	pageHTML := strings.ReplaceAll(string(page), "{{ENMAAS_WELCOME}}", fragmentHTML)
	pageHTML = strings.NewReplacer(
		"{{GATEWAY_URL}}", html.EscapeString(gateway),
		"{{DASHBOARD_URL}}", html.EscapeString(dash),
		"{{QUOTA_LABEL}}", html.EscapeString(quotaLabel(h.cfg.MonthlyTokenQuota)),
	).Replace(pageHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(pageHTML))
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
