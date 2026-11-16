# 0021: First release scope

Status: draft

## Context

The decisions so far describe far more than a minimal first release. The principles favor quality over quantity, and
the work model asks for each delivered slice to leave something stable and usable.

## Decision

- The first release serves a solo developer working fully locally. The API server, remote service implementations,
  Postgres, tokens and tenancy come later.
- Every other decided feature stays in the first release, including both harnesses, semantic search, version retention
  and purging, and Jira in both roles.
- Pull request providers for the first release are GitHub and Bitbucket Cloud, behind one provider interface.
- The first release is delivered in stages that each leave a working tool.
- Squash and rebase merges are not supported in the first release. Matching units to history after one is a later
  feature.

## Alternatives rejected

- Including the API server: the largest single addition, and the method can be proven without it.
- Deferring semantic search, Jira, the second harness or version tooling: the author keeps them in.
- One release when everything is in: nothing is usable until the end.
- Calling the stages "milestones": the term belongs to the PoC's work hierarchy, and this project's names are undecided.

## Consequences

- The service interfaces are specified and built so remote implementations are an addition later, not a rewrite.
- The fingerprint check of [0006](0006-shared-state-and-concurrency.md) is still needed locally, because the loop and an
  interactive session can write the same database at once.
- Two pull request providers force the provider interface to be provider-neutral from the start, as the two harnesses
  do for the adapter interface.
- This scope is still large, and the order of the stages decides whether an early stage is useful on its own.
