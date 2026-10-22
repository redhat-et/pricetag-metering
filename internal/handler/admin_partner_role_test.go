package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

type staticPartnerRoleLookup struct {
	role string
	err  error
}

func (l staticPartnerRoleLookup) GetPartnerRoleByUsername(context.Context, string) (string, error) {
	return l.role, l.err
}

// The partner directory is authoritative when it has a row. A missing row is
// different from an explicit user role: it falls through to the configured
// break-glass lists so dogfood/test environments work before the directory is
// seeded. See the ErrPartnerUserNotFound branch in
// GetPartnerRoleByUsername.
func TestPartnerAdminFallbackWhenDirectoryRowMissing(t *testing.T) {
	cfg := config.Config{
		AdminUsers:      []string{"admin@example.com"},
		SuperAdminUsers: []string{"super@example.com"},
	}
	missing := staticPartnerRoleLookup{err: storage.ErrPartnerUserNotFound}
	ctx := context.Background()

	cases := []struct {
		name     string
		username string
		admin    bool
		super    bool
	}{
		{name: "configured admin", username: "ADMIN@example.com", admin: true},
		{name: "configured super-admin is also admin", username: "super@example.com", admin: true, super: true},
		{name: "unlisted user", username: "user@example.com"},
		{name: "no identity", username: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPartnerAdminUsername(ctx, cfg, missing, tc.username); got != tc.admin {
				t.Errorf("isPartnerAdminUsername(%q) = %v, want %v", tc.username, got, tc.admin)
			}
			if got := isPartnerSuperAdminUsername(ctx, cfg, missing, tc.username); got != tc.super {
				t.Errorf("isPartnerSuperAdminUsername(%q) = %v, want %v", tc.username, got, tc.super)
			}
		})
	}
}

// A stored role must override the configured lists, including the explicit
// default user role. This is the security property the original break-glass
// fallback was designed to preserve.
func TestPartnerDirectoryRoleOverridesBreakGlass(t *testing.T) {
	cfg := config.Config{
		AdminUsers:      []string{"canonical@example.com"},
		SuperAdminUsers: []string{"canonical@example.com"},
	}
	ctx := context.Background()
	username := "canonical@example.com"

	cases := []struct {
		name  string
		role  string
		admin bool
		super bool
	}{
		{name: "explicit user role", role: storage.PartnerRoleUser},
		{name: "stored admin", role: storage.PartnerRoleAdmin, admin: true},
		{name: "stored super-admin", role: storage.PartnerRoleSuperAdmin, admin: true, super: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := staticPartnerRoleLookup{role: tc.role}
			if got := isPartnerAdminUsername(ctx, cfg, lookup, username); got != tc.admin {
				t.Errorf("isPartnerAdminUsername = %v, want %v", got, tc.admin)
			}
			if got := isPartnerSuperAdminUsername(ctx, cfg, lookup, username); got != tc.super {
				t.Errorf("isPartnerSuperAdminUsername = %v, want %v", got, tc.super)
			}
		})
	}
}

// A nil store must not panic through the lookup interface; it keeps the
// database-free break-glass behavior used by small handlers and tests.
func TestPartnerRoleNilLookupUsesBreakGlass(t *testing.T) {
	cfg := config.Config{
		AdminUsers:      []string{"admin@example.com"},
		SuperAdminUsers: []string{"super@example.com"},
	}
	ctx := context.Background()

	if !IsPartnerAdminUsername(ctx, cfg, nil, "admin@example.com") {
		t.Fatal("configured admin should be honored without a store")
	}
	if !IsPartnerSuperAdminUsername(ctx, cfg, nil, "super@example.com") {
		t.Fatal("configured super-admin should be honored without a store")
	}
	if IsPartnerAdminUsername(ctx, cfg, nil, "user@example.com") {
		t.Fatal("unlisted user must not become admin without a store")
	}
}

// A lookup failure (not just a missing row) retains the historical behavior:
// log and fall back to the configured break-glass identities.
func TestPartnerRoleLookupFailureUsesBreakGlass(t *testing.T) {
	cfg := config.Config{AdminUsers: []string{"admin@example.com"}}
	lookup := staticPartnerRoleLookup{err: errors.New("database unavailable")}

	if !isPartnerAdminUsername(context.Background(), cfg, lookup, "admin@example.com") {
		t.Fatal("configured admin should be honored when the directory lookup fails")
	}
	if isPartnerAdminUsername(context.Background(), cfg, lookup, "user@example.com") {
		t.Fatal("unlisted user must not become admin when the directory lookup fails")
	}
}
