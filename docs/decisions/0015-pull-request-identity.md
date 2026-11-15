# 0015: Pull request identity

Status: draft

## Context

In a shared instance, ralph must know whose work a pull request is, while the hosting provider sees only the account
whose token created it. See [0012](0012-service-layer-and-per-service-backends.md).

## Decision

- Ralph always records who did the work, as the user the API token resolves to. This is independent of the identity the
  hosting provider sees.
- Two arrangements are supported:
  - Shared service token: pull request handling is remote, and the server holds one provider token. Every pull request
    is created under that account. The ralph user is recorded on the records and credited in the pull request
    description.
  - Individual token: pull request handling is local, and each developer's own provider token stays on their machine.
    Pull requests are created under their own identity.
- Storing each user's provider token on the server, so pull requests go out as the right person from a shared server,
  is deferred as a possible enterprise option.

## Alternatives rejected

- Per-user tokens stored on the server in the first release: the server would hold many users' credentials, which
  needs encryption, rotation and a security review.

## Consequences

- Teams that want per-person pull request identity through a shared server wait for the deferred option or use local
  pull request handling.
- Pull request descriptions carry the credit when the shared token is used. Their format is open.
