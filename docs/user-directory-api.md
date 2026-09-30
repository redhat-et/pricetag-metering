# SSO User Directory and Key API

These APIs are for the trusted Atlas/AIR backend. They are not browser APIs.
Configure `USER_API_SECRET` (or the compatibility alias
`USER_DIRECTORY_API_SECRET`) and send it as:

```http
Authorization: Bearer <USER_API_SECRET>
```

An unset secret returns `503`; a missing or invalid token returns `401`.

## User record

The immutable `user_id` is separate from the MaaS username. The `tags` object
stores required identity fields and additional organization attributes without
schema changes. By default `email`, `first_name`, and `last_name` are required;
`USER_REQUIRED_TAGS` can add deployment-specific requirements such as
`manager_uuid`.

```json
{
  "user_id": "rh-user-123",
  "tags": {
    "email": "alice@example.com",
    "first_name": "Alice",
    "last_name": "Example",
    "manager_uuid": "manager-uuid",
    "country": "US"
  }
}
```

## CRUD

| Method | Path | Behavior |
|---|---|---|
| `POST` | `/api/v1/users` | Create a user; returns `201` |
| `GET` | `/api/v1/users` | List users |
| `GET` | `/api/v1/users/{user_id}` | Read one user |
| `PUT` | `/api/v1/users/{user_id}` | Replace all tags |
| `PATCH` | `/api/v1/users/{user_id}` | Merge the supplied tags |
| `DELETE` | `/api/v1/users/{user_id}` | Delete the directory record |

List filters use exact tag matches, for example:

```text
GET /api/v1/users?tag.manager_uuid=manager-uuid
GET /api/v1/users?search=alice
```

Deleting a user does not delete historical usage events.

## Mint a MaaS key

```http
POST /api/v1/users/rh-user-123/keys
Content-Type: application/json
Authorization: Bearer <USER_API_SECRET>

{
  "name": "Atlas development",
  "description": "Created from Atlas onboarding",
  "expiresIn": "720h",
  "labels": {"created_by": "atlas"}
}
```

The service first verifies that `rh-user-123` exists, reads its `tags.email`,
and calls the private MaaS API. The request always uses the `GE` group; callers
cannot select or omit a group. MaaS returns the plaintext key once. Never log
or persist the response's `key` field, and do not blindly retry an ambiguous
timeout because the key may already have been created.
