# 0005: External trackers are linked, and the source of truth is configurable

Status: settled in principle; details open

## Context

Integration with Jira, GitHub Issues and similar services is essential to adoption. The PoC treated its own database as
the source of truth and pushed changes to Jira. Other users want people to populate the tracker and have ralph read from
it, or want ralph to hold no copy of some data and fetch it from the tracker on demand, for example answering a request
for a task description with the tracker card's description.

## Decision

- Every ralph record that has a tracker counterpart carries a link to it.
- Which side is the source of truth is configurable. The supported arrangements include ralph as the source with
  changes synced out, the tracker as the source with changes synced in, and the tracker as the only holder of some
  data with ralph reading through to it.
- Which arrangements exist in the first release is open.

## Alternatives rejected

- Ralph always the source of truth: forces the PoC's behavior on users whose process starts in the tracker.
- Tracker always the source of truth: leaves out users with no tracker.

## Consequences

- Reads of tracker-backed data go through an abstraction that works for stored and proxied data alike.
- Sync direction, conflict handling and per-field versus per-record ownership need design.
