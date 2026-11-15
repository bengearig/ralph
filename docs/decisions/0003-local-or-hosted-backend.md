# 0003: Backend services run locally or against a hosted API

Status: settled in principle; details open

## Context

Local-only storage isolates developers from one another. Teams and companies need shared state. Solo developers need
something that works with no account or server.

## Decision

The CLI contains an API web server. Backend services are configured to run either locally or against an API, whether
self-hosted with that server or provided as a commercial hosted service.

## Alternatives rejected

- Local only: no sharing.
- Hosted only: excludes hobbyists and organizations that cannot or will not use a hosted service.

## Consequences

- The CLI and the API must share one service contract, so local and remote modes behave the same.
- Which services are swappable, and the contract itself, are open. See [open-questions.md](../open-questions.md).
- The choice is made per service. See [0012](0012-service-layer-and-per-service-backends.md).
