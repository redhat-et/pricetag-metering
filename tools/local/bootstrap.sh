#!/usr/bin/env bash
set -euo pipefail

# Local-only fixture. This deliberately creates no production key and never
# imports production data or provider credentials.
PROJECT="${COMPOSE_PROJECT_NAME:-pricetag-safety}"
until docker compose -p "$PROJECT" exec -T postgres pg_isready -U metering -d metering >/dev/null 2>&1; do
  sleep 1
done

docker compose -p "$PROJECT" exec -T postgres psql -U metering -d metering <<'SQL'
INSERT INTO partner_users (user_id, tags, role, active, created_by, updated_by)
VALUES (
  '00000000-0000-4000-8000-000000000001',
  '{"email":"nitzikow@redhat.com","first_name":"Noy","last_name":"Itzikowitz","country":"US","kerberos_id":"nitzikow"}',
  'super_admin', true, 'local-bootstrap', 'local-bootstrap'
)
ON CONFLICT (user_id) DO UPDATE SET role='super_admin', active=true;

INSERT INTO partner_user_logins (username, user_id, is_current)
VALUES ('nitzikow@redhat.com', '00000000-0000-4000-8000-000000000001', true)
ON CONFLICT (username) DO UPDATE SET user_id=EXCLUDED.user_id, is_current=true;

INSERT INTO partner_users (user_id, tags, role, active, created_by, updated_by)
VALUES (
  '00000000-0000-4000-8000-000000000002',
  '{"email":"local-test-user@example.com","first_name":"Local","last_name":"Test User","country":"US"}',
  'user', true, 'local-bootstrap', 'local-bootstrap'
)
ON CONFLICT (user_id) DO UPDATE SET active=true;

INSERT INTO partner_user_logins (username, user_id, is_current)
VALUES ('local-test-user@example.com', '00000000-0000-4000-8000-000000000002', true)
ON CONFLICT (username) DO UPDATE SET user_id=EXCLUDED.user_id, is_current=true;

INSERT INTO partner_user_keys (key_id, user_id, username, name, created_by)
VALUES ('local-test-user-key', '00000000-0000-4000-8000-000000000002',
        'local-test-user@example.com', 'local test inference key', 'local-bootstrap')
ON CONFLICT (key_id) DO UPDATE SET revoked_at=NULL;

-- Synthetic current-month spend. cost_usd is frozen at $150 so this fixture
-- does not depend on production pricing or provider credentials.
DELETE FROM usage_events
WHERE username='local-test-user@example.com' AND source='local-bootstrap';
INSERT INTO usage_events
  (event_id, timestamp, username, model, provider, source,
   prompt_tokens, completion_tokens, total_tokens, status_code, cost_usd)
VALUES
  ('local-bootstrap-$150', date_trunc('month', NOW()) + interval '1 hour',
   'local-test-user@example.com', 'local-echo', 'llm-katan', 'local-bootstrap',
   0, 2000000, 2000000, 200, 150.00)
ON CONFLICT DO NOTHING;
SQL

echo "Local super-admin key: local-noy-key"
echo "Local test-user key: local-test-user-key"
echo "Test user synthetic month-to-date spend: \$150"
echo "Dashboard: http://localhost:8080/admin"
