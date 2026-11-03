# Dashboard / manager / admin query performance audit

Date: 2026-11-03
Scope: every read behind `/dashboard`, `/manager`, `/account`, the admin
console, and the partner usage APIs. Measurements were taken against the
dogfood CNPG cluster, which now holds the full November EnMaaS import
(2.35M `usage_events` rows, 1.35M of them in November; 31K `usage_hourly`
rows). Production was not queried or changed.

## What the EnMaaS Performance Readout asks of us

| Readout item | What it requires | Current state |
| --- | --- | --- |
| PSAP-3133 — dashboard load | Pre-aggregated rollups, response caching, no duplicate user queries, cheaper savings math | Done for the six dashboard aggregates (`usage_hourly` + `usage_tool_hourly`), 60s LRU + single-flight, savings CTE reuses the rollup. |
| PSAP-3111 — table growth / full scans | Dashboards must not scan the whole ledger on every load | Done on the rollup path. The manager org table was still scanning raw rows and is fixed in this change. |
| PSAP-3113 — cost recomputation | Use stored `cost_usd` instead of re-pricing every row under load | Done on all rollup reads. Raw paths (`/recent`, user drill-down, parity fallback) still price per row by design. |
| PSAP-3125 — DB observability | Keep DB pressure visible in Grafana | Unchanged; pool gauges and cache counters already exported. |
| Shared DB with ingestion | Dashboard queries must not compete with the write path | Dashboard reads use the read pool (replica when configured). The two remaining hot reads that must stay fresh use the primary and are bounded. |

## Query inventory and measurements

All timings are warm-cache, single-connection, from `EXPLAIN (ANALYZE,
BUFFERS)` or `\timing` on dogfood. "Rollup" means `usage_hourly` /
`usage_tool_hourly`; "raw" means `usage_events`.

### Admin dashboard (polled every 30s per open tab)

| Endpoint | Source | Measured | Notes |
| --- | --- | --- | --- |
| `/overview` | rollup | 23 ms | 25K rows, seq scan + `count(distinct)` |
| `/groups` | rollup | 36 ms | |
| `/users` (30 rows, no savings) | rollup | 47 ms | plus one directory enrich query (4 ms) |
| `/models` | rollup | ~40 ms | |
| `/tools` | `usage_tool_hourly` | 49 ms | top-9 pie is rendered client-side from this same response; no extra query |
| `/geo` | rollup + directory PK join | 59 ms | |
| `/cost-centers` | rollup + directory PK join | 41 ms | |
| `/recent` | raw, primary-pinned | 1–24 ms | must stay fresh for activity sparks; see follow-up |
| `/directory-users` | `partner_users` | 3 ms | now wrapped in the response cache |

Eleven requests per refresh, cached for 60s with single-flight, so a single
admin tab issues roughly six computations per minute, not eleven per poll.
The new map, sparks, and pies are **client-side renderings of these existing
responses**; they add no new SQL.

### Manager page (loaded on demand, no polling)

| Endpoint | Source | Before | After |
| --- | --- | --- | --- |
| `/api/v1/org/usage` (all org, November) | raw → rollup | **9.28 s**, ~1.07M shared buffers | **71 ms**, 32K buffers |
| `/api/v1/org/charts` (5 aggregates) | rollup | ~250–400 ms total | unchanged |
| `/api/v1/org/person` (3-day drill-down) | raw | 232 ms | unchanged |

The org table was the one real regression: it joined every login to every raw
event in the window and re-priced them, then ran an `EXISTS` subquery per
output row. It now aggregates `usage_hourly` and precomputes the manager set
when live rollups are healthy. `LastUsed` is hour-granular on that path (the
rollup's resolution); the raw path is kept for parity fallback.

### User account page

`/api/v1/me/usage/{summary,models,recent}` are scoped to the caller's logins
and bounded by the window. They ride the same rollup/cache paths as the
admin dashboard. No N+1 and no all-user scans.

### Admin console

| Surface | Query | Measured |
| --- | --- | --- |
| People list | paginated `partner_users` + `COUNT(*)` | 3–76 ms with a search term (3.1K rows) |
| Roles | `ListAllPartnerUsers` | ~2 ms |
| Model allowlists | `partner_user_model_allowlists` | tiny table |
| Key search | MaaS API, not the DB | n/a |
| Pricing repricing | chunked `usage_events` scan + `usage_hourly` rebuild | explicit admin job |

The reprice job is the only admin action that touches the whole ledger. It is
chunked and idempotent, but it should still be run off-peak; it is not part of
normal page load.

## Findings and resolutions

1. **Manager org usage scanned the raw ledger (fixed).** An all-organisation
   November window was 9.3 s and read ~8 GB of buffers from the shared primary.
   That is exactly the "dashboard load competing with ingestion" failure the
   readout warns about. Now 71 ms from `usage_hourly` (130× faster), with the
   raw query retained for the parity-fallback case.
2. **`enrichDashboardUsers` read from the primary (fixed).** It was the only
   dashboard aggregate still on the primary while its parent query used the
   read pool. It now uses `reader()`.
3. **`/directory-users` bypassed the response cache (fixed).** It was queried
   on every 30s dashboard refresh despite being static directory data. It now
   rides the 60s cache.
4. **`/recent` has no lower time bound (open, bounded today).** Unfiltered it
   is an index-only backwards scan (1 ms). With a group filter that has no
   recent rows it walks the timestamp index backwards until it finds 20
   matches: 22 ms for a small group after 48K rows scanned today, and it grows
   linearly with table age. Mitigation options are below.
5. **Raw fallback is a cliff (open).** If `DASHBOARD_USE_ROLLUPS=true` but the
   parity probe goes unhealthy, all eight aggregates silently revert to raw
   queries over the full window. The 60s cache and single-flight bound the
   damage to one computation per key per minute, but that is still multiple
   multi-second scans. The rollup tables are maintained even when parity is
   red, so serving the (slightly stale) rollups would be safer than falling
   back to raw.
6. **`/org/person` still scans the caller's raw rows three times.** It is
   scoped to one person's logins (28K rows for the busiest user, 232 ms) and
   only runs on drill-down, so it is not a polling load. A rollup variant is
   possible but not urgent.

## Recommended follow-ups (not applied here)

Index changes run on the production schema at the next deploy, and the
EnMaaS production database is out of scope for this change. These should be
scheduled deliberately:

1. `CREATE INDEX CONCURRENTLY idx_usage_events_group_ts ON usage_events (group_name, timestamp DESC)`
   — makes the filtered `/recent` lookup a bounded index scan regardless of
   table age.
2. Replace the raw fallback with "serve stale rollups + alert" (or a circuit
   breaker that returns the last cached response) so a parity blip cannot
   trigger full-ledger scans.
3. Optionally move `/org/person` onto `usage_hourly` and cache `/org/usage`
   and `/org/charts` per (scope, range) if manager concurrency grows.
4. Keep an eye on `usage_events` retention (PSAP-3111). The raw paths still
   need a bounded recent window; retention is the structural fix.
