# 0022: Embedding index repair

Status: draft

## Context

Semantic search stores vectors made by one embedding configuration. Changing the model, the prefix settings or the
chunking settings leaves vectors that are not comparable with new ones. Searching them returns plausible but wrong
results with no error.

## Decision

- The index records the configuration that produced it and ralph checks it against the current one. A mismatch is a
  stale index.
- A stale index turns semantic search off, and search falls back to keyword search with a visible notice.
- A command rebuilds the index from current records. It is resumable, and the old index stays in use until the new one
  is complete.
- Ralph does not rebuild without the user's confirmation.
- The notice reaches the user through ralph's own commands, project status and `ralph monitor`. In a harness session it
  is carried by the MCP server and the project instruction entries, which tell the agent to relay it and ask. No
  harness hook is required.
- An unreachable provider is not a stale index and triggers no rebuild.

The fallback with a notice when the provider fails is in place; recording and checking the configuration, and the
rebuild, are not yet.

## Alternatives rejected

- Searching a mismatched index anyway: wrong results that look right.
- Rebuilding automatically on a change: a surprise cost in time, and in money with a hosted provider.
- A harness startup hook to nudge the user: ties the behavior to harnesses that have hooks, against
  [0007](0007-orphan-task-delivery.md) and [0016](0016-harness-integration.md).
- Deleting the old index before rebuilding: search is down for the whole rebuild and an interrupted rebuild leaves
  nothing.

## Consequences

- The index needs a place to store its configuration, and a comparison that does not fire on harmless changes such as a
  timeout.
- A rebuild keeps two indexes at once for a while, so it needs room for both.
- Whether a provider reports a model version, so that a silently updated model under the same name is noticed, depends on
  the provider. Where it does not, only a manual rebuild repairs it.
- Documents and work records are the source and the index is derived, so a rebuild can always be done and loses nothing.
