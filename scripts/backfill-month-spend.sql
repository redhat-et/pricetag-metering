-- Backfill the exact month-to-date accumulator before setting
-- MONTH_SPEND_ENABLED=true. Run with the metering writer role during the
-- controlled post-rollout step, not from application startup.
--
-- The application maintains user_month_spend for all new events regardless of
-- MONTH_SPEND_ENABLED. Run this only after every metering pod runs the version
-- containing that upsert, otherwise old pods can create a race by writing raw
-- events without updating the accumulator.

BEGIN;

-- Block accumulator upserts while the authoritative snapshot is replaced.
LOCK TABLE user_month_spend IN ACCESS EXCLUSIVE MODE;

TRUNCATE user_month_spend;

INSERT INTO user_month_spend (username, month, spent_usd, tokens, requests)
SELECT username,
       date_trunc('month', timestamp)::date,
       COALESCE(SUM(cost_usd), 0),
       COALESCE(SUM(total_tokens), 0),
       COUNT(*)
FROM usage_events
GROUP BY username, date_trunc('month', timestamp)::date;

COMMIT;

-- Verify before enabling MONTH_SPEND_ENABLED=true:
-- SELECT COUNT(*) FROM user_month_spend;
-- SELECT SUM(spent_usd), SUM(tokens), SUM(requests) FROM user_month_spend;
-- Compare these with the equivalent usage_events aggregates.
