# 0023: Loop workspace

Status: draft

## Context

The loop runs headless and changes files and branches. It must not collide with the user's own work in the checkout, but
isolation has a cost. A fresh git worktree lacks the project's untracked files, such as installed dependencies, build
caches and local environment files, so verification often fails there until a setup step runs.

## Decision

- The loop works in the user's checkout, on the middle unit's branch.
- The loop refuses to start when the working tree has uncommitted changes, and says why.
- A user who wants to keep working while the loop runs uses a second clone.
- Worktrees are a later, configurable option, behind a single seam in the workspace handling so that adding them does
  not change the work model.

## Alternatives rejected

- A worktree per middle unit now: needs a per-project setup step, creation and cleanup, and failure handling.
- Running the loop over uncommitted changes: the loop's commits would mix with the user's work.

## Consequences

- The checkout is occupied while the loop runs.
- Quick work, which runs in the user's own session, branches from the same checkout, so the clean-tree rule also
  applies when it starts.
- Moving to worktrees later must not change where the database and configuration are found. See
  [0024](0024-project-identity-and-local-database.md).
