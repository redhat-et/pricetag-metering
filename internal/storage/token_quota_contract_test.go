package storage

import (
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

// The token gate's "non-positive means unlimited" contract spans three layers:
// the README documents it, internal/config loads the value, and
// computeUsageStats implements it. Each layer was tested on its own, so a loader
// that could not produce a non-positive quota left the unlimited branch in
// computeUsageStats covered but unreachable.
//
// This test crosses the boundary the way cmd/main.go wires them
// (storage.New(cfg.DatabaseURL, int64(cfg.MonthlyTokenQuota), ...)), so a future
// change to either parser is caught here rather than in production.
func TestMonthlyTokenQuotaUnlimitedSurvivesConfigLoad(t *testing.T) {
	t.Setenv("MONTHLY_TOKEN_QUOTA", "0")
	quota := int64(config.Load().MonthlyTokenQuota)
	if quota > 0 {
		t.Fatalf("MONTHLY_TOKEN_QUOTA=0 loaded as %d, want <= 0 so the gate is off", quota)
	}
	if got := computeUsageStats(1_000_000_000_000, quota); !got.HasAccess {
		t.Fatalf("unlimited quota denied a 1T-token month: %+v", got)
	}

	// "Unlimited" must not leak into the normal path: a real quota still gates.
	t.Setenv("MONTHLY_TOKEN_QUOTA", "1000")
	quota = int64(config.Load().MonthlyTokenQuota)
	if over := computeUsageStats(1500, quota); over.HasAccess {
		t.Fatalf("quota %d admitted 1500 used tokens: %+v", quota, over)
	}
	under := computeUsageStats(500, quota)
	if !under.HasAccess || under.Balance != 500 {
		t.Fatalf("quota %d with 500 used = %+v, want access and 500 balance", quota, under)
	}
}
