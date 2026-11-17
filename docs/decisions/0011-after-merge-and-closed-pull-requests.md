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
- The next middle unit waits for the user to start the loop, unless auto-continue is on. Auto-continue starts the next
  middle unit by itself once the previous one merges, and is the `continue` kind of follow-up, off by default.

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

A merge is recorded the same way, by `ralph pr check`, `ralph apply` or `ralph monitor --watch --apply`: the record
becomes merged and the slice gets a note. The slice's status is `IN_REVIEW` while its latest pull request is open and
`MERGED` once it is merged, and a brief whose slices are all merged is `DONE`. A slice that waits for a merged slice
stops waiting. Recording changes nothing else; what follows a merge is applied by `ralph apply` (see
[0010](0010-sequencing-and-merge-detection.md)):
- Close (`--close`) finishes a merged slice. It deletes the local branch, and the remote branch as well when
  `follow_up.delete_branches` is `both`; the setting is `local` by default and `none` deletes nothing. It then
  fast-forwards the base branch from the remote when the checkout is on it with no changes. What it did and what it
  refused is reported and noted on the slice.
- A branch is deleted only when its tip is the head commit of the merged pull request, the commit the host reports the
  pull request's head branch had. A squash or rebase merge leaves the branch's commits outside the base, so ancestry
  would call a merged branch unmerged; the head commit does not. A branch with later commits is kept, the checked out
  branch is kept with `git switch` named as the way out, and nothing is deleted when the host does not report a head
  commit.
- Continue (`--continue`) is auto-continue as an opt-in follow-up kind. It runs the loop for the next slice of the
  brief, at most one per run and never one started this way before, and only from a clean checkout on the base branch
  that is up to date with the remote. Nothing starts it unless a flag or the user-only `follow_up.auto` names it, because
  the loop spends money. A refusal says why and starts nothing.
- Feedback (`--feedback`) creates the tasks of review feedback on an open pull request. A closure without merging is
  never applied: `ralph apply` records it and reports that a person must choose.

## Alternatives rejected

- Acting automatically on a closure: it cannot tell abandonment from accident or supersession, and Article V leaves the
  decision to a person.
- Always starting the next middle unit after a merge: surprises users and spends budget they did not expect.

## Consequences

- Slices need a state for review and one for merged, and a needs-attention state; the names are settled: `IN_REVIEW`,
  `MERGED` and `NEEDS_ATTENTION`.
- Branch deletion refuses when the branch holds work the merged pull request did not have, and says what it did.
- The monitor and any auto-continue apply these results through the shared change handler, so they behave the same however
  a change was detected.
