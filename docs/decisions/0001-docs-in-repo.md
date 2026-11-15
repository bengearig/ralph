# 0001: Design docs live in the repo

Status: settled

## Context

The design must be reviewable and usable by agents with no other context. The PoC store is tied to the PoC, which is
being replaced, and its contents are not visible in git.

## Decision

Design documentation is markdown under `docs/` and is the source of truth. While the PoC is in use, the files are
mirrored into its store with `/ralph:import` for search.

## Alternatives rejected

- PoC store only: couples the design to the tool being replaced and keeps it out of version control.

## Consequences

- Changes to the design arrive as reviewable diffs.
- The store mirror can lag the files; the files win on any conflict.
