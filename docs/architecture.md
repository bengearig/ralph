# Architecture

Status: draft

How ralph is built so one CLI serves a solo developer and a shared instance alike. Terms are in
[concepts.md](concepts.md). Rationale is in [0012](decisions/0012-service-layer-and-per-service-backends.md) and
[0013](decisions/0013-implementation-stack.md).

## Shape

- One program, delivered as a single static binary, contains the CLI, the service layer and the API server.
- The service layer is a set of interfaces. Each has a local implementation and a remote implementation that is an
  HTTP client of the API server. The API server exposes the same interfaces over HTTP and backs them with local
  implementations.
- Local use needs no server process. The CLI calls local implementations in-process.

## Services

Each service is configured independently as local or remote. Services include the storage of ralph's records, pull
request handling and tracker sync. The full list is open.

- Storage holds work records, documents and their links. Locally it is SQLite. It can be configured to use a remote
  instance, which is how developers share state. See [0006](decisions/0006-shared-state-and-concurrency.md).
- Pull request handling reads and writes pull request state with the hosting provider. When remote, the server holds
  the provider's API token, so pull requests can be created under one user. When local, the token is the individual
  user's and stays on their machine. Ralph records who did the work either way. See
  [0015](decisions/0015-pull-request-identity.md).
- Any other service follows the same rule: an interface with a local and a remote implementation, selected by
  configuration.

Because services are mixed per configuration, no operation spans two services in one transaction. Records that
reference one another do so by identifier, and a service must tolerate a reference it cannot resolve.

## What always runs locally

Execution stays on the developer's machine:
- The harness and the loop.
- Git operations against the developer's checkout.
- Verification commands.

The server never runs a harness or a loop. Remote loop execution is out of scope.

## API server

The server exposes the service interfaces over HTTP and handles concurrency and staleness for shared records, as in
[0006](decisions/0006-shared-state-and-concurrency.md). Receiving webhooks for
[0010](decisions/0010-sequencing-and-merge-detection.md) is part of it.

Storage, authentication and tenancy are configured as in
[0014](decisions/0014-server-storage-authentication-tenancy.md): SQLite or Postgres, personal API tokens, and
multi-tenant or single-tenant. Permissions are open and must be addable later without restructuring.

## Open

See [open-questions.md](open-questions.md).
