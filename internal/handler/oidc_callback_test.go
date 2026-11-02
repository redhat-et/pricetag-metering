package handler

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

const (
	testOIDCSubject = "redhat-user-123"
	testOIDCUUID    = "11111111-1111-4111-8111-111111111111"
)

type oidcCallbackScenario struct {
	issuer       string
	audience     any
	nonce        string
	expiresAt    time.Time
	invalidSig   bool
	rhatUUID     string
	omitRhatUUID bool
	userinfoSub  string
	userinfoData map[string]any
	tokenDelay   time.Duration
	httpTimeout  time.Duration
}

type oidcCallbackFixture struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
}

func newOIDCCallbackFixture(t *testing.T, scenario oidcCallbackScenario) *oidcCallbackFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	var fixture *oidcCallbackFixture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": fixture.server.URL, "authorization_endpoint": fixture.server.URL + "/authorize",
				"token_endpoint": fixture.server.URL + "/token", "jwks_uri": fixture.server.URL + "/jwks",
				"userinfo_endpoint":                     fixture.server.URL + "/userinfo",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}}})
		case "/token":
			if scenario.tokenDelay > 0 {
				time.Sleep(scenario.tokenDelay)
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "test-code" {
				http.Error(w, "invalid token request", http.StatusBadRequest)
				return
			}
			token, signErr := signTestIDToken(key, fixture.claims)
			if signErr != nil {
				http.Error(w, "could not sign token", http.StatusInternalServerError)
				return
			}
			if scenario.invalidSig {
				parts := strings.Split(token, ".")
				sig := []byte(parts[2])
				if sig[0] == 'A' {
					sig[0] = 'B'
				} else {
					sig[0] = 'A'
				}
				parts[2] = string(sig)
				token = strings.Join(parts, ".")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-access-token", "token_type": "Bearer", "expires_in": 3600,
				"id_token": token,
			})
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			data := map[string]any{"sub": scenario.userinfoSub}
			for k, v := range scenario.userinfoData {
				data[k] = v
			}
			_ = json.NewEncoder(w).Encode(data)
		default:
			http.NotFound(w, r)
		}
	}))
	fixture = &oidcCallbackFixture{server: server, key: key}
	t.Cleanup(server.Close)

	issuer := scenario.issuer
	if issuer == "" {
		issuer = server.URL
	}
	audience := scenario.audience
	if audience == nil {
		audience = "client-id"
	}
	nonce := scenario.nonce
	if nonce == "" {
		nonce = "expected-nonce"
	}
	exp := scenario.expiresAt
	if exp.IsZero() {
		exp = time.Now().Add(5 * time.Minute)
	}
	rhatUUID := scenario.rhatUUID
	if rhatUUID == "" {
		rhatUUID = testOIDCUUID
	}
	fixture.claims = map[string]any{
		"iss": issuer, "sub": testOIDCSubject, "aud": audience,
		"exp": exp.Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "nonce": nonce,
		"email": "person@example.com",
	}
	if !scenario.omitRhatUUID {
		fixture.claims["rhatUUID"] = rhatUUID
	}
	return fixture
}

func signTestIDToken(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	message := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type oidcCallbackStore struct {
	user       storage.PartnerUser
	lookupErr  error
	lookedUp   string
	patchCount int
}

func (s *oidcCallbackStore) PartnerUserByMaaSUsername(_ context.Context, username string) (storage.PartnerUser, error) {
	s.lookedUp = username
	return s.user, s.lookupErr
}

func (s *oidcCallbackStore) PatchPartnerUser(_ context.Context, _, userID string, tags map[string]any) (storage.PartnerUser, error) {
	s.patchCount++
	s.user.UserID = userID
	s.user.Tags = tags
	return s.user, nil
}

func invokeOIDCCallback(t *testing.T, scenario oidcCallbackScenario, store *oidcCallbackStore) *httptest.ResponseRecorder {
	t.Helper()
	fixture := newOIDCCallbackFixture(t, scenario)
	h := NewAuthHandler(config.Config{OIDC: config.OIDC{
		Enabled: true, IssuerURL: fixture.server.URL, ClientID: "client-id",
		ClientSecret: "client-secret", RedirectURL: "http://dashboard.example/oauth/callback",
		Scopes: []string{"openid", "profile", "email"},
	}})
	if scenario.httpTimeout > 0 {
		h.httpClient.Timeout = scenario.httpTimeout
	}
	h.setOIDCPartnerStore(store)
	if err := h.InitOIDC(context.Background()); err != nil {
		t.Fatal(err)
	}

	state := oidcState{State: "expected-state", Nonce: "expected-nonce", CodeVerifier: "test-code-verifier", Exp: time.Now().Add(time.Minute).Unix()}
	stateResponse := httptest.NewRecorder()
	if err := h.setOIDCState(stateResponse, state); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/oauth/callback?"+url.Values{
		"state": {state.State}, "code": {"test-code"},
	}.Encode(), nil)
	request.AddCookie(stateResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	h.HandleOIDCCallback(response, request)
	return response
}

func TestOIDCCallbackAcceptsVerifiedTokenAndMatchingUserInfoSubject(t *testing.T) {
	store := &oidcCallbackStore{user: storage.PartnerUser{
		UserID: testOIDCUUID, Active: true, Tags: map[string]any{"email": "person@example.com"},
	}}
	response := invokeOIDCCallback(t, oidcCallbackScenario{
		userinfoSub: testOIDCSubject, userinfoData: map[string]any{"given_name": "Pat"},
	}, store)
	if response.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d: %s", response.Code, http.StatusFound, response.Body.String())
	}
	if store.lookedUp != "person@example.com" || store.patchCount != 1 {
		t.Fatalf("store lookup=%q profile patches=%d; want expected user and one profile patch", store.lookedUp, store.patchCount)
	}
}

func TestOIDCCallbackRejectsInvalidIDTokens(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*oidcCallbackScenario)
	}{
		{name: "wrong nonce", mutate: func(s *oidcCallbackScenario) { s.nonce = "wrong-nonce" }},
		{name: "wrong audience", mutate: func(s *oidcCallbackScenario) { s.audience = "another-client" }},
		{name: "wrong issuer", mutate: func(s *oidcCallbackScenario) { s.issuer = "https://wrong-issuer.example" }},
		{name: "expired token", mutate: func(s *oidcCallbackScenario) { s.expiresAt = time.Now().Add(-time.Minute) }},
		{name: "invalid signature", mutate: func(s *oidcCallbackScenario) { s.invalidSig = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scenario := oidcCallbackScenario{userinfoSub: testOIDCSubject}
			tc.mutate(&scenario)
			store := &oidcCallbackStore{}
			response := invokeOIDCCallback(t, scenario, store)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("callback status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
			}
			if store.lookedUp != "" || store.patchCount != 0 {
				t.Fatalf("invalid token reached identity store: lookup=%q patches=%d", store.lookedUp, store.patchCount)
			}
		})
	}
}

func TestOIDCCallbackRejectsUserInfoSubjectMismatch(t *testing.T) {
	store := &oidcCallbackStore{user: storage.PartnerUser{UserID: testOIDCUUID, Active: true}}
	response := invokeOIDCCallback(t, oidcCallbackScenario{
		userinfoSub: "different-subject", userinfoData: map[string]any{"email": "other@example.com"},
	}, store)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	if store.lookedUp != "" {
		t.Fatalf("mismatched UserInfo reached directory lookup for %q", store.lookedUp)
	}
}

func TestOIDCCallbackFailsClosedForMissingOrUnprovisionedIdentity(t *testing.T) {
	tests := []struct {
		name     string
		scenario oidcCallbackScenario
		store    *oidcCallbackStore
		wantCode int
	}{
		{
			name:     "missing rhatUUID",
			scenario: oidcCallbackScenario{omitRhatUUID: true, userinfoSub: testOIDCSubject},
			store:    &oidcCallbackStore{}, wantCode: http.StatusUnauthorized,
		},
		{
			name:     "no active partner record",
			scenario: oidcCallbackScenario{userinfoSub: testOIDCSubject},
			store:    &oidcCallbackStore{lookupErr: errors.New("not found")}, wantCode: http.StatusForbidden,
		},
		{
			name:     "inactive partner record",
			scenario: oidcCallbackScenario{userinfoSub: testOIDCSubject},
			store:    &oidcCallbackStore{user: storage.PartnerUser{UserID: testOIDCUUID, Active: false}}, wantCode: http.StatusForbidden,
		},
		{
			name:     "partner UUID mismatch",
			scenario: oidcCallbackScenario{userinfoSub: testOIDCSubject},
			store:    &oidcCallbackStore{user: storage.PartnerUser{UserID: "22222222-2222-4222-8222-222222222222", Active: true}}, wantCode: http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := invokeOIDCCallback(t, tc.scenario, tc.store)
			if response.Code != tc.wantCode {
				t.Fatalf("callback status = %d, want %d: %s", response.Code, tc.wantCode, response.Body.String())
			}
			if tc.store.patchCount != 0 {
				t.Fatalf("failed identity mapping persisted profile %d times", tc.store.patchCount)
			}
		})
	}
}

func TestOIDCDevelopmentFeaturesRequireExplicitGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.OIDC
	}{
		{name: "claims debug", cfg: config.OIDC{Enabled: true, ClaimsDebug: true}},
		{name: "test login", cfg: config.OIDC{Enabled: true, TestLogin: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAuthHandler(config.Config{OIDC: tc.cfg})
			if err := h.InitOIDC(context.Background()); err == nil || !strings.Contains(err.Error(), "OIDC_DEVELOPMENT_MODE") {
				t.Fatalf("InitOIDC error = %v, want explicit development-mode guard", err)
			}
		})
	}
}

func TestOIDCDiscoveryUsesBoundedNetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprintln(w, `{}`)
	}))
	defer server.Close()
	h := NewAuthHandler(config.Config{OIDC: config.OIDC{
		Enabled: true, IssuerURL: server.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "http://dashboard.example/oauth/callback",
	}})
	h.httpClient.Timeout = 20 * time.Millisecond
	start := time.Now()
	err := h.InitOIDC(context.Background())
	if err == nil {
		t.Fatal("expected discovery timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("OIDC discovery was not bounded: elapsed %s", time.Since(start))
	}
}

func TestOIDCCallbackUsesBoundedTokenExchangeTimeout(t *testing.T) {
	store := &oidcCallbackStore{}
	start := time.Now()
	response := invokeOIDCCallback(t, oidcCallbackScenario{
		userinfoSub: testOIDCSubject, tokenDelay: 150 * time.Millisecond, httpTimeout: 20 * time.Millisecond,
	}, store)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("OIDC token exchange was not bounded: elapsed %s", time.Since(start))
	}
	if store.lookedUp != "" {
		t.Fatalf("timed-out exchange reached identity store for %q", store.lookedUp)
	}
}
