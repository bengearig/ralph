# 0017: Integration files and headless permissions

Status: draft

## Context

Ralph writes files into a project so a harness can use it, and users edit files. The loop runs a harness with no human
present to approve actions. See [0016](0016-harness-integration.md).

## Decision

Integration files:
- `ralph init` installs them and records a content hash of each file it writes.
- A file that still matches its recorded hash is replaced silently when updated.
- A file that differs is never overwritten. The new version is written next to it, the difference is reported, and the
  user can mark the file as customized so ralph stops reporting it.
- In a file the user owns, such as a project instruction file, ralph manages only a clearly marked section and never
  touches the rest.
- Updates are explicit by default: ralph warns when its files are outdated and `ralph init` applies the update.
  Automatic update of untouched files is a configuration option.

Headless permissions:
- The default is restricted: a scoped allowlist per adapter that permits editing files in the repository, the git
  operations the loop needs, and the project's verification commands, and denies everything else.
- A denial fails the iteration visibly, with the denied action reported, so the user can widen the allowlist
  deliberately. The harness's own settings still apply on top.
- A second mode, auto, hands approval to the harness's own automatic mode instead of a ralph-defined level. Each adapter
  maps it to what its harness provides.
- Restricted is the only mode a committed project file can request. Auto comes only from user-level configuration, an
  environment variable, or a run flag, so cloning a repository never grants an agent broad power.
- The names are open. "Auto" is already used for starting the next middle unit in
  [0011](0011-after-merge-and-closed-pull-requests.md).

## Alternatives rejected

- Three-way merging of edited files: heavy and error-prone for the first release.
- Updating files automatically by default: changes files the user may rely on without telling them.
- A ralph-defined permission level broader than restricted: ralph would own what counts as safe.
- Allowing auto from project config: a committed file could grant power to everyone who runs the loop.

## Consequences

- Ralph keeps a record of what it wrote, and `ralph init` must cope with a missing or damaged record.
- Each adapter defines an allowlist and an auto mapping, and a harness without an automatic mode supports restricted
  only.
- A verification command not in the allowlist fails every iteration, so the allowlist must be derived from the
  configured verification commands.
