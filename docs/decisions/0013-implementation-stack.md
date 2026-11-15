# 0013: Implementation stack

Status: draft

## Context

One program must serve as the CLI and the API server with minimal installation effort for hobbyists and enterprises
alike.

## Decision

- Go, delivered as a single static binary.
- SQLite is the local storage.

## Alternatives rejected

- Rust: the same distribution benefits with a steeper development curve.
- TypeScript on Node: installation needs a runtime.

## Consequences

- The binary contains the CLI and the API server.
- Local storage needs no external database. Storage for a server that serves many clients is open.
