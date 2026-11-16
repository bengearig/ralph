# 0005: External work items are linked, and the source of truth is configurable

Status: settled in principle; details in [0020](0020-task-manager-and-issue-source.md)

## Context

Integration with systems such as Jira and GitHub Issues is essential to adoption. The PoC treated its own database as
the source of truth and pushed changes to Jira. Other users want people to populate tickets and have ralph read from
them, or want ralph to hold no copy of some data and fetch it on demand, for example answering a request for a task
description with the ticket's description.

## Decision

- Every ralph record that has a counterpart in an outside system carries a link to it.
- Which side is the source of truth is configurable. The supported arrangements include ralph as the source with
  changes pushed out, the outside system as the source with changes copied in, and the outside system as the only holder
  of some data with ralph reading through to it.
- Which arrangements exist in the first release is decided in [0020](0020-task-manager-and-issue-source.md).

## Alternatives rejected

- Ralph always the source of truth: forces the PoC's behavior on users whose process starts in an outside system.
- The outside system always the source of truth: leaves out users with no such system.

## Consequences

- Reads of data held in an outside system go through an abstraction that works for stored and proxied data alike.
- Sync direction, conflict handling and per-field versus per-record ownership need design.
