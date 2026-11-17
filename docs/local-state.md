# Local state

Status: draft

Where ralph keeps its data on a developer's machine, how that data is upgraded and backed up, and what ralph records
about loop runs. Rationale is in [0023](decisions/0023-loop-workspace.md),
[0024](decisions/0024-project-identity-and-local-database.md) and [0025](decisions/0025-run-records.md).

## Project root

A project is a git repository. Ralph resolves the project root from the repository's primary worktree, using git's
common directory, not from the directory it was started in. Every worktree and subdirectory of one repository therefore
reads the same ralph directory.

The ralph directory at the project root holds the database, the project file, the project-local file and run records.
See [configuration.md](configuration.md).

## Project identifier

`ralph init` generates a project identifier and writes it to the committed project file. Clones of the repository read
the same identifier. In the first release each clone has its own database, so the identifier does not yet join them. A
remote instance uses it later to tell projects apart.

## Database

The local database is SQLite, in the ralph directory, and is excluded from version control. Several processes can use it
at once, such as the loop and an interactive session, so it uses write-ahead logging and writes carry a fingerprint, as
in [0006](decisions/0006-shared-state-and-concurrency.md).

## Upgrades

- The database stores its schema version.
- Forward-only migrations ship in the binary and run at startup, so an upgrade does not need a step from the user.
- Before any migration, ralph copies the database as a backup.
- A database newer than the running ralph understands is refused with a message that says so. It is never opened or
  downgraded.
- A setting can require the user to run the migration explicitly instead.

## Backup

A command makes a consistent snapshot of the database. It is safe to run while the loop is running. The portable export,
a versioned and lossless export of records and documents that can be imported elsewhere, is designed with the server
release and is not in the first release.

## Loop workspace

The loop works in the user's checkout and does not create worktrees. Before it starts, the working tree must have no
uncommitted changes, or the loop refuses to start and says why. A user who wants to keep working while the loop runs can
run it from a second clone. Worktrees are a later option behind the same seam.

## Run records

The loop keeps a record of each iteration, stored in a gitignored directory in the ralph directory, `.ralph/runs/`.
It is local and is never committed or sent to a server. Each attempt has a directory named for its start time, attempt
and unit under the directory of its middle unit, with these files:
- `prompt.md`: the prompt sent to the harness.
- `output.jsonl`: the harness's output.
- `verify.txt`: the output of verification, left out when the attempt failed before verification.
- `record.json`: the outcome, failure, commit, cost and duration, and the actions that were denied by the permission
  mode with the line to add to `headless.allow`, so the user can see what to allow.

A short summary of each attempt is attached to its unit as a note, so `ralph monitor` and status can show it. It says
how the attempt ended, names the record and carries the denial suggestions.

Values of the secret settings are replaced by `[redacted]` in the records and the notes, including a value split
across the harness's output. Redaction knows only those values, and ignores any shorter than eight characters; a secret
that came from elsewhere, such as a file the agent printed, is not found. Treat a record as sensitive.

Records older than `runs.keep_days` days (default 30; 0 keeps them) are removed when a loop starts. `ralph runs purge`
removes them by hand: give `--older-than DURATION`, `--slice <ID>` or both, and it lists what it would remove unless
`--yes` is given. Only directories holding a readable `record.json` are removed.

## Open

See [open-questions.md](open-questions.md).
