# 0016: Harness integration

Status: draft

## Context

Ralph never replaces the harness and must not disrupt the user's workflow. It has to work inside whichever harness a
developer already uses, and run headlessly for the loop.

## Decision

- A harness adapter encapsulates everything specific to one harness. The first release ships Claude Code and OpenCode.
- Interactive phases use harness-native commands or skills, an MCP server, and entries in the harness's project
  instruction file.
- The MCP server covers only what agents need, and the CLI covers what people run. There is no goal of parity between
  them.
- The loop spawns the harness's CLI per iteration as a fresh process for one bottom unit, then checks the result itself.
- Ralph ships canonical workflow instructions, renders them per harness, and lets users override them with
  files in `.ralph/workflow/`.

## Alternatives rejected

- Claude Code only first: it would not test whether the adapter interface is harness-neutral.
- A harness SDK or API for the loop: couples ralph to each vendor, and not every harness has one.
- A long-lived harness process: context accumulates and failures are harder to isolate.
- Instructions that cannot be overridden: risks breaking early adopters' workflows.
- Instructions the user must supply: every user starts from nothing.

## Consequences

- The adapter interface must work for both harnesses before it is accepted.
- A harness that lacks one surface, such as commands, must still work through the others.
- The loop depends on each harness's CLI having a non-interactive mode, a permission control and ideally a budget
  control. A harness without them can be integrated interactively only.
