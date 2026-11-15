# 0014: Server storage, authentication and tenancy

Status: draft

## Context

The API server must serve a small team on a laptop and an enterprise or hosted deployment. See
[0012](0012-service-layer-and-per-service-backends.md).

## Decision

- Server storage is chosen by configuration. SQLite and Postgres are supported.
- Clients authenticate with personal API tokens. Other methods may be added later for enterprise needs, so
  authentication sits behind an interface and nothing else depends on tokens specifically.
- Tenancy is chosen by configuration. An instance runs either multi-tenant, with organizations and projects, or as a
  simple single-tenant instance.
- All users of an instance, or of a tenant within it, see everything in it for now. The design allows permissions to
  be added later. See [0006](0006-shared-state-and-concurrency.md).

## Alternatives rejected

- SQLite only: not enough for many concurrent clients.
- Postgres only: too heavy for a small team that wants a single binary.
- Multi-tenant only: unnecessary weight for self-hosting.
- SSO in the first release: deferred; tokens cover the first release.

## Consequences

- Storage must be specified so SQLite and Postgres behave the same, including fingerprint checks on write.
- The data model must support both tenancy modes. How single-tenant maps onto organizations and projects is open.
- Every request resolves to a user, and in multi-tenant mode to an organization and project, before reaching a service.
