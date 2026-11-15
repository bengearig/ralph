# 0006: Shared state lives on the server, which owns concurrency

Status: settled in principle; details open

## Context

Developers who share an instance can edit the same records at once, and a client's last read can go stale.

## Decision

- Shared state is provided by a hosted ralph API server, whether self-run or the commercial service.
- The server's endpoint handlers handle concurrency and staleness. A write carries the fingerprint of the record as the
  client last read it. If the record has changed since, the server rejects the write with an error that includes a path
  to reconciliation.
- Every user of an instance sees everything in it. The design must allow permissions to be added later without
  restructuring.

## Alternatives rejected

- Last write wins: loses changes silently.
- Clients coordinating among themselves: no authority to arbitrate.

## Consequences

- Records exposed for writing need a fingerprint.
- The reconciliation path, the error contract, and what permissions attach to are open.
