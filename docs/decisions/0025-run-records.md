# 0025: Run records

Status: draft

## Context

A headless iteration can fail or stall, and the Stall Statute requires the loop to stop and ask for help. The user needs
evidence to act on. Under the restricted permission mode of
[0017](0017-integration-files-and-headless-permissions.md), a denied action is how the user learns what to allow.

## Decision

- The loop keeps a run record for each iteration: the prompt, the harness's output, the output of verification, the
  actions denied by the permission mode, and the cost and duration.
- Run records are stored locally in a gitignored directory in the ralph directory.
- A short summary of each iteration is attached to its unit, so status and `ralph monitor` can show it.
- How long run records are kept is configurable.

## Alternatives rejected

- Summaries only: not enough to diagnose a failure.
- Storing full records in the database: large, and rarely read.

## Consequences

- Run records may contain code and command output, so they are local and never sent to a server.
- The prompt and output can include secrets that appeared in the project. Their handling is open.
- Retention needs a default and a way to purge.
