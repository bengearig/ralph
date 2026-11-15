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
- The next middle unit waits for the user to start the loop, unless an auto mode is active. Auto mode starts the next
  middle unit by itself once the previous one merges. Its definition is open.

After a closure without merging:
- Ralph records the closure and stops. The branch and commits are left untouched.
- The middle unit is marked as needing attention. Dependent middle units keep waiting, and the monitor and status show
  why.
- The user chooses one of: reopen with a new pull request from the same branch, re-articulate the unit (the Kickback
  Clause), or cancel the unit and anything depending on it.

## Alternatives rejected

- Acting automatically on a closure: it cannot tell abandonment from accident or supersession, and Article V leaves the
  decision to a person.
- Always starting the next middle unit after a merge: surprises users and spends budget they did not expect.

## Consequences

- Units need a closed state and a needs-attention state; names are open.
- Branch deletion must refuse when the branch holds unmerged work, and must say what it did.
- The monitor and any auto mode apply these results through the shared change handler, so they behave the same however
  a change was detected.
