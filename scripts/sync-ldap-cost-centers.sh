#!/usr/bin/env bash
#
# Sync Red Hat IPA directory fields into PriceTag partner_users.
#
# Read-only against LDAP. The only writes are:
#   - selected keys in partner_users.tags (default: all supported fields), and
#   - partner_users.manager_user_id when a manager DN resolves to a partner user.
#
# It never changes LDAP, never provisions users, and never touches a database
# unless --apply is passed. Every apply first writes a snapshot and a rollback
# SQL file under --output-dir.
#
# Mapping (empty LDAP values are skipped, so a missing attribute cannot erase
# a value already present in PriceTag):
#   LDAP rhatUUID              -> partner_users.user_id (UUID)
#   LDAP mail                  -> tags.email
#   LDAP givenName             -> tags.first_name
#   LDAP sn                    -> tags.last_name
#   LDAP co / c                -> tags.country
#   LDAP uid                   -> tags.kerberos_id
#   LDAP rhatCostCenter        -> tags.cost_center
#   LDAP rhatCostCenterDesc    -> tags.cost_center_description
#   LDAP rhatOrganization      -> tags.organization
#   LDAP rhatJobTitle / title  -> tags.job_title
#   LDAP rhatJobCode           -> tags.job_code
#   LDAP rhatGeo               -> tags.geo
#   LDAP rhatOfficeLocation    -> tags.office_location
#   LDAP rhatWorkerId          -> tags.worker_id
#   LDAP rhatSupervisorWorkerId-> tags.supervisor_worker_id
#   LDAP rhatEmployeeSubType   -> tags.employee_subtype
#   LDAP rhatPersonType        -> tags.person_type
#   LDAP rhatHireDate          -> tags.hire_date
#   LDAP rhatTermDate          -> tags.term_date
#   LDAP rhatDeactivated       -> tags.deactivated
#   LDAP manager DN            -> tags.manager_uuid and manager_user_id
#
# Requirements:
#   - Red Hat VPN for production IPA.
#   - ldapsearch (OpenLDAP client), python3, and either psql or docker/oc.
#   - LDAP password in a mode 600/400 file (default: ~/.ldap_key).
#   - LDAP CA file (default: ~/enmaas-ldap/2022-IT-Root-CA.pem).
#
# Usage:
#   ./scripts/sync-ldap-cost-centers.sh                 # dry run, generates files
#   ./scripts/sync-ldap-cost-centers.sh --apply ...     # write to PriceTag DB
#   ./scripts/sync-ldap-cost-centers.sh --fields cost_center,cost_center_description,manager
#
# Database modes:
#   1. Local psql:
#        export DATABASE_URL='postgres://...'
#        ./scripts/sync-ldap-cost-centers.sh --apply
#      or:
#        ./scripts/sync-ldap-cost-centers.sh --apply --database-url-file ~/db-url
#      The URL is translated into a temporary libpq service + password file;
#      it never appears in a psql argv or process command line.
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
#      reachable database URL:
#        ./scripts/sync-ldap-cost-centers.sh --apply --docker \
#          --database-url-file ~/db-url
#
# Useful options:
#   --ldap-server URL          Repeatable; defaults to the prod IPA failover list.
#   --ldap-base DN             Default: cn=users,cn=accounts,dc=ipa,dc=redhat,dc=com
#   --ldap-filter FILTER       Default: (&(objectClass=posixAccount)(rhatUUID=*))
#   --fields LIST              Comma list, or "all" (default). Supported:
#                              email,first_name,last_name,country,kerberos_id,
#                              cost_center,cost_center_description,organization,
#                              job_title,job_code,geo,office_location,worker_id,
#                              supervisor_worker_id,employee_subtype,person_type,
#                              hire_date,term_date,deactivated,manager
#   --min-matched N            Abort apply if fewer than N LDAP rows match a
#                              partner user. Default: 0.
#   --allow-empty              Permit apply when LDAP returned zero usable rows.
#   --password-file PATH       Default: ~/.ldap_key
#   --ca-file PATH             Default: ~/enmaas-ldap/2022-IT-Root-CA.pem
#   --output-dir PATH          Default: ~/enmaas-oidc-local
#   --ldif PATH                Reuse an already-fetched LDIF instead of querying.
#   --pg-remote-dir PATH       Writable directory in the Postgres pod for SQL
#                              (default: /tmp; use /controller/run when needed).
#   --dry-run                  Default; generate SQL/CSV/LDIF but do not write DB.
#
# Sensitive LDIF/CSV/backup/rollback output is written under --output-dir with
# mode 600. Full LDAP records are never printed to stdout/stderr.
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
LDAP_FIELDS="${LDAP_FIELDS:-all}"
MIN_MATCHED="${LDAP_SYNC_MIN_MATCHED:-0}"
ALLOW_EMPTY=0
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
DB_SERVICE="pricetag-ldap-sync"

LDAP_SERVERS=()
LDAP_ATTRS=(
  rhatUUID uid mail givenName sn cn displayName manager
  rhatPersonType employeeType title rhatJobTitle rhatJobCode
  c co st l rhatGeo rhatOfficeLocation street postalCode
  rhatCostCenter rhatCostCenterDesc rhatOrganization
  rhatEmployeeSubType rhatWorkerId rhatSupervisorWorkerId
  rhatHireDate rhatTermDate rhatDeactivated modifyTimestamp
)

die() { echo "ERROR: $*" >&2; exit 1; }

usage() {
  awk 'NR > 1 && /^set -euo pipefail$/ { exit } NR > 1 { print }' "$0"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) APPLY=1; shift ;;
    --dry-run) APPLY=0; shift ;;
    --ldap-server) LDAP_SERVERS+=("$2"); shift 2 ;;
    --ldap-base) LDAP_USER_BASE="$2"; shift 2 ;;
    --ldap-filter) LDAP_FILTER="$2"; shift 2 ;;
    --fields) LDAP_FIELDS="$2"; shift 2 ;;
    --min-matched) MIN_MATCHED="$2"; shift 2 ;;
    --allow-empty) ALLOW_EMPTY=1; shift ;;
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

[[ "$MIN_MATCHED" =~ ^[0-9]+$ ]] || die "--min-matched must be a non-negative integer"

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

# Apply umask before creating the output directory so every generated file is
# private from the moment it exists, including the directory itself.
umask 077
mkdir -p "$OUTPUT_DIR"
chmod 700 "$OUTPUT_DIR"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
ldif="$OUTPUT_DIR/ldap-directory-$stamp.ldif"
csv="$OUTPUT_DIR/ldap-directory-$stamp.csv"
sql="$OUTPUT_DIR/ldap-directory-$stamp.sql"
backup_sql="$OUTPUT_DIR/ldap-directory-$stamp.backup.sql"
backup_csv="$OUTPUT_DIR/ldap-directory-$stamp.backup.csv"
rollback_sql="$OUTPUT_DIR/ldap-directory-$stamp.rollback.sql"
: > "$csv"; chmod 600 "$csv"
: > "$sql"; chmod 600 "$sql"

pw_tmp=""
pg_tmp_dir=""
kube_remote_dir=""
cleanup() {
  if [[ -n "$pw_tmp" ]]; then
    rm -f "$pw_tmp"
  fi
  if [[ -n "$pg_tmp_dir" ]]; then
    rm -rf "$pg_tmp_dir"
  fi
  if [[ -n "$kube_remote_dir" ]]; then
    "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      exec -i -c "$PG_CONTAINER" "$PG_POD" -- rm -rf "$kube_remote_dir" >/dev/null 2>&1 || true
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
    "${LDAP_ATTRS[@]}" > "$ldif"
fi

python3 - "$ldif" "$csv" "$sql" "$backup_sql" "$LDAP_FIELDS" "$MIN_MATCHED" <<'PY'
import base64
import csv
import re
import sys
import uuid

ldif_path, csv_path, sql_path, backup_sql_path, fields_arg, min_matched_arg = sys.argv[1:7]
min_matched = int(min_matched_arg)
uuid_re = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

FIELD_ATTRS = {
    "email": "mail",
    "first_name": "givenName",
    "last_name": "sn",
    "country": "co",
    "kerberos_id": "uid",
    "cost_center": "rhatCostCenter",
    "cost_center_description": "rhatCostCenterDesc",
    "organization": "rhatOrganization",
    "job_title": "rhatJobTitle",
    "job_code": "rhatJobCode",
    "geo": "rhatGeo",
    "office_location": "rhatOfficeLocation",
    "worker_id": "rhatWorkerId",
    "supervisor_worker_id": "rhatSupervisorWorkerId",
    "employee_subtype": "rhatEmployeeSubType",
    "person_type": "rhatPersonType",
    "hire_date": "rhatHireDate",
    "term_date": "rhatTermDate",
    "deactivated": "rhatDeactivated",
    "manager": "manager",
}
TEXT_FIELDS = [f for f in FIELD_ATTRS if f != "manager"]


def parse_ldif(path):
    """Parse LDIF with continuation unfolding BEFORE base64 decoding.

    RFC 2849 folds *encoded* logical lines. A wrapped base64 value therefore
    arrives as an unfolded line ending in the full base64 payload; decoding
    each physical line first (the old behaviour) truncates or corrupts it.
    """
    logical = []
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for raw in fh:
            line = raw.rstrip("\r\n")
            if line.startswith(" "):
                if logical:
                    logical[-1] += line[1:]
                continue
            logical.append(line)

    record = {}
    for line in logical:
        if not line:
            if record:
                yield record
            record = {}
            continue
        if ":" not in line:
            continue
        attr, rest = line.split(":", 1)
        attr = attr.lower()
        if rest.startswith(":"):
            payload = rest[1:].strip()
            try:
                value = base64.b64decode(payload).decode("utf-8", "replace")
            except Exception:
                value = ""
        elif rest.startswith("<"):
            # URL-valued attributes are not used by this sync.
            value = ""
        else:
            value = rest[1:] if rest.startswith(" ") else rest
        record.setdefault(attr, []).append(value)
    if record:
        yield record


def first(record, key):
    vals = record.get(key.lower())
    return vals[0].strip() if vals else ""


def clean(value):
    return value.replace("\x00", "").strip()


def norm_dn(value):
    return re.sub(r"\s*,\s*", ",", value.strip().lower())


def uid_from_dn(value):
    match = re.match(r"uid=([^,]+)", value.strip(), re.I)
    return match.group(1).strip().lower() if match else ""


def q(value):
    if value is None or value == "":
        return "NULL"
    return "'" + value.replace("'", "''") + "'"


selected = [f.strip() for f in fields_arg.split(",") if f.strip()]
if selected == ["all"]:
    selected = list(FIELD_ATTRS)
unknown = sorted(set(selected) - set(FIELD_ATTRS))
if unknown:
    raise SystemExit(f"unknown --fields value(s): {', '.join(unknown)}")
if not selected:
    raise SystemExit("--fields must name at least one field")

records = []
dn_index = {}
uid_index = {}
total = 0
invalid_uuid = 0

for record in parse_ldif(ldif_path):
    total += 1
    raw_uuid = first(record, "rhatUUID").lower()
    if not uuid_re.match(raw_uuid):
        invalid_uuid += 1
        continue
    uid = str(uuid.UUID(raw_uuid))
    dn = first(record, "dn")
    if dn:
        dn_index.setdefault(norm_dn(dn), uid)
    login = first(record, "uid").lower()
    if login:
        uid_index.setdefault(login, uid)
    records.append((uid, record))

rows = {}
duplicates = 0
no_data = 0
manager_unresolved = 0

for uid, record in records:
    values = {}
    for field in selected:
        if field == "manager":
            continue
        attr = FIELD_ATTRS[field]
        value = clean(first(record, attr))
        if not value and field == "country":
            value = clean(first(record, "c"))
        if not value and field == "job_title":
            value = clean(first(record, "title"))
        if not value:
            continue
        if field == "deactivated":
            lowered = value.lower()
            value = "true" if lowered in ("true", "1", "yes") else "false"
        values[field] = value

    if "manager" in selected:
        manager_dn = clean(first(record, "manager"))
        if manager_dn:
            manager_uuid = dn_index.get(norm_dn(manager_dn)) or uid_index.get(uid_from_dn(manager_dn))
            if manager_uuid:
                values["manager_uuid"] = manager_uuid
            else:
                manager_unresolved += 1

    if not values:
        no_data += 1
        continue
    if uid in rows:
        duplicates += 1
        continue
    rows[uid] = values

csv_fields = ["user_id"] + [f for f in selected if f != "manager"]
if "manager" in selected:
    csv_fields.append("manager_uuid")

with open(csv_path, "w", encoding="utf-8", newline="") as fh:
    writer = csv.writer(fh)
    writer.writerow(csv_fields)
    for uid in sorted(rows):
        values = rows[uid]
        writer.writerow([uid] + [values.get(f, "") for f in csv_fields[1:]])

def write_update_sql(fh):
    fh.write("BEGIN;\n")
    fh.write("SET LOCAL lock_timeout = '10s';\n")
    fh.write("SET LOCAL statement_timeout = '120s';\n")
    columns = ["user_id uuid PRIMARY KEY"]
    columns.extend(f"  {field} text" for field in TEXT_FIELDS if field in selected)
    if "manager" in selected:
        columns.append("  manager_uuid uuid")
    fh.write("CREATE TEMP TABLE _ldap_sync (\n  " + ",\n  ".join(columns) + "\n) ON COMMIT DROP;\n")

    ordered_fields = [f for f in selected if f != "manager"]
    if rows:
        fh.write("INSERT INTO _ldap_sync (user_id, " + ", ".join(ordered_fields + (["manager_uuid"] if "manager" in selected else [])) + ") VALUES\n")
        items = sorted(rows.items())
        for index, (uid, values) in enumerate(items):
            cells = [q(uid)] + [q(values.get(field)) for field in ordered_fields]
            if "manager" in selected:
                cells.append(q(values.get("manager_uuid")))
            suffix = ",\n" if index + 1 < len(items) else ";\n"
            fh.write("  (" + ", ".join(cells) + ")" + suffix)

    fh.write("SELECT 'ldap_sync_rows=' || count(*) FROM _ldap_sync;\n")
    fh.write("SELECT 'partner_users_total=' || count(*) FROM partner_users;\n")
    fh.write("SELECT 'matched_partner_users=' || count(*) FROM _ldap_sync u JOIN partner_users pu USING (user_id);\n")
    fh.write("SELECT 'partner_users_without_ldap_match=' || count(*) FROM partner_users pu LEFT JOIN _ldap_sync u USING (user_id) WHERE u.user_id IS NULL;\n")
    if "email" in selected:
        fh.write("SELECT 'email_conflicts=' || count(*) FROM _ldap_sync u JOIN partner_users pu USING (user_id) WHERE u.email IS NOT NULL AND EXISTS (SELECT 1 FROM partner_users x WHERE x.user_id <> u.user_id AND lower(x.tags->>'email') = lower(u.email));\n")
    if min_matched > 0:
        fh.write("DO $$\nDECLARE matched integer;\nBEGIN\n")
        fh.write("  SELECT count(*) INTO matched FROM _ldap_sync u JOIN partner_users pu USING (user_id);\n")
        fh.write(f"  IF matched < {min_matched} THEN RAISE EXCEPTION 'matched_partner_users % is below --min-matched {min_matched}', matched; END IF;\n")
        fh.write("END $$;\n")

    if rows:
        object_items = []
        for field in TEXT_FIELDS:
            if field not in selected:
                continue
            if field == "email":
                object_items.append(
                    "        'email', CASE WHEN u.email IS NOT NULL AND NOT EXISTS ("
                    "SELECT 1 FROM partner_users x WHERE x.user_id <> u.user_id "
                    "AND lower(x.tags->>'email') = lower(u.email)) THEN u.email END"
                )
            else:
                object_items.append(f"        '{field}', u.{field}")
        if "manager" in selected:
            object_items.append("        'manager_uuid', u.manager_uuid::text")
        fh.write("WITH new_values AS (\n")
        fh.write("  SELECT u.user_id,\n")
        fh.write("         pu.tags || jsonb_strip_nulls(jsonb_build_object(\n")
        fh.write(",\n".join(object_items) + "\n")
        fh.write("         )) AS new_tags")
        if "manager" in selected:
            fh.write(",\n         CASE WHEN u.manager_uuid IS NULL THEN pu.manager_user_id\n")
            fh.write("              ELSE (SELECT m.user_id FROM partner_users m WHERE m.user_id = u.manager_uuid) END AS new_manager_user_id")
        fh.write("\n  FROM _ldap_sync u\n  JOIN partner_users pu USING (user_id)\n")
        fh.write("), updated AS (\n")
        fh.write("  UPDATE partner_users pu\n  SET tags = n.new_tags")
        if "manager" in selected:
            fh.write(",\n      manager_user_id = n.new_manager_user_id")
        fh.write("\n  FROM new_values n\n  WHERE pu.user_id = n.user_id\n")
        fh.write("    AND (pu.tags IS DISTINCT FROM n.new_tags")
        if "manager" in selected:
            fh.write(" OR pu.manager_user_id IS DISTINCT FROM n.new_manager_user_id")
        fh.write(")\n  RETURNING 1\n)\n")
        fh.write("SELECT 'updated_partner_users=' || count(*) FROM updated;\n")
    else:
        fh.write("SELECT 'updated_partner_users=0';\n")
    fh.write("COMMIT;\n")


with open(sql_path, "w", encoding="utf-8") as fh:
    write_update_sql(fh)

# A backup is taken before apply: one CSV row per candidate partner user, plus
# a generated rollback SQL file. Both are mode 600 under the output directory.
with open(backup_sql_path, "w", encoding="utf-8") as fh:
    fh.write("BEGIN;\n")
    fh.write("SET LOCAL statement_timeout = '120s';\n")
    fh.write("CREATE TEMP TABLE _ldap_backup_ids (user_id uuid PRIMARY KEY) ON COMMIT DROP;\n")
    if rows:
        fh.write("INSERT INTO _ldap_backup_ids (user_id) VALUES\n")
        items = sorted(rows)
        for index, uid in enumerate(items):
            suffix = ",\n" if index + 1 < len(items) else ";\n"
            fh.write(f"  ('{uid}'){suffix}")
    fh.write("COPY (\n")
    fh.write("  SELECT pu.user_id, pu.tags::text, pu.manager_user_id::text\n")
    fh.write("  FROM partner_users pu JOIN _ldap_backup_ids i USING (user_id)\n")
    fh.write("  ORDER BY pu.user_id\n")
    fh.write(") TO STDOUT WITH (FORMAT csv, HEADER true);\n")
    fh.write("COMMIT;\n")

print(f"ldap_records={total}")
print(f"ldap_records_with_selected_data={len(rows)}")
print(f"ldap_records_without_selected_data={no_data}")
print(f"ldap_records_invalid_uuid={invalid_uuid}")
print(f"ldap_records_duplicate_uuid_skipped={duplicates}")
print(f"ldap_manager_unresolved={manager_unresolved}")
PY

row_count="$(python3 - "$csv" <<'PY'
import csv
import sys
with open(sys.argv[1], "r", encoding="utf-8", newline="") as fh:
    print(max(0, sum(1 for _ in csv.reader(fh)) - 1))
PY
)"
if [[ "$APPLY" -eq 1 && "$row_count" -eq 0 && "$ALLOW_EMPTY" -ne 1 ]]; then
  die "LDAP produced zero usable rows; refusing to apply (use --allow-empty to override)"
fi

echo "LDIF:  $ldif"
echo "CSV:   $csv"
echo "SQL:   $sql"

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
[[ -n "$dburl" ]] || die "DATABASE_URL or --database-url-file is required for apply"

# Translate the URL into private libpq service/password files. Neither the URL
# nor the password is passed as a psql argument or exported in a process argv.
pg_tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/pricetag-ldap-pg.XXXXXX")"
chmod 700 "$pg_tmp_dir"
DATABASE_URL="$dburl" python3 - "$pg_tmp_dir" "$DB_SERVICE" <<'PY'
import os
import sys
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse

out_dir = Path(sys.argv[1])
service = sys.argv[2]
dsn = urlparse(os.environ["DATABASE_URL"])
if dsn.scheme not in {"postgres", "postgresql"}:
    raise SystemExit("DATABASE_URL must use postgres:// or postgresql://")
host = dsn.hostname or "localhost"
port = str(dsn.port or 5432)
user = unquote(dsn.username or "")
password = unquote(dsn.password or "")
database = unquote(dsn.path.lstrip("/"))
query = parse_qs(dsn.query)


def qv(value):
    return value.replace("\\", "\\\\").replace(":", "\\:")


lines = [
    f"[{service}]",
    f"host={host}",
    f"port={port}",
    f"user={user}",
    f"dbname={database}",
]
for key in ("sslmode", "sslrootcert", "sslcert", "sslkey", "application_name", "connect_timeout"):
    if key in query and query[key]:
        lines.append(f"{key}={query[key][0]}")
service_path = out_dir / "pg_service.conf"
service_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
os.chmod(service_path, 0o600)
pass_path = out_dir / "pgpass"
pass_path.write_text(
    ":".join([qv(host), qv(port), qv(database), qv(user), qv(password)]) + "\n",
    encoding="utf-8",
)
os.chmod(pass_path, 0o600)
PY

export PGSERVICEFILE="$pg_tmp_dir/pg_service.conf"
export PGPASSFILE="$pg_tmp_dir/pgpass"
export PGSERVICE="$DB_SERVICE"

run_psql_file() {
  local file="$1"
  case "$DB_MODE" in
    local)
      psql -w -v ON_ERROR_STOP=1 -qAt -f "$file"
      ;;
    docker)
      docker run --rm -i \
        -v "$file:/sql:ro" \
        -v "$pg_tmp_dir:/pg:ro" \
        -e PGSERVICEFILE=/pg/pg_service.conf \
        -e PGPASSFILE=/pg/pgpass \
        -e PGSERVICE="$DB_SERVICE" \
        "$DOCKER_IMAGE" psql -w -v ON_ERROR_STOP=1 -qAt -f /sql
      ;;
    kube)
      local remote="$kube_remote_dir/$(basename "$file")"
      "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
        cp -c "$PG_CONTAINER" "$file" "$PG_POD:$remote"
      "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
        exec -i -c "$PG_CONTAINER" "$PG_POD" -- chmod 600 "$remote"
      "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
        exec -i -c "$PG_CONTAINER" "$PG_POD" -- env \
          PGSERVICEFILE="$kube_remote_dir/pg_service.conf" \
          PGPASSFILE="$kube_remote_dir/pgpass" \
          PGSERVICE="$DB_SERVICE" \
          psql -w -v ON_ERROR_STOP=1 -qAt -f "$remote"
      ;;
  esac
}

if [[ "$DB_MODE" == "kube" ]]; then
  kube_remote_dir="$("$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
    exec -i -c "$PG_CONTAINER" "$PG_POD" -- mktemp -d "${PG_REMOTE_DIR%/}/pricetag-ldap-sync.XXXXXX")"
  "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
    exec -i -c "$PG_CONTAINER" "$PG_POD" -- chmod 700 "$kube_remote_dir"
  for file in pg_service.conf pgpass; do
    "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      cp -c "$PG_CONTAINER" "$pg_tmp_dir/$file" "$PG_POD:$kube_remote_dir/$file"
    "$KUBECTL" --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
      exec -i -c "$PG_CONTAINER" "$PG_POD" -- chmod 600 "$kube_remote_dir/$file"
  done
fi

# Snapshot first. The rollback file is generated from the exact CSV snapshot,
# so it can restore tags and manager_user_id without a separate query.
if grep -q 'INSERT INTO _ldap_backup_ids' "$backup_sql"; then
  run_psql_file "$backup_sql" > "$backup_csv"
  chmod 600 "$backup_csv"
else
  : > "$backup_csv"
  chmod 600 "$backup_csv"
fi

python3 - "$backup_csv" "$rollback_sql" <<'PY'
import csv
import sys

backup_path, rollback_path = sys.argv[1:3]


def q(value):
    if value is None or value == "":
        return "NULL"
    return "'" + value.replace("'", "''") + "'"


with open(backup_path, "r", encoding="utf-8", newline="") as fh:
    rows = list(csv.DictReader(fh))

with open(rollback_path, "w", encoding="utf-8") as fh:
    fh.write("BEGIN;\n")
    fh.write("SET LOCAL lock_timeout = '10s';\n")
    fh.write("SET LOCAL statement_timeout = '120s';\n")
    fh.write("CREATE TEMP TABLE _ldap_rollback (\n")
    fh.write("  user_id uuid PRIMARY KEY,\n")
    fh.write("  tags jsonb NOT NULL,\n")
    fh.write("  manager_user_id uuid\n")
    fh.write(") ON COMMIT DROP;\n")
    if rows:
        fh.write("INSERT INTO _ldap_rollback (user_id, tags, manager_user_id) VALUES\n")
        for index, row in enumerate(rows):
            suffix = ",\n" if index + 1 < len(rows) else ";\n"
            fh.write(
                "  ("
                + q(row.get("user_id"))
                + ", "
                + q(row.get("tags"))
                + "::jsonb, "
                + q(row.get("manager_user_id"))
                + ")"
                + suffix
            )
        fh.write("UPDATE partner_users pu\n")
        fh.write("SET tags = r.tags, manager_user_id = r.manager_user_id\n")
        fh.write("FROM _ldap_rollback r\n")
        fh.write("WHERE pu.user_id = r.user_id;\n")
    fh.write("COMMIT;\n")
PY
chmod 600 "$rollback_sql"

echo "Backup CSV:    $backup_csv"
echo "Rollback SQL:  $rollback_sql"

run_psql_file "$sql"
echo "Applied LDAP directory sync."
