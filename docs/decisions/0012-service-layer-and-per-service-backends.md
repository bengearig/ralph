# 0012: One service layer, per-service backends

Status: draft

## Context

[0003](0003-local-or-hosted-backend.md) established that backend services run locally or against an API. It left
open how the CLI relates to the server and how fine the choice is.

## Decision

- The CLI calls a service layer of interfaces. Each interface has a local implementation and a remote implementation
  that is an HTTP client of the API server. The server exposes the same interfaces.
- Each service chooses local or remote independently by configuration.
- Storage can be remote. Pull request handling can be remote, so a server can hold the hosting provider's token, for
  example to create pull requests under one user. Otherwise it runs locally under the individual user's token.
- Loop execution, the harness, git operations and verification are always local. Remote loop execution is excluded.

## Alternatives rejected

- The CLI always acting as an API client, with a local server started automatically: a background process for every
  solo user.
- Separate local and server implementations: they drift apart.
- One backend for the whole project: too coarse for the token-holding case.
- Remote loop execution: a much larger product (sandboxing, secrets, cost).

## Consequences

- Per-service choice enlarges the configuration and test surface. Combinations beyond what is tested may be refused,
  and which combinations the first release supports is open.
- No operation spans two services in one transaction.
- Every service interface must be specified so that local and remote implementations behave identically, including
  errors.
