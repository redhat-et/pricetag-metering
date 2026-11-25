package handler

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

func TestNormalizeGroupsDropsTechnicalRoles(t *testing.T) {
	got := NormalizeGroups([]string{
		"GE", " offline_access ", "uma_authorization", "default-roles-stage",
		"enmaas_authorization", "ge", "", "Platform",
	})
	want := []string{"GE", "Platform"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeGroups = %#v, want %#v", got, want)
	}
}

func TestSameOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "http://dashboard.example/api", nil)
	r.Host = "dashboard.example"
	if !sameOrigin(r) {
		t.Fatal("missing Origin should be allowed for non-browser clients")
	}
	r.Header.Set("Origin", "http://dashboard.example")
	if !sameOrigin(r) {
		t.Fatal("matching Origin should be allowed")
	}
	r.Header.Set("Origin", "http://evil.example")
	if sameOrigin(r) {
		t.Fatal("cross-origin request must be refused")
	}
}

func TestGateGroupsPrefersDirectoryRoverGroups(t *testing.T) {
	tags := map[string]any{
		"rover_groups": []any{"enmaas-users", "other-group"},
		"groups":       []string{"GE", "platform"},
	}
	got := gateGroups([]string{"GE"}, tags)
	want := []string{"enmaas-users", "other-group"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gateGroups = %#v, want %#v", got, want)
	}
}

func TestGateGroupsEmptyDirectoryListDoesNotFallBack(t *testing.T) {
	tags := map[string]any{
		"rover_groups": []any{},
		"groups":       []string{"GE"},
	}
	if got := gateGroups([]string{"GE"}, tags); len(got) != 0 {
		t.Fatalf("gateGroups = %#v, want no groups", got)
	}
}

func TestGateGroupsFallsBackToOIDCClaim(t *testing.T) {
	tags := map[string]any{"groups": []any{"GE", " offline_access ", "platform"}}
	got := gateGroups([]string{"session-group"}, tags)
	want := []string{"GE", "platform", "session-group"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gateGroups = %#v, want %#v", got, want)
	}
}

func TestTagStringListEncodings(t *testing.T) {
	cases := map[string]any{
		"array":       []any{"GE", "platform"},
		"json string": `["GE","platform"]`,
		"csv":         "GE, platform",
	}
	for name, value := range cases {
		got := tagStringList(value)
		if len(got) != 2 || got[0] != "GE" || got[1] != "platform" {
			t.Errorf("%s: tagStringList = %#v", name, got)
		}
	}
}

func TestWorksCouncilOverride(t *testing.T) {
	for _, tags := range []map[string]any{
		{"works_council_override": true},
		{"works_council_override": "true"},
		{"has_works_council_override": "yes"},
	} {
		if !hasWorksCouncilOverride(tags) {
			t.Errorf("expected override for %#v", tags)
		}
	}
	if hasWorksCouncilOverride(map[string]any{"works_council_override": "false"}) {
		t.Fatal("false override must not pass")
	}
}

func TestRequireAccountRedirectsForPendingTermsInObserve(t *testing.T) {
	store, ctx := openHandlerTestStore(t)
	const username = "observe-terms@example.com"
	userID := uuid.NewString()
	if _, err := store.CreatePartnerUser(ctx, "test", userID, map[string]any{
		"email": username, "first_name": "Observe", "last_name": "Terms", "country": "US",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		UserHeader:   "X-Auth-Username",
		GroupsHeader: "X-Auth-Groups",
		Onboarding:   config.Onboarding{Mode: config.OnboardingObserve, TermsVersion: "v1"},
		Access:       config.Access{RequiredGroups: []string{"enmaas-users"}},
	}
	gate := NewAccessGate(cfg, store)
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/account", nil)
	req.Header.Set(cfg.UserHeader, username)
	req.Header.Set(cfg.GroupsHeader, `["enmaas-users"]`)
	gate.RequireAccount(next)(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/onboarding" {
		t.Fatalf("pending terms must redirect to onboarding in observe: code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}

	if _, err := store.RecordAgreement(ctx, storage.Agreement{UserID: userID, DocType: "terms", Version: "v1", Method: "test"}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	gate.RequireAccount(next)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("observed-ready user must reach the account page: %d", rec.Code)
	}
}

func TestNormalizeGroupsHandlesDNsAndPaths(t *testing.T) {
	got := NormalizeGroups([]string{
		"CN=enmaas-users,OU=Groups,DC=redhat,DC=com",
		"/enmaas-users",
		" enmaas-users ",
		"CN=Other Group,OU=Groups,DC=redhat,DC=com",
	})
	want := []string{"enmaas-users", "Other Group"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeGroups = %#v, want %#v", got, want)
	}
}

func TestTagStringListKeepsDNsIntact(t *testing.T) {
	raw := "CN=enmaas-users,OU=Groups,DC=redhat,DC=com\nCN=other,OU=Groups,DC=redhat,DC=com"
	got := NormalizeGroups(tagStringList(raw))
	want := []string{"enmaas-users", "other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DN memberOf list = %#v, want %#v", got, want)
	}
}
