package handler

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// DashboardPolicyHandler is the super-admin surface for the dashboard country
// policy. It lists the blocked ISO-3166-1 alpha-2 codes, adds and removes
// them, and previews how many active directory users a change affects.
//
// The route is mounted behind RequireAuth + RequirePartnerSuperAdmin, and the
// dashboard gate exempts super-admins, so policy changes can never lock the
// operator out of the tab that manages them.
type DashboardPolicyHandler struct {
	store *storage.Store
	cfg   config.Config
	gate  *DashboardCountryGate
	// onChange runs after a successful policy change so cached dashboard
	// responses (manager tables and totals) apply it immediately.
	onChange func()
}

// NewDashboardPolicyHandler wires the policy API. gate is optional; when set,
// a policy change invalidates the gate's cached country set immediately.
func NewDashboardPolicyHandler(store *storage.Store, cfg config.Config, gate *DashboardCountryGate) *DashboardPolicyHandler {
	return &DashboardPolicyHandler{store: store, cfg: cfg, gate: gate}
}

// SetOnChange registers a post-change hook (the manager-response cache).
func (h *DashboardPolicyHandler) SetOnChange(fn func()) {
	h.onChange = fn
}

func (h *DashboardPolicyHandler) changed() {
	if h.onChange != nil {
		h.onChange()
	}
}

type dashboardPolicyResponse struct {
	Blocked             []string `json:"blocked"`
	EnmaasBlocked       []string `json:"enmaas_blocked"`
	RequiredGroups      []string `json:"required_groups"`
	AllowUnknownCountry bool     `json:"allow_unknown_country"`
	ISO                 []string `json:"iso"`
	Impacted            int      `json:"impacted"`
	People              int      `json:"people"`
}

func (h *DashboardPolicyHandler) response(w http.ResponseWriter, r *http.Request) {
	blocked, err := h.store.DashboardBlockedCountries(r.Context())
	if err != nil {
		slog.Error("dashboard policy list failed", "error", err)
		http.Error(w, "policy query failed", http.StatusInternalServerError)
		return
	}
	impacted, people, err := h.store.DashboardPolicyImpact(r.Context())
	if err != nil {
		slog.Error("dashboard policy impact failed", "error", err)
		http.Error(w, "policy query failed", http.StatusInternalServerError)
		return
	}
	enmaasBlocked, err := h.store.EnmaasBlockedCountries(r.Context())
	if err != nil {
		slog.Error("enmaas policy list failed", "error", err)
		http.Error(w, "policy query failed", http.StatusInternalServerError)
		return
	}
	policy, err := h.store.GetAccessPolicy(r.Context())
	if err != nil {
		slog.Error("access policy read failed", "error", err)
		http.Error(w, "policy query failed", http.StatusInternalServerError)
		return
	}
	groups := policy.RequiredGroups
	allowUnknown := policy.AllowUnknownCountry
	if !policy.Configured {
		groups = h.cfg.Access.RequiredGroups
		allowUnknown = h.cfg.Access.AllowUnknownCountry
	}
	writeJSON(w, dashboardPolicyResponse{
		Blocked:             storage.SortedCountries(blocked),
		EnmaasBlocked:       storage.SortedCountries(enmaasBlocked),
		RequiredGroups:      groups,
		AllowUnknownCountry: allowUnknown,
		ISO:                 isoCountryCodes(),
		Impacted:            impacted,
		People:              people,
	})
}

// Handle serves GET (read the policy) and POST (update one country's
// dashboard and EnMaaS eligibility). POST keeps its historical shape:
// {country: "NL"} blocks the dashboard, while the admin UI sends both flags.
func (h *DashboardPolicyHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.response(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Country   string `json:"country"`
		Dashboard *bool  `json:"dashboard"`
		Enmaas    *bool  `json:"enmaas"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	country := strings.ToUpper(strings.TrimSpace(body.Country))
	if !validISOCountry(country) {
		http.Error(w, "country must be an ISO 3166-1 alpha-2 code", http.StatusBadRequest)
		return
	}
	// Apply only the flags the caller sent; a bare {country} keeps the legacy
	// meaning of "block the dashboard" (older clients), which is also what an
	// admin tab left open across the deploy sends.
	dashboardBlocked := true
	if body.Dashboard != nil {
		dashboardBlocked = *body.Dashboard
	}
	applyDashboard := body.Dashboard != nil || body.Enmaas == nil
	applyEnmaas := body.Enmaas != nil
	actor := caller(r, h.cfg)
	changedDashboard := false
	if applyDashboard {
		if dashboardBlocked {
			added, err := h.store.AddDashboardBlockedCountry(r.Context(), actor, country)
			if err != nil {
				slog.Error("dashboard policy add failed", "country", country, "error", err)
				http.Error(w, "policy update failed", http.StatusInternalServerError)
				return
			}
			changedDashboard = added
		} else {
			removed, err := h.store.RemoveDashboardBlockedCountry(r.Context(), actor, country)
			if err != nil {
				slog.Error("dashboard policy remove failed", "country", country, "error", err)
				http.Error(w, "policy update failed", http.StatusInternalServerError)
				return
			}
			changedDashboard = removed
		}
	}
	changedEnmaas := false
	if applyEnmaas {
		// Both flags carry the *blocked* state: dashboard preserves its legacy
		// bare-{country} meaning, enmaas mirrors it for the admin matrix.
		blockedEnmaas := *body.Enmaas
		changed, err := h.store.SetEnmaasCountryBlocked(r.Context(), actor, country, blockedEnmaas)
		if err != nil {
			slog.Error("enmaas policy update failed", "country", country, "error", err)
			http.Error(w, "policy update failed", http.StatusInternalServerError)
			return
		}
		changedEnmaas = changed
	}
	if (changedDashboard || changedEnmaas) && h.gate != nil {
		h.gate.Invalidate()
	}
	if changedDashboard || changedEnmaas {
		h.changed()
	}
	h.response(w, r)
}

// HandlePolicy serves PUT /api/v1/admin/dashboard-policies/policy: the EnMaaS
// guest-list groups and the unknown-country verdict.
func (h *DashboardPolicyHandler) HandlePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		RequiredGroups      []string `json:"required_groups"`
		AllowUnknownCountry *bool    `json:"allow_unknown_country"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	current, err := h.store.GetAccessPolicy(r.Context())
	if err != nil {
		http.Error(w, "policy query failed", http.StatusInternalServerError)
		return
	}
	allowUnknown := current.AllowUnknownCountry
	if body.AllowUnknownCountry != nil {
		allowUnknown = *body.AllowUnknownCountry
	} else if !current.Configured {
		allowUnknown = h.cfg.Access.AllowUnknownCountry
	}
	groups := body.RequiredGroups
	if groups == nil {
		groups = current.RequiredGroups
		if !current.Configured {
			groups = h.cfg.Access.RequiredGroups
		}
	}
	actor := caller(r, h.cfg)
	if _, err := h.store.SaveAccessPolicy(r.Context(), storage.AccessPolicy{
		RequiredGroups:      groups,
		AllowUnknownCountry: allowUnknown,
	}, actor); err != nil {
		slog.Error("save access policy failed", "error", err)
		http.Error(w, "policy update failed", http.StatusInternalServerError)
		return
	}
	_ = h.store.RecordAccessAudit(r.Context(), "", actor, "access_policy_updated", "", "", map[string]any{
		"required_groups":       groups,
		"allow_unknown_country": allowUnknown,
	})
	h.response(w, r)
}

// HandleCountry serves DELETE /api/v1/admin/dashboard-policies/{CC}.
func (h *DashboardPolicyHandler) HandleCountry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	country := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/dashboard-policies/")))
	if !validISOCountry(country) {
		http.Error(w, "country must be an ISO 3166-1 alpha-2 code", http.StatusBadRequest)
		return
	}
	removed, err := h.store.RemoveDashboardBlockedCountry(r.Context(), caller(r, h.cfg), country)
	if err != nil {
		slog.Error("dashboard policy remove failed", "country", country, "error", err)
		http.Error(w, "policy update failed", http.StatusInternalServerError)
		return
	}
	if removed && h.gate != nil {
		h.gate.Invalidate()
	}
	if removed {
		h.changed()
	}
	h.response(w, r)
}

// validISOCountry accepts any ISO-3166-1 alpha-2 code the platform knows. The
// directory and the IdP carry alpha-2 codes, so that is the policy vocabulary.
func validISOCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	_, ok := iso2ToISO3[country]
	return ok
}

// isoCountryCodes returns every selectable alpha-2 code, sorted, so the admin
// tab can offer the full list instead of a hard-coded subset.
func isoCountryCodes() []string {
	out := make([]string, 0, len(iso2ToISO3))
	for code := range iso2ToISO3 {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
