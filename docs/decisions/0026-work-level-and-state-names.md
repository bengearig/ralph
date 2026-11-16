# 0026: Work level and state names

Status: settled

## Context

The work model of [0004](0004-work-model.md) defines three levels by role, and the docs so far identified them only as
top-level, middle and bottom unit because the PoC's names were wrong and replacements were undecided. The workflow also
needs names for the states a unit of work passes through, in particular the unplanned and the ready state.

## Decision

- The top-level unit is a Brief, the middle unit a Slice, and the bottom unit a Task. An orphan task is a Task with no
  Slice.
- IDs are a letter prefix, a hyphen and a per-project number, counted per level: `B-1`, `S-3`, `T-12`.
- Only tasks store a state. The names are `PROPOSED`, `TODO`, `IN_PROGRESS`, `BLOCKED`, `CANCELLED` and `DONE`.
  `PROPOSED` is the design's unplanned state and `TODO` is its ready state.
- A slice and a brief store no state. Each derives its status from its children.

## Alternatives rejected

- Goal/Slice/Task, Epic/Story/Task and Feature/Change/Task: Epic/Story/Task would borrow words that task trackers define
  differently.
- New words for the states: the author's habits and the older tool's commands carry over with the existing names.
- A stored state on every level: two places that can disagree.
- Random identifiers: unspeakable.
- A hidden identifier plus a display number: a second identifier everywhere, for a server that does not exist yet.

## Consequences

- Other documents may keep the role words where they explain the mapping, and use the names elsewhere.
- Status for a slice or brief is computed, so it can never contradict its tasks.
- Numbers are per level and per project, so IDs are only unique together with their prefix.
