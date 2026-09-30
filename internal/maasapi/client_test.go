package maasapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// maas-api's auth middleware 500s (AUTH_FAILURE refId 002) on any v1 call
// missing a non-empty X-MaaS-Group header. The client must refuse those
// calls locally, naming the real problem, instead of sending a request that
// fails with a confusing auth error.
func TestClientRefusesGrouplessCalls(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "test-tenant") // never dialed: guard must fire first
	tests := []struct {
		name string
		run  func() error
	}{
		{"search nil groups", func() error { _, err := c.SearchAPIKeys(context.Background(), "u@x.com", nil); return err }},
		{"search empty groups", func() error { _, err := c.SearchAPIKeys(context.Background(), "u@x.com", []string{}); return err }},
		{"revoke nil groups", func() error { return c.RevokeAPIKey(context.Background(), "key-1", nil) }},
		{"revoke empty groups", func() error { return c.RevokeAPIKey(context.Background(), "key-1", []string{}) }},
	}
	for _, tc := range tests {
		err := tc.run()
		if err == nil {
			t.Fatalf("%s: want local refusal, got nil error", tc.name)
		}
		if !strings.Contains(err.Error(), "no groups") {
			t.Fatalf("%s: error %q should name the missing groups, not leak a transport/auth error", tc.name, err)
		}
	}
}

func TestCreateGEAPIKeyAlwaysUsesGEGroupAndForwardsMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/api-keys" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-MaaS-Username"); got != "alice@example.com" {
			t.Fatalf("username header = %q", got)
		}
		if got := r.Header.Get("X-MaaS-Group"); got != `["GE"]` {
			t.Fatalf("group header = %q, want [\"GE\"]", got)
		}
		var body APIKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Name != "Atlas" || body.ExpiresIn != "720h" || body.Labels["created_by"] != "atlas" {
			t.Fatalf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIKeyResponse{ID: "key-1", Key: "sk-oai-plaintext"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "tenant")
	key, err := client.CreateGEAPIKey(context.Background(), "alice@example.com", APIKeyRequest{
		Name: "Atlas", ExpiresIn: "720h", Labels: map[string]string{"created_by": "atlas"},
	})
	if err != nil {
		t.Fatalf("CreateGEAPIKey: %v", err)
	}
	if key.ID != "key-1" || key.Key != "sk-oai-plaintext" {
		t.Fatalf("response = %+v", key)
	}
}
