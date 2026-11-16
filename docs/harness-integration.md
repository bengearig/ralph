# Harness integration

Status: draft

How ralph works inside the harness a developer already uses, without replacing it. Rationale is in
[0016](decisions/0016-harness-integration.md). The principles are in [overview.md](overview.md).

## Adapters

A harness adapter is the part of ralph that knows one harness. Everything else in ralph is harness-neutral. The first
release ships adapters for Claude Code and OpenCode, so the adapter interface is shaped by two harnesses from the start.

An adapter provides:
- The interactive surfaces below, rendered in the harness's native format.
- Headless invocation for the loop.

## Interactive surfaces

Used for the phases where the user works with the agent: Receive, Articulate, and quick work.

- Commands or skills native to the harness carry the workflow instructions inside the user's session. The quick-work
  entry point of [0007](decisions/0007-orphan-task-delivery.md) is one of them.
- An MCP server exposes ralph's service interfaces as tools, so the agent reads and writes ralph's records through
  ralph and never touches storage directly. In remote mode the MCP server is a client of the instance.
- Entries in the harness's project instruction file make every session aware of the workflow.

The MCP server covers only what agents need. The CLI covers what people run, such as installing integration files and
monitoring. Actions an agent needs to finish its own work, such as finalizing quick work, are MCP tools, and
[0007](decisions/0007-orphan-task-delivery.md) also names a CLI form for it. Which actions, if any, appear in both is
open. See [0016](decisions/0016-harness-integration.md).

## Headless invocation

Used by the loop. See [workflow.md](workflow.md).

- Each iteration spawns the harness's CLI as a fresh process with a prompt built for one bottom unit. Context is not
  carried between iterations.
- The adapter defines the command, the permission mode, the budget cap and the handling of output.
- Permissions default to restricted, a scoped allowlist. An auto mode defers to the harness's own automatic mode and
  is never accepted from a committed project file. See [0017](decisions/0017-integration-files-and-headless-permissions.md).
- Ralph checks the result itself after the process exits and does not rely on what the agent reports. A harness can
  exit successfully after denying an action, so the adapter reads denied actions from the harness's output, not from
  the exit code.
- A session that hits the budget cap, or fails verification, counts as a failed iteration for the Stall Statute.

## Installation

`ralph init` installs a harness's integration files and updates them when ralph changes. A file the user edited is never
overwritten, and in a file the user owns ralph manages only a marked section. Updates are explicit by default. See
[0017](decisions/0017-integration-files-and-headless-permissions.md).

## Workflow instructions

Ralph ships a canonical version of the instructions for each phase and renders them into each harness's native format.
Users and projects can override parts by configuration. What can be overridden, and how, is open.

## Open

See [open-questions.md](open-questions.md).
