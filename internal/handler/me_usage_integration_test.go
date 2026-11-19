package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// The self view's "Saved · Hosted Models" card must come from the same
// server-side savings CTE the admin overview uses. HandleSummary used to omit
// it, so the tile always rendered $0. It needs a Postgres (DATABASE_URL);
// otherwise it skips.
func TestMeUsageSummaryIncludesHostedSavings(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — self-usage savings test needs a Postgres")
	}
	store := openFreshStore(t, dsn)
	ctx := context.Background()

	const (
		login  = "self-savings@example.com"
		userID = "5d5c1b8e-1111-4222-8333-444455556666"
	)
	if _, err := store.CreatePartnerUser(ctx, "test-admin", userID, map[string]any{
		"email": login, "first_name": "Self", "last_name": "Savings",
	}); err != nil {
		t.Fatalf("CreatePartnerUser: %v", err)
	}

	// Hosted model priced well below the reference model, plus list prices so
	// the savings comparison has a vendor baseline.
	if _, err := store.SeedPricing(ctx, []storage.ModelPrice{
		{Model: "test-hosted", Provider: "vllm", InputCost: 0.5, OutputCost: 2},
		{Model: "claude-opus-4-8", Provider: "anthropic", InputCost: 15, OutputCost: 75},
	}); err != nil {
		t.Fatalf("SeedPricing: %v", err)
	}
	if _, err := store.SeedListPricing(ctx, []storage.ModelPrice{
		{Model: "test-hosted", ListInputCost: 15, ListOutputCost: 75},
	}); err != nil {
		t.Fatalf("SeedListPricing: %v", err)
	}

	if err := store.InsertEvent(ctx, storage.UsageEvent{
		EventID:          "self-savings-1",
		Timestamp:        time.Now().UTC(),
		Username:         login,
		GroupName:        "test",
		Provider:         "vllm",
		Model:            "test-hosted",
		PromptTokens:     10000,
		CompletionTokens: 5000,
		TotalTokens:      15000,
		Source:           "test",
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	cfg := config.Config{UserHeader: "X-Forwarded-User"}
	h := NewMeUsageHandler(store, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/usage/summary?range=mtd&ref=claude-opus-4-8", nil)
	req.Header.Set(cfg.UserHeader, login)
	rec := httptest.NewRecorder()
	h.HandleSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Overview storage.DashboardOverview `json:"overview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if resp.Overview.TotalRequests != 1 || resp.Overview.TotalTokens != 15000 {
		t.Fatalf("overview requests/tokens = %d/%d, want 1/15000",
			resp.Overview.TotalRequests, resp.Overview.TotalTokens)
	}
	if resp.Overview.TotalCostUSD <= 0 {
		t.Fatalf("overview cost = %v, want > 0", resp.Overview.TotalCostUSD)
	}
	// (10000*15 + 5000*75)/1e6 - (10000*0.5 + 5000*2)/1e6 = 0.525 - 0.015 = 0.51
	if resp.Overview.SavedUSD <= 0 {
		t.Fatalf("saved_usd = %v, want the hosted savings (0.51), not zero", resp.Overview.SavedUSD)
	}
	if got := resp.Overview.SavedUSD; got < 0.50 || got > 0.52 {
		t.Fatalf("saved_usd = %v, want ~0.51", got)
	}
}
