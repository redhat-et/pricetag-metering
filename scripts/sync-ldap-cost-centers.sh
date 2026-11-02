#!/usr/bin/env bash
#
# Sync Red Hat IPA cost centers into PriceTag partner_users.tags.
#
# Read-only against LDAP. The only writes are UPDATEs to PriceTag's
# partner_users table, keyed by the stable rhatUUID. It never changes LDAP,
# never provisions users, and never touches a database unless --apply is passed.
#
# Mapping:
#   LDAP rhatUUID              -> partner_users.user_id (UUID)
#   LDAP rhatCostCenter        -> partner_users.tags.cost_center
#   LDAP rhatCostCenterDesc    -> partner_users.tags.cost_center_description
#
# Existing non-cost-center tags are preserved. Empty LDAP values are skipped so
# a missing attribute cannot erase a value already present in PriceTag.
#
# Requirements:
#   - Red Hat VPN for production IPA.
#   - ldapsearch (OpenLDAP client), python3, and either psql or docker/oc.
#   - LDAP password in a mode 600 file (default: ~/.ldap_key).
#   - LDAP CA file (default: ~/enmaas-ldap/2022-IT-Root-CA.pem).
#
# Usage:
#   ./scripts/sync-ldap-cost-centers.sh                 # dry run, generates files
#   ./scripts/sync-ldap-cost-centers.sh --apply ...     # write to PriceTag DB
#
# Database modes:
#   1. Local psql:
#        export DATABASE_URL='postgres://...'
#        ./scripts/sync-ldap-cost-centers.sh --apply
#      or:
#        ./scripts/sync-ldap-cost-centers.sh --apply --database-url-file ~/db-url
#
#   2. OpenShift/kubectl, reading the URL from a Secret and running psql in the
#      Postgres pod. This is the supported mode for the dogfood cluster:
#        ./scripts/sync-ldap-cost-centers.sh --apply \
#          --kube-context 'ai-gateway-dogfood/...' \
#          --namespace ai-gateway-dogfood \
#          --pg-pod aigateway-pg-1 \
#          --pg-secret postgresql-credentials \
#          --pg-secret-key METERING_DB_URL
#
#   3. Docker, for machines without a local psql and with an externally
#      reachable database URL. In-cluster service names will not resolve from
#      the Docker network; use the kube mode above for those:
#        ./scripts/sync-ldap-cost-centers.sh --apply --docker \
#          --database-url-file ~/db-url
#
# Useful options:
#   --ldap-server URL          Repeatable; defaults to the prod IPA failover list.
#   --ldap-base DN             Default: cn=users,cn=accounts,dc=ipa,dc=redhat,dc=com
#   --ldap-filter FILTER       Default: (&(objectClass=posixAccount)(rhatUUID=*))
#   --password-file PATH       Default: ~/.ldap_key
#   --ca-file PATH             Default: ~/enmaas-ldap/2022-IT-Root-CA.pem
#   --output-dir PATH          Default: ~/enmaas-oidc-local
#   --ldif PATH                Reuse an already-fetched LDIF instead of querying.
#   --pg-remote-dir PATH       Writable directory in the Postgres pod for SQL
#                              (default: /tmp; use /controller/run when needed).
#   --dry-run                  Default; generate SQL/CSV/LDIF but do not write DB.
#
# Sensitive output is written under --output-dir with mode 600. Full LDAP
# records are never printed to stdout/stderr.
#
set -euo pipefail

APPLY=0
DB_MODE="auto"
OUTPUT_DIR="${OUTPUT_DIR:-$HOME/enmaas-oidc-local}"
LDAP_BIND_DN="${LDAP_BIND_DN:-uid=enmaas-ldap,cn=users,cn=accounts,dc=ipa,dc=redhat,dc=com}"
LDAP_USER_BASE="${LDAP_USER_BASE:-cn=users,cn=accounts,dc=ipa,dc=redhat,dc=com}"
LDAP_PASSWORD_FILE="${LDAP_PASSWORD_FILE:-$HOME/.ldap_key}"
LDAP_CA_CERT="${LDAP_CA_CERT:-$HOME/enmaas-ldap/2022-IT-Root-CA.pem}"
LDAP_FILTER="${LDAP_FILTER:-(&(objectClass=posixAccount)(rhatUUID=*))}"
LDIF_INPUT=""
DATABASE_URL_FILE="${DATABASE_URL_FILE:-}"
KUBECTL="${KUBECTL:-oc}"
KUBE_CONTEXT="${KUBE_CONTEXT:-}"
KUBE_NAMESPACE="${KUBE_NAMESPACE:-}"
PG_POD="${PG_POD:-}"
PG_CONTAINER="${PG_CONTAINER:-postgres}"
PG_REMOTE_DIR="${PG_REMOTE_DIR:-/tmp}"
PG_SECRET="${PG_SECRET:-postgresql-credentials}"
PG_SECRET_KEY="${PG_SECRET_KEY:-METERING_DB_URL}"
DOCKER_IMAGE="${DOCKER_IMAGE:-postgres:16}"

LDAP_SERVERS=()

die() { echo "ERROR: $*" >&2; exit 1; }
usage() { sed -n '2,90p' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) APPLY=1; shift ;;
    --dry-run) APPLY=0; shift ;;
    --ldap-server) LDAP_SERVERS+=("$2"); shift 2 ;;
    --ldap-base) LDAP_USER_BASE="$2"; shift 2 ;;
    --ldap-filter) LDAP_FILTER="$2"; shift 2 ;;
    --password-file) LDAP_PASSWORD_FILE="$2"; shift 2 ;;
    --ca-file) LDAP_CA_CERT="$2"; shift 2 ;;
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    --ldif) LDIF_INPUT="$2"; shift 2 ;;
    --database-url-file) DATABASE_URL_FILE="$2"; shift 2 ;;
    --docker) DB_MODE="docker"; shift ;;
    --kube-context) KUBE_CONTEXT="$2"; DB_MODE="kube"; shift 2 ;;
    --namespace) KUBE_NAMESPACE="$2"; shift 2 ;;
    --pg-pod) PG_POD="$2"; shift 2 ;;
    --pg-container) PG_CONTAINER="$2"; shift 2 ;;
    --pg-remote-dir) PG_REMOTE_DIR="$2"; shift 2 ;;
    --pg-secret) PG_SECRET="$2"; shift 2 ;;
    --pg-secret-key) PG_SECRET_KEY="$2"; shift 2 ;;
    --kubectl) KUBECTL="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (use --help)" ;;
  esac
done

if [[ ${#LDAP_SERVERS[@]} -eq 0 ]]; then
  LDAP_SERVERS=(
    "ldaps://admin2.idm-001.prod.iad2.dc.redhat.com"
    "ldaps://s3.idm-001.prod.iad2.dc.redhat.com"
    "ldaps://s4.idm-001.prod.iad2.dc.redhat.com"
    "ldaps://s3.idm-001.prod.rdu2.dc.redhat.com"
    "ldaps://s4.idm-001.prod.rdu2.dc.redhat.com"
    "ldaps://s1.idm-001.prod.us-east-1.aws.redhat.com"
    "ldaps://s2.idm-001.prod.us-east-1.aws.redhat.com"
  )
fi

command -v python3 >/dev/null || die "python3 is required"
if [[ -z "$LDIF_INPUT" ]]; then
  command -v ldapsearch >/dev/null || die "ldapsearch is required"
  [[ -r "$LDAP_PASSWORD_FILE" ]] || die "LDAP password file is missing or unreadable: $LDAP_PASSWORD_FILE"
  [[ -r "$LDAP_CA_CERT" ]] || die "LDAP CA file is missing or unreadable: $LDAP_CA_CERT"
  mode="$(stat -f '%Lp' "$LDAP_PASSWORD_FILE" 2>/dev/null || stat -c '%a' "$LDAP_PASSWORD_FILE")"
  if [[ "$mode" != "600" && "$mode" != "400" ]]; then
    die "LDAP password file must be mode 600 (or 400), got $mode: $LDAP_PASSWORD_FILE"
  fi
else
  [[ -r "$LDIF_INPUT" ]] || die "LDIF file is missing or unreadable: $LDIF_INPUT"
fi

if [[ "$DB_MODE" == "auto" ]]; then
  if [[ -n "$PG_POD" ]]; then
    DB_MODE="kube"
  elif [[ -n "${DATABASE_URL:-}" || -n "$DATABASE_URL_FILE" ]]; then
    if command -v psql >/dev/null; then
      DB_MODE="local"
    else
      DB_MODE="docker"
    fi
  else
    DB_MODE="none"
  fi
fi

if [[ "$APPLY" -eq 1 ]]; then
  case "$DB_MODE" in
    local)
      command -v psql >/dev/null || die "psql is required for local mode"
      ;;
    kube)
      [[ -n "$KUBE_CONTEXT" ]] || die "--kube-context is required for kube mode"
      [[ -n "$KUBE_NAMESPACE" ]] || die "--namespace is required for kube mode"
      [[ -n "$PG_POD" ]] || die "--pg-pod is required for kube mode"
      command -v "$KUBECTL" >/dev/null || die "$KUBECTL is required for kube mode"
      ;;
    docker)
      command -v docker >/dev/null || die "docker is required for docker mode"
      ;;
    *)
      die "no database target configured; set DATABASE_URL/DATABASE_URL_FILE or use --kube-context/--pg-pod"
      ;;
  esac
fi

mkdir -p "$OUTPUT_DIR"
chmod 700 "$OUTPUT_DIR"
umask 077
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
ldif="$OUTPUT_DIR/ldap-cost-centers-$stamp.ldif"
csv="$OUTPUT_DIR/ldap-cost-centers-$stamp.csv"
sql="$OUTPUT_DIR/ldap-cost-centers-$stamp.sql"
: > "$csv"; chmod 600 "$csv"
: > "$sql"; chmod 600 "$sql"

pw_tmp=""
cleanup() {
  if [[ -n "$pw_tmp" ]]; then
    rm -f "$pw_tmp"
  fi
}
trap cleanup EXIT

if [[ -n "$LDIF_INPUT" ]]; then
  ldif="$LDIF_INPUT"
  echo "Using existing LDIF: $ldif"
else
  pw_tmp="$(mktemp "${TMPDIR:-/tmp}/pricetag-ldap-pw.XXXXXX")"
  chmod 600 "$pw_tmp"
  tr -d '\r\n' < "$LDAP_PASSWORD_FILE" > "$pw_tmp"

  host=""
  for server in "${LDAP_SERVERS[@]}"; do
    if LDAPTLS_CACERT="$LDAP_CA_CERT" ldapsearch -LLL -o ldif-wrap=no -H "$server" \
         -D "$LDAP_BIND_DN" -y "$pw_tmp" -b "$LDAP_USER_BASE" \
         -s base -z 1 '(objectClass=*)' dn >/dev/null 2>&1; then
      host="$server"
      break
    fi
  done
  [[ -n "$host" ]] || die "could not bind to any LDAP server; check VPN, network, CA, and credentials"
  echo "LDAP bind OK: $host"

  : > "$ldif"; chmod 600 "$ldif"
  LDAPTLS_CACERT="$LDAP_CA_CERT" ldapsearch -LLL -o ldif-wrap=no -E pr=1000/noprompt \
    -H "$host" -D "$LDAP_BIND_DN" -y "$pw_tmp" \
    -b "$LDAP_USER_BASE" "$LDAP_FILTER" \
    rhatUUID rhatCostCenter rhatCostCenterDesc > "$ldif"
fi

python3 - "$ldif" "$csv" "$sql" <<'PY'
import base64
import csv
import re
import sys
import uuid

ldif_path, csv_path, sql_path = sys.argv[1:4]
uuid_re = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def parse_ldif(path):
    record = {}
    last_attr = None

    def value_of(rest):
        if rest.startswith(":"):
            return base64.b64decode(rest[1:].strip()).decode("utf-8", "replace")
        return rest[1:] if rest.startswith(" ") else rest

    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for raw in fh:
            line = raw.rstrip("\r\n")
            if not line:
                if record:
                    yield record
                record = {}
                last_attr = None
                continue
            if line.startswith(" "):
                if last_attr and last_attr in record:
                    record[last_attr][-1] += line[1:]
                continue
            if ":" not in line:
                continue
            attr, rest = line.split(":", 1)
            attr = attr.lower()
            record.setdefault(attr, []).append(value_of(rest))
            last_attr = attr
    if record:
        yield record


def first(record, key):
    vals = record.get(key.lower())
    return vals[0].strip() if vals else ""


rows = {}
total = 0
invalid_uuid = 0
duplicates = 0
missing_cost_center = 0

for record in parse_ldif(ldif_path):
    total += 1
    raw_uuid = first(record, "rhatUUID").lower()
    if not uuid_re.match(raw_uuid):
        invalid_uuid += 1
        continue
    uid = str(uuid.UUID(raw_uuid))
    cc = first(record, "rhatCostCenter")
    desc = first(record, "rhatCostCenterDesc")
    if not cc and not desc:
        missing_cost_center += 1
        continue
    if uid in rows:
        duplicates += 1
        continue
    rows[uid] = (cc, desc)


def q(value):
    if value is None:
        return "NULL"
    return "'" + value.replace("'", "''") + "'"


with open(csv_path, "w", encoding="utf-8", newline="") as fh:
    writer = csv.writer(fh)
    writer.writerow(["user_id", "cost_center", "cost_center_description"])
    for uid in sorted(rows):
        cc, desc = rows[uid]
        writer.writerow([uid, cc, desc])

with open(sql_path, "w", encoding="utf-8") as fh:
    fh.write("BEGIN;\n")
    fh.write("CREATE TEMP TABLE _ldap_cost_centers (\n")
    fh.write("  user_id uuid PRIMARY KEY,\n")
    fh.write("  cost_center text NOT NULL,\n")
    fh.write("  cost_center_description text NOT NULL\n")
    fh.write(") ON COMMIT DROP;\n")
    fh.write("INSERT INTO _ldap_cost_centers (user_id, cost_center, cost_center_description) VALUES\n")
    items = sorted(rows.items())
    for index, (uid, (cc, desc)) in enumerate(items):
        suffix = ",\n" if index + 1 < len(items) else ";\n"
        fh.write(f"  ('{uid}', {q(cc)}, {q(desc)}){suffix}")
    fh.write("SELECT 'ldap_cost_center_rows=' || count(*) FROM _ldap_cost_centers;\n")
    fh.write("SELECT 'partner_users_total=' || count(*) FROM partner_users;\n")
    fh.write("SELECT 'matched_partner_users=' || count(*) FROM _ldap_cost_centers u JOIN partner_users pu USING (user_id);\n")
    fh.write("SELECT 'partner_users_without_ldap_match=' || count(*) FROM partner_users pu LEFT JOIN _ldap_cost_centers u USING (user_id) WHERE u.user_id IS NULL;\n")
    fh.write("WITH updated AS (\n")
    fh.write("  UPDATE partner_users pu\n")
    fh.write("  SET tags = pu.tags || jsonb_strip_nulls(jsonb_build_object(\n")
    fh.write("        'cost_center', NULLIF(btrim(u.cost_center), ''),\n")
    fh.write("        'cost_center_description', NULLIF(btrim(u.cost_center_description), '')\n")
    fh.write("      )),\n")
    fh.write("      updated_at = now()\n")
    fh.write("  FROM _ldap_cost_centers u\n")
    fh.write("  WHERE pu.user_id = u.user_id\n")
    fh.write("    AND (NULLIF(btrim(u.cost_center), '') IS NOT NULL\n")
    fh.write("         OR NULLIF(btrim(u.cost_center_description), '') IS NOT NULL)\n")
    fh.write("  RETURNING 1\n")
    fh.write(")\n")
    fh.write("SELECT 'updated_partner_users=' || count(*) FROM updated;\n")
    fh.write("COMMIT;\n")

print(f"ldap_records={total}")
print(f"ldap_records_with_cost_center={len(rows)}")
print(f"ldap_records_missing_cost_center={missing_cost_center}")
print(f"ldap_records_invalid_uuid={invalid_uuid}")
print(f"ldap_records_duplicate_uuid_skipped={duplicates}")
PY

echo "LDIF: $ldif"
echo "CSV:  $csv"
echo "SQL:  $sql"

if [[ "$APPLY" -eq 0 ]]; then
  echo "Dry run only. Re-run with --apply to update partner_users in PriceTag."
  exit 0
fi

dburl=""
if [[ -n "${DATABASE_URL:-}" ]]; then
  dburl="$DATABASE_URL"
elif [[ -n "$DATABASE_URL_FILE" ]]; then
  [[ -r "$DATABASE_URL_FILE" ]] || die "database URL file is missing or unreadable: $DATABASE_URL_FILE"
  dburl="$(tr -d '\r\n' < "$DATABASE_URL_FILE")"
fi

case "$DB_MODE" in
  local)
    [[ -n "$dburl" ]] || die "DATABASE_URL or --database-url-file is required for local mode"
    psql "$dburl" -v ON_ERROR_STOP=1 -At -f "$sql"
    ;;
  docker)
    [[ -n "$dburl" ]] || die "DATABASE_URL or --database-url-file is required for docker mode"
    printf '%s\n' "$dburl" | docker run --rm -i -v "$sql:/sql:ro" "$DOCKER_IMAGE" \
      sh -c 'IFS= read -r url; psql "$url" -v ON_ERROR_STOP=1 -At -f /sql'
    ;;
  kube)
    secret_json="$("$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      get secret "$PG_SECRET" -o "jsonpath={.data.${PG_SECRET_KEY}}")"
    dburl="$(printf '%s' "$secret_json" | base64 -d)"
    remote="${PG_REMOTE_DIR%/}/pricetag-ldap-cost-centers-$$.sql"
    "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      cp -c "$PG_CONTAINER" "$sql" "$PG_POD:$remote"
    printf '%s\n' "$dburl" | "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      exec -i -c "$PG_CONTAINER" "$PG_POD" -- sh -c '
        IFS= read -r url
        sql=$1
        trap "rm -f \"$sql\"" EXIT
        psql "$url" -v ON_ERROR_STOP=1 -At -f "$sql"
      ' _ "$remote"
    ;;
esac
