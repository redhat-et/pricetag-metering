package storage

import "testing"

func TestAgreementAndAccessPolicyRoundTrip(t *testing.T) {
	s, ctx := openTestStore(t)
	const userID = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"

	if _, err := s.CreatePartnerUser(ctx, "test", userID, map[string]any{
		"email": "terms@example.com", "first_name": "Terms", "last_name": "Tester",
		"manager_uuid": nil, "country": "US",
	}); err != nil {
		t.Fatalf("CreatePartnerUser: %v", err)
	}

	accepted, err := s.HasAgreement(ctx, userID, "terms", "v1")
	if err != nil {
		t.Fatalf("HasAgreement: %v", err)
	}
	if accepted {
		t.Fatal("agreement should not exist before acceptance")
	}

	inserted, err := s.RecordAgreement(ctx, Agreement{
		UserID: userID, DocType: "terms", Version: "v1",
		IP: "127.0.0.1", UserAgent: "test", Method: "oidc",
	})
	if err != nil || !inserted {
		t.Fatalf("RecordAgreement inserted=%v err=%v", inserted, err)
	}
	// Re-accepting the same version is a no-op, not a duplicate or an error.
	inserted, err = s.RecordAgreement(ctx, Agreement{
		UserID: userID, DocType: "terms", Version: "v1", Method: "oidc",
	})
	if err != nil || inserted {
		t.Fatalf("duplicate RecordAgreement inserted=%v err=%v", inserted, err)
	}
	accepted, err = s.HasAgreement(ctx, userID, "terms", "v1")
	if err != nil || !accepted {
		t.Fatalf("HasAgreement after accept = %v, err %v", accepted, err)
	}
	// A new version needs a fresh acceptance.
	accepted, err = s.HasAgreement(ctx, userID, "terms", "v2")
	if err != nil || accepted {
		t.Fatalf("HasAgreement v2 = %v, err %v", accepted, err)
	}

	if _, err := s.SaveAccessPolicy(ctx, AccessPolicy{
		RequiredGroups:      []string{"enmaas-users", " enmaas-users ", ""},
		AllowUnknownCountry: true,
	}, "admin@example.com"); err != nil {
		t.Fatalf("SaveAccessPolicy: %v", err)
	}
	policy, err := s.GetAccessPolicy(ctx)
	if err != nil {
		t.Fatalf("GetAccessPolicy: %v", err)
	}
	if !policy.Configured {
		t.Fatal("policy should be configured after save")
	}
	if len(policy.RequiredGroups) != 1 || policy.RequiredGroups[0] != "enmaas-users" {
		t.Fatalf("required groups = %#v", policy.RequiredGroups)
	}
	if !policy.AllowUnknownCountry || policy.UpdatedBy != "admin@example.com" {
		t.Fatalf("policy metadata = %#v", policy)
	}

	// EnMaaS country list is independent from the dashboard list. A fresh
	// database seeds NL, so clear it first and then exercise both directions.
	if blocked, err := s.EnmaasCountryBlocked(ctx, "NL"); err != nil || !blocked {
		t.Fatalf("fresh database should seed NL as blocked (%v, %v)", blocked, err)
	}
	if changed, err := s.SetEnmaasCountryBlocked(ctx, "test", "NL", false); err != nil || !changed {
		t.Fatalf("unblock seeded NL changed=%v err=%v", changed, err)
	}
	if changed, err := s.SetEnmaasCountryBlocked(ctx, "test", "NL", true); err != nil || !changed {
		t.Fatalf("block NL changed=%v err=%v", changed, err)
	}
	if blocked, err := s.EnmaasCountryBlocked(ctx, "nl"); err != nil || !blocked {
		t.Fatalf("NL should be blocked (%v, %v)", blocked, err)
	}
	if blocked, err := s.EnmaasCountryBlocked(ctx, "US"); err != nil || blocked {
		t.Fatalf("US should not be blocked (%v, %v)", blocked, err)
	}
	if changed, err := s.SetEnmaasCountryBlocked(ctx, "test", "NL", false); err != nil || !changed {
		t.Fatalf("unblock NL changed=%v err=%v", changed, err)
	}
	if blocked, err := s.EnmaasCountryBlocked(ctx, "NL"); err != nil || blocked {
		t.Fatalf("NL should be unblocked after clear (%v, %v)", blocked, err)
	}
}
