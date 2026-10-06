# Legacy org/table removal (PR A)

This change removes the service's dependence on the legacy organization tables
so a later PR can drop them safely:

- `people`
- `person_identities`
- `user_profiles`
- quota/approval tables

## Principle

`partner_users` is the single identity source. Manager hierarchy is derived
dynamically from `partner_users.manager_user_id`; a user created before their
manager appears as a root until the manager joins, after which the tree
resolves automatically. Group-based behavior is removed. Quota enforcement and
the approval workflow are disabled and being removed.

## Migration checklist

- [x] Partner-user manager scope resolver (`PartnerManagerScope`).
- [x] Login manager redirect uses partner manager scope.
- [ ] Org tree / scope / usage APIs read partner users only.
- [ ] Remove group filters and group-based enforcement.
- [ ] Remove quota request/approval code paths.
- [ ] Display names and historical login resolution from partner users.
- [ ] Invite and identity-link flows on partner users.
- [ ] Remove legacy Admin People table/handlers.
- [ ] Parity tests covering manager scope, usage attribution, and display.
- [ ] Pre-drop guard: fail startup migration if any legacy reference remains.

## Pre-drop guard (PR B)

The follow-up PR drops the legacy tables only after this PR is deployed and the
service no longer references them. The drop migration verifies there are no
remaining foreign keys or code references before executing.
