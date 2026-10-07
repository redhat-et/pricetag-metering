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
SQL

echo "Local super-admin key: local-noy-key"
echo "Dashboard: http://localhost:8080/admin"
