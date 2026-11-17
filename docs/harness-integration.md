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

Used by the loop: each attempt on a bottom unit is one headless run, and the loop checks the result itself. See
[the loop](workflow.md#loop). The design record is the headless-invocation document in ralph's
store; this section is the settled behavior.

### The process

Each iteration starts the harness's CLI as a fresh process in the project root, with a prompt built for one bottom
unit. The prompt travels on standard input, never in the arguments, so its size is not limited by the command line and
it does not show in a process list. Context is not carried between iterations. The adapter describes the process as data
(the command, arguments, environment additions and standard input), supplies a parser for the harness's output and
declares what the harness can do; an adapter never starts a process itself. Ralph's runner starts it in its own process
group, reads its output a line at a time, and ends the whole group on the time limit or on cancellation.

Ralph checks the result itself after the process exits and does not rely on what the agent reports.

### Permission modes

`headless.permissions` chooses the mode. It is a user-only setting: a value found in the committed project file is
ignored with a warning, so cloning a repository never grants an agent broad power. See
[0017](decisions/0017-integration-files-and-headless-permissions.md).

- **Restricted**, the default, denies everything and then allows what the loop needs.
  - Claude Code runs in its mode that denies whatever is not allowed, without prompting, with allow and deny rules
    built from ralph's allowlist. A deny rule beats an allow rule.
  - OpenCode gets the same rules as permission configuration injected by ralph, with a catch-all deny first so that
    reads and edits outside the project are denied too. Ralph's rules come last, and the last matching rule wins.
- **Auto** hands approval to the harness's own automatic mode. Claude Code runs in its automatic permission mode with
  prompts turned off, so nothing waits for an answer. OpenCode runs with its auto flag, which approves everything not
  explicitly denied. Ralph adds no allowlist, only the protected paths. This is the only meaning "auto mode" has in these
  documents; starting the next middle unit by itself is auto-continue.

A restricted run may:

- edit files inside the project and read;
- call ralph's read-only tools;
- run read-only git: status, diff, log, show, rev-parse and ls-files, each with any arguments;
- run each configured verification command, as a whole and as each simple command inside it, split at `&&`, `||`, `;`,
  `|`, `&` and newlines without regard to quoting;
- run the shell command patterns of `headless.allow`.

Everything else is denied, including committing, pushing and network access. Ralph commits after verification, as quick
work does, so the agent is not given `git add` or `git commit`. A model that is refused a shell command may try another
route, so the allowlist constrains shell commands and not only file edits.

In both modes the agent may not edit `.git`, `.ralph` or the harness's own configuration: `.claude` and `.mcp.json` for
Claude Code, `.opencode`, `opencode.json` and `opencode.jsonc` for OpenCode. An edit of those would let a run widen itself
or damage ralph's state.

### Settings that are not loaded

Committed harness settings are not loaded in either mode, because a clone could otherwise widen an unattended agent or
run its hooks.

- Claude Code runs with only its user and local setting sources and only ralph's MCP server. A hook or allow rule
  committed to the project neither runs nor widens the agent.
- OpenCode runs with its project configuration left out, which skips the project's `opencode.json` and `.opencode`
  directory and so the project's committed plugins and commands, with ralph's MCP server and rules injected, and with a
  private server so the injected rules apply to the process that evaluates them. Its instruction file (`AGENTS.md`) is
  still read.

### Widening a restricted run

`headless.allow` is a user-only list of shell command patterns, accepted from the user file, the project-local file, the
environment and flags, and never from a committed file. A pattern uses `*` for any text and a backslash to take the next
character literally; a trailing space and `*` also matches the command with no arguments. Both harnesses read a pattern
the same way, so it is written once, and it works for both.

Claude Code also applies the user's own settings, so an allow rule there widens a restricted run too. OpenCode has no
such layer: ralph's rules come last and win, and the user's own OpenCode permission rules cannot widen a restricted run,
so `headless.allow` is the only way for OpenCode. Setting `headless.permissions` to `auto` is the other route.

### Denials

The exit status says nothing about denials: both harnesses exit successfully after denying an action. Ralph reads them
from the output instead.

- Claude Code writes an event for each denied tool as it happens and lists them in the final result.
- OpenCode writes a tool event whose state is an error beginning with a permission denial.

Each denial records the tool, the command or path, and the reason. A run with a denial fails, even when the process
exited with success. The report suggests the exact line to add: for a shell denial, the command as a `headless.allow`
pattern (ending it in a space and `*` allows any arguments); for another tool, that it is outside the restricted
allowlist and that `headless.permissions` can be `auto`.

### Cost, tokens and the budget

Ralph counts cost and tokens from the harness's output for every run and records them.

- Claude Code reports cost and usage once, in its final result; per-message usage during the run is partial, so only
  the final figures are used. Ralph passes the cap to Claude Code, which stops itself, and also checks the cost the final
  result reports.
- OpenCode has no cost flag but reports cost and tokens in every finished step. Ralph adds the cost of each finished
  step and stops the process when the sum passes the cap. The step that crosses the cap is already paid for, so the
  recorded cost can be above the cap.
- When a cap is set and the harness reports no cost, as for a model without a price, the run is not stopped and the
  result carries a note that the budget was not checked. The note belongs in the run record.
- Tokens never stop a run.

A run stopped by the cap, by either path, fails.

### Time limit

`headless.timeout` limits the wall-clock time of one run. On expiry ralph ends the process group, waits a grace period
and then kills it. A run that timed out fails.

### The run result

A run returns the exit code, its duration, whether it timed out or was canceled, the last of its standard error, the
parsed outcome (the final answer, the session, any harness error, the denials and the usage) and any notes. An error is
returned only when the process cannot be started or its output cannot be read. A failure line says why in one line: timed
out, canceled, budget exceeded, a harness error, a non-zero exit, or denied actions with the first few listed. An empty
line means a clean run. A failed iteration, whether from this or from failed verification, counts for the Stall Statute.

### Settings

| Setting | Scope | Default | Meaning |
| --- | --- | --- | --- |
| `headless.permissions` | user-only | `restricted` | `restricted` or `auto`: the permission mode of a headless run. |
| `headless.allow` | user-only | none | Shell command patterns a restricted run may also run. |
| `headless.budget_usd` | project | `0` | Cost cap of one run in US dollars; `0` means no cap. |
| `headless.timeout` | project | `30m` | Wall-clock limit of one run, a duration greater than zero. |

### Live checks

Each harness has a check that runs it for real, skipped by default. They read and change only a temporary project that
the test creates.

- `RALPH_CLAUDE_LIVE=1` runs the Claude Code check. It spends a small amount: `RALPH_CLAUDE_LIVE_BUDGET_USD` sets the
  cap (default 0.50) and `RALPH_CLAUDE_LIVE_MODEL` the model (default `haiku`).
- `RALPH_OPENCODE_LIVE=1` runs the OpenCode check with a private home. `RALPH_OPENCODE_LIVE_MODEL` sets the model; the
  default is a free one.

Both need the harness on the path. They exist to find drift between ralph's adapter and the real harness.

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
