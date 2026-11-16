package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestValidISOCountry(t *testing.T) {
	for _, code := range []string{"FR", "DE", "US", "GB", "TR"} {
		if !validISOCountry(code) {
			t.Errorf("%s should be a valid ISO-3166-1 alpha-2 code", code)
		}
	}
	for _, code := range []string{"", "F", "FRA", "fr", "XX", "1A", "F1"} {
		if validISOCountry(code) {
			t.Errorf("%q must not validate as an ISO-3166-1 alpha-2 code", code)
		}
	}
}

func TestISOCountryCodesSortedAndComplete(t *testing.T) {
	codes := isoCountryCodes()
	if len(codes) < 200 {
		t.Fatalf("expected the full ISO list, got %d codes", len(codes))
	}
	for i := 1; i < len(codes); i++ {
		if codes[i-1] > codes[i] {
			t.Fatalf("codes are not sorted at %d: %q > %q", i, codes[i-1], codes[i])
		}
	}
	found := map[string]bool{}
	for _, code := range codes {
		found[code] = true
	}
	for _, want := range []string{"FR", "DE", "US", "AQ"} {
		if !found[want] {
			t.Errorf("ISO list missing %s", want)
		}
	}
}

func TestDashboardGateDenyAPIAndPage(t *testing.T) {
	gate := &DashboardCountryGate{page: []byte("<html>coming soon</html>")}

	rec := httptest.NewRecorder()
	gate.deny(rec, httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("API status = %d, want 403", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("API content-type = %q, want JSON", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "dashboard_unavailable_region") {
		t.Errorf("API body = %q, want the machine-readable error", body)
	}

	rec = httptest.NewRecorder()
	gate.deny(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("page status = %d, want 403", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("page content-type = %q, want HTML", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "coming soon") {
		t.Errorf("page body = %q, want the blocked page", body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cache)
	}

	// A non-GET navigation-shaped request is not a page render: it must not
	// return the HTML page to an API client.
	rec = httptest.NewRecorder()
	gate.deny(rec, httptest.NewRequest(http.MethodPost, "/account", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("POST content-type = %q, want JSON", ct)
	}
}

func TestDashboardPolicyHandlerRejectsInvalidCountry(t *testing.T) {
	h := NewDashboardPolicyHandler(nil, config.Config{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/dashboard-policies",
		strings.NewReader(`{"country":"XX"}`))
	rec := httptest.NewRecorder()
	h.Handle(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown country", rec.Code)
	}
}

// The gate is mounted inside RequireAuth. A deployment without the policy
// store (or without the module) must pass every request through untouched.
func TestDashboardGatePassesThroughWithoutStore(t *testing.T) {
	called := false
	next := func(http.ResponseWriter, *http.Request) { called = true }

	(&DashboardCountryGate{}).Wrap(next)(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if !called {
		t.Fatal("nil-store gate must pass through")
	}
}
