package storage

import (
	"strings"
	"testing"
)

// Manager drill-downs are read-only surfaces; they must use the hourly
// rollup when it is live instead of re-scanning the raw ledger per person.
func TestManagerReadsUseHourlyRollupsWhenLive(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"partner_org.go", "func (s *Store) GetPersonModelUsage"},
	} {
		body := funcBody(t, tc.file, tc.fn)
		if !strings.Contains(body, "s.rollupsLive()") {
			t.Errorf("%s no longer gates on rollupsLive()", tc.fn)
		}
		if !strings.Contains(body, "usage_hourly e") {
			t.Errorf("%s lost its hourly rollup source", tc.fn)
		}
		if !strings.Contains(body, "usage_events e") {
			t.Errorf("%s lost its raw fallback source", tc.fn)
		}
	}
}

// Filtered recent-activity lookups must not walk the timestamp index back
// through the whole ledger when the filter has no recent matches.
func TestFilteredRecentHasBoundedWindow(t *testing.T) {
	body := funcBody(t, "postgres.go", "func (s *Store) GetRecentEvents")
	for _, want := range []string{"recentActivityWindow", "e.timestamp >= $5", "filtered :="} {
		if !strings.Contains(body, want) {
			t.Errorf("GetRecentEvents missing bounded-window guard %q", want)
		}
	}
}

func TestUsageGroupIndexRunsOutsideStartupMigrations(t *testing.T) {
	want := "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_events_group_ts ON usage_events (group_name, timestamp DESC)"
	if usageGroupActivityIndexDDL != want {
		t.Fatalf("index DDL drifted: %s", usageGroupActivityIndexDDL)
	}
	all := strings.Join(migrations, "\n")
	if strings.Contains(all, "CONCURRENTLY") {
		t.Fatal("a CONCURRENTLY build must not run under the fixed startup migration timeout")
	}
	if !strings.Contains(all, "idx_usage_events_group ON usage_events (group_name)") {
		t.Fatal("the plain group index should remain for non-time-scoped predicates")
	}
}
