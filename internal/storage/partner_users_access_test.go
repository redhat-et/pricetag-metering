package storage

import "testing"

func TestDashboardTagsAllowlist(t *testing.T) {
	got := DashboardTags(map[string]any{
		"email": "a@example.com", "country": "US", "manager_uuid": "m",
		"internal_secret": "do-not-display",
	})
	if got["email"] != "a@example.com" || got["country"] != "US" || got["manager_uuid"] != "m" {
		t.Fatalf("allowed partner tags missing: %#v", got)
	}
	if _, ok := got["internal_secret"]; ok {
		t.Fatalf("sensitive partner tag leaked: %#v", got)
	}
}
