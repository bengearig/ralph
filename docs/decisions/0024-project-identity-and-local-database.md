# 0024: Project identity, local database, upgrades and backup

Status: draft

## Context

The local database is gitignored, so a git worktree would not contain it and ralph would see an empty project. Several
processes use the database at once. Ralph versions will change its structure.

## Decision

- The project root is resolved from the repository's primary worktree through git's common directory. All worktrees of
  one repository share one ralph directory.
- `ralph init` generates a project identifier and writes it to the committed project file.
- The database lives in the ralph directory, is excluded from version control, and uses write-ahead logging.
- The database records its schema version. Forward-only migrations run at startup, preceded by an automatic backup
  copy. A newer database than ralph understands is refused, not opened. A setting can require an explicit migration
  command.
- A command makes a consistent snapshot, safe while the loop runs.
- The portable export is deferred to the server release.

## Alternatives rejected

- A database in each worktree: an empty project in every new worktree.
- Explicit migration only: a step users forget, contrary to not disrupting their workflow.
- Opening a newer database read-only or downgrading it: silent damage or silent data loss.
- Portable export in the first release: no use for it until a server exists.

## Consequences

- Every path to project state goes through one resolution of the project root, which is the unit to specify and test.
- Backups accumulate, so how many are kept is open.
- Each clone has its own database in the first release, so work records are not shared between clones or machines.
