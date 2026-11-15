# 0008: Feature flow shape

Status: draft

## Context

Feature work needs the full method. The PoC's flow was shaped by one workplace; the new flow must fit many.

## Decision

- Receive stores the brief verbatim and proposes the top-level unit, its middle units and rough bottom units. Approval
  before creation is optional and configurable. Revisions always ask.
- Bottom units are explicitly unplanned or ready. Articulate promotes a unit to ready with the user. The loop only
  works ready units. State names are undecided.
- The loop runs headless, outside the user's session, one bottom unit per iteration.
- Documents are first-class records that the loop can retrieve as context, project-wide or tied to a top-level unit.

## Alternatives rejected

- Mandatory approval of every outline: heavy for users who trust it.
- Units ready at creation, or readiness derived from content: no point at which the user confirms the detail.
- Loop inside the session: ties up the session and its context grows with every iteration.
- Repo files only for documents: no search or retrieval shared across an instance.

## Consequences

- The model has distinct unplanned and ready states, and the loop must refuse unready units.
- Documents need storage, search and chunking suited to retrieval, and a defined relationship to repo files.
- A headless loop needs its own permission, budget and logging settings per harness.
