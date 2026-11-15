# Overview

Status: draft

## Purpose

ralph is a developer-side tool that wraps a harness (Claude, OpenCode, Codex and others) and keeps the developer's
workflow consistent, predictable and familiar, following [the Manifesto](../MANIFESTO.md).

The CLI includes a built-in API web server. A person or company can run it to centralize ralph's functionality, so
developers are not isolated from one another at the storage point. The same API is also offered as a hosted commercial
service for a monthly fee, with enterprise options possible.

Backend services are configurable to run locally or against a hosted API. This lets ralph serve hobbyists and
employees of large enterprises alike.

## Principles

Listed from the most general to the more specific. See [0002](decisions/0002-design-principles.md) for rationale.

1. Avoid disrupting the user's existing workflow. Adopting ralph should be a near seamless transition.
2. Avoid reinventing the wheel when the existing one is perfectly fine.
3. Be highly configurable, because principle 1 requires fitting many workflows.
4. Stay minimalist and favor quality over quantity. Some configurability arrives later so this stays true.
5. Do not be specific to any one employer's or person's workflow.

## Goals

- The CLI works fully with local services and no network dependency beyond the harness.
- The same CLI works against a shared API, self-hosted or hosted, selected by configuration.
- Work that is small, such as a quick bugfix, does not require ceremony designed for large work. How the work model
  achieves this is open.

## Non-goals

- ralph never replaces the harness.
- Ralph does not assume a particular company's process.

## Open

- The work model is drafted in [concepts.md](concepts.md) and [0004](decisions/0004-work-model.md); its names are
  undecided. See [open-questions.md](open-questions.md).
- Which functionality belongs to the API server versus the local CLI.
- Monetization specifics are deliberately out of scope until the author raises them.
