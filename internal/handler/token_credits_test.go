package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestTokenCreditsAPIAndEntitlement(t *testing.T) {
	store, ctx := openHandlerTestStore(t)
	tenantID, err := store.EnsureDefaultTenant(ctx)
	if err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	uid := uuid.NewString()
	username := "tokencred-" + uid[:8] + "@example.com"
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO partner_users (user_id, active, role, tags, created_by, updated_by) VALUES ($1, true, 'user', $2::jsonb, 'test', 'test')`, uid, fmt.Sprintf(`{"email":%q}`, username)); err != nil {
		t.Fatalf("partner user: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO partner_user_logins (username, user_id, is_current) VALUES ($1, $2, true)`, username, uid); err != nil {
		t.Fatalf("login: %v", err)
	}
	subjectID, err := store.SyncPartnerUserSubject(ctx, tenantID, uid)
	if err != nil {
		t.Fatalf("sync subject: %v", err)
	}
	role, err := store.CreateModelAccessRoleWithTokens(ctx, tenantID, "tester", "tokencred-role", "", []string{"claude-opus-4-8"}, false, 0, 5000000000)
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := store.AssignModelAccessRole(ctx, tenantID, role.RoleID, subjectID, "tester"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	maHandler := NewModelAccessHandler(store)
	entHandler := NewEntitlementsHandler(store, config.Config{MonthlyTokenQuota: 100_000_000_00})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/customers/"+username+"?model=claude-opus-4-8", nil)
	rec := httptest.NewRecorder()
	entHandler.HandleEntitlement(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("baseline entitlement status %d body %s", rec.Code, rec.Body.String())
	}
	var ent map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := ent["tokenLimit"]; got != float64(5000000000) {
		t.Fatalf("baseline tokenLimit=%v want 5000000000", got)
	}

	expires := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	body := fmt.Sprintf(`{"amount_tokens":1000000000,"expires_at":%q,"reason":"test bump","request_id":"%s"}`, expires, uuid.NewString())
	hreq := httptest.NewRequest(http.MethodPost, "/api/v2/tenants/default/model-access/subjects/"+subjectID+"/token-credits", bytes.NewBufferString(body))
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("X-Forwarded-User", "tester")
	hrec := httptest.NewRecorder()
	maHandler.HandleAdmin(hrec, hreq)
	if hrec.Code != http.StatusCreated {
		t.Fatalf("POST token-credit status %d body %s", hrec.Code, hrec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/customers/"+username+"?model=claude-opus-4-8", nil)
	rec2 := httptest.NewRecorder()
	entHandler.HandleEntitlement(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("after credit status %d", rec2.Code)
	}
	var ent2 map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &ent2)
	if got := ent2["tokenLimit"]; got != float64(6000000000) {
		t.Fatalf("after credit tokenLimit=%v want 6000000000", got)
	}
	if got := ent2["tokenCredit"]; got != float64(1000000000) {
		t.Fatalf("tokenCredit=%v want 1000000000", got)
	}

	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	bodyPast := fmt.Sprintf(`{"amount_tokens":1000,"expires_at":%q,"reason":"past","request_id":"%s"}`, past, uuid.NewString())
	hreqPast := httptest.NewRequest(http.MethodPost, "/api/v2/tenants/default/model-access/subjects/"+subjectID+"/token-credits", bytes.NewBufferString(bodyPast))
	hreqPast.Header.Set("Content-Type", "application/json")
	hrecPast := httptest.NewRecorder()
	maHandler.HandleAdmin(hrecPast, hreqPast)
	if hrecPast.Code != http.StatusBadRequest {
		t.Fatalf("past expires_at status %d want 400 body %s", hrecPast.Code, hrecPast.Body.String())
	}
}
