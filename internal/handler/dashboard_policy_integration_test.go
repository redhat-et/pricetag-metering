package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

// The admin matrix sends both flags; the handler must apply only what is sent
// and must not couple the two policies through a default.
func TestDashboardPolicyMatrixFlags(t *testing.T) {
	store, _ := openHandlerTestStore(t)
	ctx := context.Background()
	h := NewDashboardPolicyHandler(store, config.Config{UserHeader: "X-Auth-Username"}, nil)

	post := func(body map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/dashboard-policies", bytes.NewReader(raw))
		req.Header.Set("X-Auth-Username", "admin@example.com")
		rec := httptest.NewRecorder()
		h.Handle(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %v = %d: %s", body, rec.Code, rec.Body.String())
		}
	}

	state := func(country string) (dash, enm bool) {
		t.Helper()
		d, err := store.DashboardCountryBlocked(ctx, country)
		if err != nil {
			t.Fatal(err)
		}
		e, err := store.EnmaasCountryBlocked(ctx, country)
		if err != nil {
			t.Fatal(err)
		}
		return d, e
	}

	const cc = "US"
	post(map[string]any{"country": cc, "dashboard": true, "enmaas": false})
	if d, e := state(cc); !d || e {
		t.Fatalf("dashboard-only block = (%v,%v), want (true,false)", d, e)
	}
	post(map[string]any{"country": cc, "dashboard": false, "enmaas": true})
	if d, e := state(cc); d || !e {
		t.Fatalf("enmaas-only block = (%v,%v), want (false,true)", d, e)
	}
	post(map[string]any{"country": cc, "dashboard": false, "enmaas": false})
	if d, e := state(cc); d || e {
		t.Fatalf("cleared = (%v,%v), want (false,false)", d, e)
	}
	post(map[string]any{"country": cc, "dashboard": true, "enmaas": true})
	if d, e := state(cc); !d || !e {
		t.Fatalf("both blocked = (%v,%v), want (true,true)", d, e)
	}
}
