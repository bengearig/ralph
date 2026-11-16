# 0004: Work model

Status: draft (the author leans toward this and is open to change)

## Context

The PoC's hierarchy was too rigid and wrongly named, and gave quick work no place to go. What it got right: a received
instruction became slices, each fit for one pull request, each made of tasks that are each one commit. After any slice
merges, the result is stable and usable.

## Decision

- Keep a fixed set of levels for ralph-style development, mapped to git: middle unit to branch, pull request and
  stable point, bottom unit to commit. The top-level unit has no git artifact; it groups the middle units that deliver
  one received instruction.
- Middle units under one top-level unit are worked sequentially where order matters, and it may not always matter.
  Parallel work is avoided because of the complexity it adds.
- A top-level unit may have a single middle unit, and that is encouraged when one slice makes more sense than two.
- A middle unit must have a top-level parent.
- A bottom unit may exist with no parent (an orphan task) for quick work. See
  [0007](0007-orphan-task-delivery.md).
- Each middle unit leaves the result stable and usable when merged.
- Bottom units are the level most naturally mapped to tickets in a task manager, so the bottom unit is named Task.
  The names of all levels and states are in [0026](0026-work-level-and-state-names.md).

## Alternatives rejected

- Arbitrary nesting: not adopted so far; the author prefers fixed levels but is open to it if it serves the goals
  better.

## Consequences

- Work in a ralph instance is always representable as git branches, pull requests and commits.
- No parallel execution means no coordination between concurrent branches of one top-level unit. Parallel work can
  be reconsidered later; it is not excluded forever.
- Orphan task handling must define its branch and pull request. See [open-questions.md](../open-questions.md).
