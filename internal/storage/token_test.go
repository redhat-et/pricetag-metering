package storage

import (
	"testing"
)

func TestRoleTokenBudgetAndCredits(t *testing.T) {
	s, ctx := openTestStore(t)
	tenantID, _ := s.EnsureDefaultTenant(ctx)
	// seed global policy default 10B
	if _, err := s.db.ExecContext(ctx, `UPDATE quota_policy SET default_monthly_tokens = 10000000000 WHERE id = true`); err != nil {
		t.Fatalf("seed global tokens: %v", err)
	}
	// create partner user stub via direct insert then subject sync
	userID := "11111111-1111-4111-8111-111111111111"
	if _, err := s.db.ExecContext(ctx, `INSERT INTO partner_users (user_id, active, role, tags, created_by, updated_by) VALUES ($1, true, 'user', '{"email":"tokentest@example.com"}'::jsonb, 'test', 'test') ON CONFLICT (user_id) DO UPDATE SET active=true`, userID); err != nil {
		t.Fatalf("partner user: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO partner_user_logins (user_id, username, is_current) VALUES ($1, 'tokentest@example.com', true) ON CONFLICT DO NOTHING`, userID); err != nil {
		t.Fatalf("login: %v", err)
	}
	subjectID, _ := s.SyncPartnerUserSubject(ctx, tenantID, userID)

	// role with 15B token limit
	roleA, err := s.CreateModelAccessRoleWithTokens(ctx, tenantID, "tester", "RoleA", "test", []string{"claude-opus-4-8"}, false, 0, 15_000_000_000)
	if err != nil {
		t.Fatalf("create role A: %v", err)
	}
	if err := s.AssignModelAccessRole(ctx, tenantID, roleA.RoleID, subjectID, "tester"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	got, cred, err := s.GetRoleTokenBudgetAndCredits(ctx, tenantID, subjectID)
	if err != nil || got != 15_000_000_000 || cred != 0 {
		t.Fatalf("role token budget = %d cred %d err %v; want 15000000000/0", got, cred, err)
	}
	// add token credit 5B temporary
	if _, err := s.AddTokenCredit(ctx, tenantID, subjectID, 5_000_000_000, nil, "extra", "tester", "req-1"); err != nil {
		t.Fatalf("add token credit: %v", err)
	}
	got, cred, err = s.GetRoleTokenBudgetAndCredits(ctx, tenantID, subjectID)
	if err != nil || got != 15_000_000_000 || cred != 5_000_000_000 {
		t.Fatalf("after credit budget=%d cred=%d err=%v want 15000000000/5000000000", got, cred, err)
	}
	// per-person permanent override should be via quota_token_overrides; test that quotaDecision honors it
	if err := s.UpsertTokenOverride(ctx, "tester", "tokentest@example.com", 20_000_000_000); err != nil {
		t.Fatalf("upsert token override: %v", err)
	}
	dec, err := s.quotaDecision(ctx, "tokentest@example.com", false)
	if err != nil {
		t.Fatalf("quotaDecision: %v", err)
	}
	// user_override absolute 20B + 5B credit = 25B
	if dec.BaseTokens != 20_000_000_000 || dec.TokenLimit != 25_000_000_000 || dec.TokenSource != "user_override" {
		t.Fatalf("token decision = base %d limit %d source %s; want 20B/25B/user_override", dec.BaseTokens, dec.TokenLimit, dec.TokenSource)
	}
}
