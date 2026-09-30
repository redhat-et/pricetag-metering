# User Usage Report API

`GET /api/v1/usage/users` returns usage for a list of SSO user IDs over an
explicit time window. The endpoint is intended for the AIR/Atlas backend, not
browser code.

## Authentication

Send the dedicated `USAGE_REPORT_API_SECRET` as a bearer token:

```http
Authorization: Bearer <USAGE_REPORT_API_SECRET>
```

The endpoint fails closed: an unset secret returns `503`, and a missing or
incorrect token returns `401`. Keep the credential in the partner backend and
derive user IDs from verified SSO identity or an approved directory query.

## Request

```http
GET /api/v1/usage/users?user_ids=8f3...%2C91a...&from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z
Authorization: Bearer <USAGE_REPORT_API_SECRET>
Accept: application/json
```

`user_ids` is a comma-separated list (it may also be repeated). `from` is
inclusive and `to` is exclusive. Both accept RFC3339 timestamps or
`YYYY-MM-DD`; the window may not exceed 366 days and at most 100 users may be
requested.

Unknown user IDs return `404`; malformed windows return `400`.

## Response

```json
{
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z",
  "asOf": "2026-09-30T12:00:00Z",
  "users": [
    {
      "userId": "8f3...",
      "username": "alice@example.com",
      "tags": {
        "email": "alice@example.com",
        "first_name": "Alice",
        "last_name": "Example",
        "manager_uuid": "manager-uuid"
      },
      "totals": {
        "requests": 42,
        "promptTokens": 12000,
        "completionTokens": 3400,
        "totalTokens": 15400,
        "cachedInputTokens": 2300,
        "cacheCreationTokens": 800,
        "reasoningTokens": 500,
        "estimatedCostUsd": 0.4821
      },
      "models": [
        {
          "provider": "anthropic",
          "model": "claude-sonnet-4-5",
          "requests": 42,
          "promptTokens": 12000,
          "completionTokens": 3400,
          "totalTokens": 15400,
          "cachedInputTokens": 2300,
          "cacheCreationTokens": 800,
          "reasoningTokens": 500,
          "estimatedCostUsd": 0.4821
        }
      ]
    }
  ]
}
```

Users with no events in the window are included with zero totals and an empty
`models` array. `estimatedCostUsd` uses PriceTag's current model pricing and
fallback rates, so it is an estimate rather than a provider invoice.

## Errors

| Status | Meaning |
|---|---|
| `400` | Missing/invalid user list or time window |
| `401` | Missing or invalid bearer token |
| `404` | At least one user ID does not exist |
| `500` | Storage/report operation failed |
| `503` | API secret is not configured |

Responses contain billing data. Use `Cache-Control: no-store` and do not log
the bearer token, user assertions, or full response.
