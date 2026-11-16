# 0007: Orphan task delivery

Status: draft (details settled for the first release)

## Context

Quick work such as a bugfix does not warrant a breakdown into slices, but still needs review. The method requires work
to arrive as a reviewable change. Small work must stay lighter than feature work, or users will bypass ralph.

## Decision

- An orphan task gets its own branch and pull request by default. This is implemented so the behavior can be made
  configurable later.
- It is received through a dedicated entry point, `quick` (`/ralph:quick`), that takes either an instruction or a
  reference to an existing issue or ticket. See [0020](0020-task-manager-and-issue-source.md).
- The interview scales with ambiguity: questions are asked only where the instruction is ambiguous (Article I). An
  unambiguous instruction, such as changing a color to a given value, needs none. No slices are produced. The agent in
  the session judges ambiguity from the instruction's wording; there is no numeric threshold.
- When the instruction is significantly larger than quick work, the entry point suggests the feature Receive flow
  instead. The agent judges "too big for quick work" in the same way, from the wording and with no numeric threshold.
  The user decides; ralph does not switch flows on its own.
- The harness session the user is already in implements the task. The loop is not required.
- The flow is session-driven. Ralph supplies the task, its acceptance criteria and its verification commands, and the
  harness runs them. A ralph command (CLI and MCP tool) finalizes the work: it checks the result, commits, and opens
  the pull request. Ralph does not depend on harness hooks, so the flow works in any harness.
- The entry point exists both as a harness slash command and as an equivalent CLI command (`ralph quick start` and
  `ralph quick finish`). The finalizing actions are also MCP tools (`quick_start` and `quick_finish`). All of them call
  the same service, so harnesses without slash commands and scripts are covered.
- After implementation, the branch, commit and pull request follow, and a human reviews as for any other work.
- The steps are described in [workflow.md](../workflow.md#quick-work).

## Alternatives rejected

- Always running through the loop: two steps for a quick fix.
- A fixed-depth interview for every task: heavy for trivial changes.
- No interview at all: risks acting on a misreading.
- Slash command only or CLI only: couples to harness features, or forces leaving the session.
- Hook-enforced finalization: ties the flow to harnesses that support hooks.

## Consequences

- Delivery of orphan work is represented in git the same way as other work.
- The delivery behavior sits behind a single seam so alternatives can be added without changing the work model.
- Finalization is explicit. If the session never calls it, the work stays unfinalized, so ralph must make an
  unfinalized task visible. An unfinalized task is listed by `ralph status` and reported as a notice to the agent.
- Finalization must handle a result that fails its checks, and must not commit or open a pull request in that case. A
  failed check leaves the task unfinalized, so it is surfaced the same way.
