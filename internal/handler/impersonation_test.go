package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

func TestImpersonationStoresCanonicalLogin(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required")
	}
	store := openFreshStore(t, dsn)
	if _, err := store.ProvisionLogin(t.Context(), "test", storage.LoginProvision{Subject: "admin", Role: storage.PartnerRoleSuperAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProvisionLogin(t.Context(), "test", storage.LoginProvision{Subject: "alice", Role: storage.PartnerRoleUser}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DeploymentMode: config.DeploymentModeStandalone, UserHeader: "X-User", ManagementPublicOrigin: "https://app.example"}
	auth := NewAuthHandler(cfg)
	auth.SetOrgStore(store)
	cookie := httptest.NewRecorder()
	auth.setCookie(cookie, "admin", nil, "", "")
	// A case-variant target passes the case-insensitive directory lookup;
	// the session it produces must still key exact-match usage rows.
	r := httptest.NewRequest("POST", "/admin/impersonate?as=ALICE", nil)
	r.Header.Set("Cookie", cookie.Header().Get("Set-Cookie"))
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	auth.HandleImpersonate(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d; a super-admin must be able to switch views", w.Code)
	}
	var session sessionPayload
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name != cookieName {
			continue
		}
		found = true
		parts := strings.SplitN(c.Value, ".", 2)
		if len(parts) != 2 {
			t.Fatal("session cookie is not signed")
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			t.Fatalf("decode session payload: %v", err)
		}
		if err := json.Unmarshal(raw, &session); err != nil {
			t.Fatalf("parse session payload: %v", err)
		}
	}
	if !found {
		t.Fatal("no session cookie on the impersonation response")
	}
	if session.As != "alice" {
		t.Fatalf("impersonated login=%q; the session must carry the directory's stored spelling", session.As)
	}
}
