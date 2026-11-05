# LDAP directory sync service

Runs the PriceTag LDAP directory sync from a VPN-connected host. The
OpenShift cluster cannot resolve or reach Red Hat IPA, so this cannot be a
Kubernetes CronJob in the EnMaaS cluster. The service runs once on start and
then every `LDAP_SYNC_INTERVAL_SECONDS` (default **86400**, once per 24 hours).
A failed run is logged and retried after `LDAP_SYNC_RETRY_SECONDS`
(default **900**); successful runs wait the configured interval.

The underlying script is `scripts/sync-ldap-cost-centers.sh`. It is
read-only against LDAP and writes only:

- selected keys in `partner_users.tags`, and
- `partner_users.manager_user_id` when the LDAP `manager` DN resolves to a
  partner user.

It never provisions users, never writes `updated_at`, and never changes a row
whose values already match. Every apply writes a pre-apply CSV snapshot and a
generated rollback SQL file under the output directory.

## Fields kept current

| LDAP attribute | PriceTag field |
|---|---|
| `rhatUUID` | `user_id` (match key) |
| `mail` | `tags.email` (opt-in; unique-email conflicts are skipped) |
| `givenName` / `sn` | `tags.first_name` / `tags.last_name` |
| `co` or `c` | `tags.country` |
| `uid` | `tags.kerberos_id` (opt-in) |
| `rhatCostCenter` | `tags.cost_center` |
| `rhatCostCenterDesc` | `tags.cost_center_description` |
| `rhatOrganization` | `tags.organization` |
| `rhatJobTitle` or `title` | `tags.job_title` |
| `rhatJobCode` | `tags.job_code` |
| `rhatGeo` | `tags.geo` |
| `rhatOfficeLocation` | `tags.office_location` |
| `rhatWorkerId` | `tags.worker_id` |
| `rhatSupervisorWorkerId` | `tags.supervisor_worker_id` |
| `rhatEmployeeSubType` | `tags.employee_subtype` |
| `rhatPersonType` | `tags.person_type` |
| `rhatHireDate` / `rhatTermDate` | `tags.hire_date` / `tags.term_date` |
| `rhatDeactivated` | `tags.deactivated` (`true`/`false`) |
| `manager` DN | `tags.manager_uuid` + `manager_user_id` |

The default service field set is `directory` (everything above except `mail`
and `uid`). `email`/`kerberos_id` are opt-in via `--fields all` or an explicit
list. This is deliberate: a direct `partner_users.tags.email` write does not
reconcile `partner_user_logins`, so use the partner API for email changes
unless you have extended the SQL to update logins in the same transaction.

Empty LDAP values are skipped, so a missing attribute never erases a value
that PriceTag already has. A `manager` DN is resolved through the same LDAP
result set; unresolved manager DNs are counted in the run summary and leave
the existing manager untouched. When a manager UUID resolves but that manager
is not a partner user yet, the sync creates a minimal `partner_users` row for
them (directory name, optional email, `created_by`/`updated_by = ldap-sync`)
before setting `manager_user_id`, so the report stays under the new manager in
the org chart. Rollback removes those rows again unless SSO has since claimed
them.

Use `--fields` to narrow the sync for a one-off run, for example:

```bash
./scripts/sync-ldap-cost-centers.sh \
  --fields cost_center,cost_center_description,manager --apply ...
```

## Safety properties

- LDAP is read-only; no LDAP writes, provisioning, or password changes.
- The only `partner_users` rows created are minimal manager stubs for a
  resolved manager DN that is not a partner user yet. They are recorded in the
  backup file and deleted by rollback (unless SSO has since claimed them).
- `--apply` is required. Dry run is the default.
- The DB URL is translated to a temporary libpq service file and `PGPASSFILE`;
  the URL/password never appears in a `psql` argv or process command line.
  Kube mode copies private `pg_service.conf`/`pgpass` files into a mode-700
  temp directory in the pod and removes them on exit.
- LDIF continuation lines are unfolded **before** base64 decoding.
- `lock_timeout=10s` and `statement_timeout=120s` bound the apply transaction.
- Only changed rows are updated (`IS DISTINCT FROM`), and `updated_at` is not
  touched.
- `LDAP_SYNC_MIN_MATCHED` aborts an apply if a partial LDAP result would match
  fewer rows than expected. A zero-row result is also refused unless
  `--allow-empty` is passed.
- The service refuses to run with a password file that is not mode 600/400.
- Generated LDIF/CSV/SQL/backup/rollback files are mode 600 under a mode-700
  output directory.

## Docker Compose on the VPN host

1. Copy the example env and set absolute host paths:

   ```bash
   cd deploy/ldap-sync
   cp .env.example .env
   chmod 600 .env
   $EDITOR .env
   ```

2. Put secrets on the host with restrictive permissions:

   ```bash
   install -m 600 /dev/null ~/.ldap_key
   # paste the enmaas-ldap password into ~/.ldap_key
   install -m 600 /dev/null ~/.config/pricetag/database-url
   # paste the postgres://... URL into that file
   chmod 600 ~/enmaas-ldap/2022-IT-Root-CA.pem
   ```

3. Build and start:

   ```bash
   docker compose -f deploy/ldap-sync/compose.yaml --env-file deploy/ldap-sync/.env up -d --build
   docker compose -f deploy/ldap-sync/compose.yaml --env-file deploy/ldap-sync/.env logs -f ldap-sync
   ```

The service logs one line per run with the timestamp. The sync script prints
only aggregate counts, never LDAP records. Output snapshots/rollback files are
in `LDAP_SYNC_OUTPUT_DIR_HOST` on the host.

## systemd alternative (no Docker)

Install the repo at `/opt/pricetag-metering`, create a `pricetag-ldap-sync`
user, and put the service environment in `/etc/pricetag-ldap-sync/env`:

```bash
sudo install -d -m 700 -o pricetag-ldap-sync -g pricetag-ldap-sync /var/lib/pricetag-ldap-sync
sudo install -m 644 deploy/ldap-sync/systemd/pricetag-ldap-sync.service /etc/systemd/system/
# /etc/pricetag-ldap-sync/env should set the same LDAP_* variables, with
# LDAP_SYNC_OUTPUT_DIR=/var/lib/pricetag-ldap-sync
sudo systemctl daemon-reload
sudo systemctl enable --now pricetag-ldap-sync.service
```

The service loop reads `LDAP_SYNC_INTERVAL_SECONDS` from the environment file,
so the 24-hour cadence is configuration, not a hard-coded timer.

## First run and verification

Always dry-run before the first service start:

```bash
./scripts/sync-ldap-cost-centers.sh \
  --password-file ~/.ldap_key \
  --ca-file ~/enmaas-ldap/2022-IT-Root-CA.pem \
  --database-url-file ~/.config/pricetag/database-url \
  --fields directory \
  --dry-run
```

Review the printed aggregate counts and the SQL file. Then apply once:

```bash
./scripts/sync-ldap-cost-centers.sh ... --apply
```

The apply prints `matched_partner_users`, `partner_users_without_ldap_match`,
`email_conflicts`, `updated_partner_users`, `manager_stub_candidates`, and
`manager_stubs_created`. A healthy production run currently matches about
3,348 users; the six unmatched rows are expected (active users without an LDAP
record plus inactive load-test rows).

Post-apply checks:

```sql
SELECT count(*) FROM partner_users WHERE tags ? 'cost_center';
SELECT count(*) FROM partner_users WHERE tags ? 'manager_uuid';
SELECT count(*) FROM partner_users WHERE manager_user_id IS NOT NULL;
```

## Rollback

Every apply leaves `ldap-directory-<stamp>.backup.csv` and
`ldap-directory-<stamp>.rollback.sql`. To restore exactly the tags and
manager links captured before that apply:

```bash
psql ... -v ON_ERROR_STOP=1 -f "$OUTPUT_DIR/ldap-directory-<stamp>.rollback.sql"
```

Treat both files as sensitive PII and delete them when they are no longer
needed. The rollback SQL restores `tags` and `manager_user_id`, deletes manager
rows this run created, and does not touch `updated_at` because the sync never
changed it.

## Scheduling notes

- Default cadence: 86400 seconds (24 hours). The loop is the scheduler; there
  is no cron/timer dependency. A failed run retries after
  `LDAP_SYNC_RETRY_SECONDS` (default 900) so a transient VPN/DB blip does not
  skip a full day.
- `LDAP_SYNC_RUN_ON_START=1` makes a restarted host reconcile immediately
  instead of waiting up to 24 hours.
- Set `LDAP_SYNC_APPLY=0` to run the service in dry-run mode while validating
  a new host.
- Keep `LDAP_SYNC_MIN_MATCHED` below the normal matched count but high enough
  to catch a broken LDAP search. A partial result is the dangerous case, not
  an empty one (empty is refused separately).
