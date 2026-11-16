# 0002: Design principles

Status: settled

## Context

The PoC was built around one author's workplace workflow and was too specific. The new product must fit developers from
hobbyists to enterprise employees, and wraps harnesses people already use.

## Decision

The principles in [overview.md](../overview.md) govern all later decisions. When they conflict, the more general
principle wins: not disrupting the user's workflow outranks not reinventing the wheel, which outranks minimalism, which
bounds configurability. Configurability is expected to grow substantially over time as features are added, within
that bound.

## Alternatives rejected

- Specific built-in workflow with limited options: reproduces the PoC's flaw.
- Maximal configurability from the start: conflicts with minimalism and quality. Configurability grows over time.

## Consequences

- Features are weighed against existing tools before being built.
- Configuration surface is added deliberately, not speculatively.
