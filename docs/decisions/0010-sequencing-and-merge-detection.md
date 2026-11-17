# 0010: Sequencing and merge detection

Status: draft

## Context

Middle units that depend on a previous one need that one merged, and a human review can take a long time. Ralph needs
to learn about merges, and about other changes to pull requests, without being a hosting provider itself.

## Decision

- Whether a dependent middle unit waits for the previous merge or stacks on the previous branch is configurable. The
  default is to wait: the next middle unit starts after the previous one merges, branching from the updated base.
  Stacking is an option, not required in the first release.
- Order between middle units is recorded as a dependency: one middle unit waits for another. Any two middle units may
  be tied, including ones under different top-level units, and a cycle is rejected. Dependencies are plain records
  with no versioning of their own.
- A middle unit that waits for another cannot have a bottom unit started until the unit it waits on is satisfied. Until
  merges are detected, a dependency is satisfied when the unit it waits on is done; once merges are detected, it is
  satisfied when that unit's work is merged, or when it is done and has no pull request. Removing the dependency lets the waiting unit start.
- Ralph can learn about pull request changes in three ways, and all are planned: polling the hosting provider, a
  webhook into the API server, and a command the user runs. Each feeds the same handling, so behavior does not depend
  on how a change was detected. The user command is `ralph pr check <slice>`, which reads the slice's latest pull
  request and records what changed through the same handler the other paths will use.
- Viewing and acting are separate. `ralph monitor` shows what is happening in the project and polls the provider for
  updates, and changes nothing. `ralph apply` is the separate action: it records what the host reports and applies what
  follows a detected change, such as creating bottom units from review feedback, closing a merged unit or starting the
  next one. Both use the same handler, in internal/followup, so a user can look without anything changing. Acting
  without being asked is off by default: what a run with no flags applies is the user-only setting `follow_up.auto`,
  empty by default. See [0011](0011-after-merge-and-closed-pull-requests.md).

## Alternatives rejected

- Stacking by default: retargeting and rebasing when the earlier branch changes in review, and feedback on the earlier
  pull request triggers exactly that.
- A single detection path: polling alone is slow, webhooks need a reachable server, and manual alone is easy to forget.

## Consequences

- The pull request provider integration supports reading state, feedback and merges, and optionally receiving webhooks.
- Webhooks apply only to instances that run the API server, so they are not in the first release. See
  [0021](0021-first-release-scope.md).
- The monitor, `ralph apply` and the other detection paths share one handler for changes, which is the unit to specify
  and test.
- A merge satisfies the dependencies on the merged slice: a slice that waits for it can start once the merge is
  recorded, and until then it shows what it waits for.
- Views show what a middle unit waits for, and starting its work names the unfinished dependencies.
- Review latency stalls a feature under the default. Users who need more throughput opt into stacking later.
