# 0011: After merge, and pull requests closed without merging

Status: draft

## Context

A detected pull request change needs a defined result. A merge is the expected outcome. A closure without merging is
ambiguous: the approach may have been wrong, the work superseded, or the closure accidental. See
[0010](0010-sequencing-and-merge-detection.md) for how changes are detected.

## Decision

After a merge:
- The merged middle unit and its bottom units are closed.
- The branch is deleted.
- The next middle unit waits for the user to start the loop, unless auto-continue is active. Auto-continue starts the next
  middle unit by itself once the previous one merges. Its definition is open.

After a closure without merging:
- Ralph records the closure and stops. The branch and commits are left untouched.
- The middle unit is marked as needing attention. Dependent middle units keep waiting, and the monitor and status show
  why.
- The user chooses one of: reopen with a new pull request from the same branch, re-articulate the unit (the Kickback
  Clause), or cancel the unit and anything depending on it.

As implemented, a closure is recorded when `ralph pr check` reads the pull request from the host: the pull request's
record becomes closed and the slice gets a note, and the slice's status is `NEEDS_ATTENTION` while its latest pull
request is closed with no decision. A brief with such a slice is `NEEDS_ATTENTION` too, unless one of its slices is
blocked. `ralph loop` and `ralph pr create` refuse the slice and name the three choices, which are commands:
- `ralph pr reopen` opens a new pull request from the same branch and resolves the closed one as reopened.
- `ralph pr rearticulate` moves every task that is not done or cancelled back to proposed, with a kickback note, and
  resolves the closed pull request as rearticulated; the plan is then made again with receive and articulate.
- `ralph pr cancel` cancels the unfinished tasks and resolves the closed pull request as cancelled. Without `--yes` it
  only says what it would do; with `--dependents` it also cancels the unfinished tasks of the slices waiting for the
  slice, directly or through others, and without it lists them and they keep waiting.

The merge side is still to come: a merge found by `ralph pr check` is recorded and noted, and nothing else changes.

## Alternatives rejected

- Acting automatically on a closure: it cannot tell abandonment from accident or supersession, and Article V leaves the
  decision to a person.
- Always starting the next middle unit after a merge: surprises users and spends budget they did not expect.

## Consequences

- Units need a closed state and a needs-attention state; names are open.
- Branch deletion must refuse when the branch holds unmerged work, and must say what it did.
- The monitor and any auto-continue apply these results through the shared change handler, so they behave the same however
  a change was detected.
