# Concepts

Status: draft

Terms are defined here once and linked from other docs. The vocabulary for the work model is deliberately unnamed: the
PoC's names were wrong and replacements are undecided. Until they are chosen, units are identified by role. See
[0004](decisions/0004-work-model.md).

## Work units

Work is organized in a fixed set of levels, each tied to a git artifact.

| Role | Git artifact | Meaning |
| --- | --- | --- |
| Top-level unit | None | A received instruction, such as a feature, broken down into slices. It groups the branches and pull requests of its middle units. |
| Middle unit | Branch and pull request | A slice of work that leaves the result stable and usable when merged. |
| Bottom unit | Commit | The smallest unit of work done. Corresponds to an item in an external tracker. |

"Result" means whatever is being developed: an application, documentation, and so on.

Parent and child rules:
- A top-level unit has one or more middle units. A single middle unit is valid and preferred over splitting work into
  two slices that would not each stand alone.
- A middle unit always has a top-level parent.
- A middle unit has one or more bottom units.
- A bottom unit has a middle unit as parent, or is an orphan task.

### Orphan task

A bottom unit that belongs to no middle or top-level unit. Used for work that needs no breakdown, such as a bugfix or
"change the accent color to a given value". It is not limited to bugfixes.

By default an orphan task gets its own branch and pull request. This must be implemented so it can become configurable
later. How orphan tasks are received and completed is open. See [0007](decisions/0007-orphan-task-delivery.md).

### Ordering

Middle units under one top-level unit are worked sequentially where order matters. It does not always matter. Parallel
work is avoided because of the complexity it adds. See [0004](decisions/0004-work-model.md).

### Review feedback

Feedback received on a pull request becomes a new bottom unit appended to that pull request's middle unit. See
[0009](decisions/0009-review-feedback-and-verification.md).

## Phases

The method is Receive, Articulate, Loop, Pull request, Human review, as in [the Manifesto](../MANIFESTO.md). Feature
work passes through Receive and Articulate to produce top-level, middle and bottom units. An orphan task skips the
breakdown; its flow is open.

## External tracker

A service such as Jira or GitHub Issues that holds work items outside ralph. See
[0005](decisions/0005-external-tracker-linking.md).

## Link

The association between a ralph record and the corresponding item in an external tracker. Required for every
integrated record, whichever side holds the data.

## Instance

A running ralph backend that clients use: local to one developer, self-hosted with the built-in API server, or the
hosted commercial service.

## Fingerprint

A value derived from a record's content at the time a client reads it. A client sends it back when writing, so the
server can detect that the record changed in between. See [0006](decisions/0006-shared-state-and-concurrency.md).
