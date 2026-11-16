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
[0007](decisions/0007-orphan-task-delivery.md) also names a CLI form for it. Quick start and finish are both a command
and a tool; other actions are decided as they are added. See [0016](decisions/0016-harness-integration.md).

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

### OpenCode

The OpenCode adapter writes these files in the project:

- Commands as `.opencode/commands/ralph/<name>.md`, run as `/ralph/<name>`. Claude Code's are `/ralph:<name>`.
- Ralph's marked section of project notes in the project instruction file. That is `AGENTS.md` if it exists, else
  `CLAUDE.md` if it exists, else a new `AGENTS.md`. OpenCode reads `CLAUDE.md` only when there is no `AGENTS.md`, so
  creating an `AGENTS.md` would hide a user's `CLAUDE.md`.
- The MCP server as `mcp.servers.ralph` in the first of `opencode.json`, `opencode.jsonc`, `.opencode/opencode.json` and
  `.opencode/opencode.jsonc` that exists, or a new `opencode.json` in the project root. The entry is a local server
  whose command is `ralph mcp`.

A file ending in `jsonc` is never edited, because rewriting it would drop the user's comments. If the entry is already
there, ralph adopts it. Otherwise `ralph init` reports the file as kept and prints the snippet to add. Other `json`
files keep their other keys and numbers.

No permission entry is written. OpenCode does not need one to call ralph's tools, and a committed file never grants
permission.

Detection looks for the `opencode` command and reads its version. A different, older project is also called OpenCode;
it does not list a `--format` option for `opencode run`, so ralph treats it as unsupported, as it does a major version
before the one ralph supports. `ralph init` prints the reason and skips an unsupported harness that it detected, and
`ralph init --harness opencode` fails before writing anything.

The project notes name tools by their bare names and say how commands are written in each harness, so they are the
same for every harness. Two harnesses that read one instruction file, such as Claude Code and OpenCode with a
`CLAUDE.md`, share one section, and the second application is reported as unchanged.

## Workflow instructions

Ralph ships a canonical version of the instructions for each phase and renders them into each harness's native format.
Where a harness needs different wording for the same step, such as how the user approves an outline, the instruction
asks for harness-specific text by key and each adapter supplies its own; a harness without text for a key gets neutral
wording.

A project can override an instruction with files in `.ralph/workflow/`. `<name>.md` replaces the instruction called
`<name>`, and `<name>.append.md` adds its text at the end of it. The files are project-level so that they are committed
and shared by everyone working in the repository, and the integration files rendered from them stay the same for all.
`ralph init` applies the overrides when it renders the instructions and reports each one with a `workflow:` line, and
`ralph status` reports the integration files as out of date when an override has changed since they were written. A file
named for an instruction that does not exist makes `ralph init` fail, naming the file, before it writes anything.

## Open

See [open-questions.md](open-questions.md).
