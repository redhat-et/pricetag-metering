# Changelog

Release notes for the EnMaaS PriceTag metering service.

The `Unreleased` section is updated as changes land. When a release is
published, move its completed entries into a dated version section and link
the section to the corresponding GitHub tag.

## [Unreleased]

### Added

Nothing yet.

### Changed

Nothing yet.

### Fixed

Nothing yet.

## [0.2.0] - 2026-10-07

This is the first tagged EnMaaS release of the metering service.

### Added

- A super-admin Safety Net control with a `$600` monthly per-user default.
- Exact-model over-limit allowance for `rits/zai-org/glm-5-3`.
- Read-only Model Restrictions visibility backed by partner-user model
  allowlists.
- Partner-user identity, role, login, key metadata, and model-policy APIs.
- Paginated administrator and usage-user views.
- Local-only Compose fixtures for synthetic users, usage, MaaS validation, and
  Praxis/LLM-Katan testing.

### Changed

- Partner users are now the canonical dashboard and authorization identity
  source.
- Dashboard access and administrative actions are restricted to administrators
  and super-administrators where appropriate.
- The welcome and operations documentation now describes EnMaaS gateway URLs,
  authentication dialects, model discovery, and model limits.

### Removed

- The legacy Quotas UI.
- The optional soft bypass ceiling; the legacy database column is retained only
  for migration compatibility and is no longer read or written.

### Fixed

- Usage-report handling for partner users.
- Initial administrator-table pagination behavior.
- Partner API authentication and partner-role resolution across dashboard
  requests.

## Links

- [Unreleased]: https://github.com/redhat-et/pricetag-metering/compare/v0.2.0...HEAD
- [0.2.0]: https://github.com/redhat-et/pricetag-metering/releases/tag/v0.2.0
