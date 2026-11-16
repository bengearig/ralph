# First release

Status: draft

What the first release contains and how it is delivered. Rationale is in [0021](decisions/0021-first-release-scope.md).

## Audience

A solo developer working fully locally. Everything runs on one machine with local storage.

## Included

- The work model, with orphan tasks, and the feature flow: Receive, Articulate, the headless loop, pull requests, review
  feedback as new bottom units, quick work, `ralph monitor`, and the handling after a merge or a closure. See
  [concepts.md](concepts.md), [workflow.md](workflow.md).
- Local storage in SQLite, behind the service interfaces, with migrations, a backup command and run records. See
  [architecture.md](architecture.md) and [local-state.md](local-state.md).
- Layered configuration with the command that shows the effective configuration, and `ralph init`. See
  [configuration.md](configuration.md), [harness-integration.md](harness-integration.md).
- Harness adapters for Claude Code and OpenCode, with commands or skills, MCP, project instruction entries and the
  headless loop in restricted and auto permission modes. See [harness-integration.md](harness-integration.md).
- Documents with kept versions, retention and purging, archive and supersede, one-way export, and keyword and semantic
  search through Ollama, with detection of a stale semantic index and a way to rebuild it. See
  [documents-and-search.md](documents-and-search.md).
- Pull request creation and reading for GitHub and Bitbucket Cloud, behind one provider interface.
- Jira as a task manager and as an issue source. See [task-manager-and-issues.md](task-manager-and-issues.md).

## Not included

- The API server, remote implementations of the services, Postgres, personal API tokens, and multi-tenant mode. See
  [0012](decisions/0012-service-layer-and-per-service-backends.md) and
  [0014](decisions/0014-server-storage-authentication-tenancy.md).
- The hosted commercial service.
- Webhooks, which need a server.
- The portable export of records and documents, and worktrees for the loop.
- Squash and rebase merges. A bottom unit is a commit, and these merges replace those commits on the base branch. The
  first release does not match units to history after such a merge. Doing so is a later feature.
- Stacked branches, the arrangements in which an outside system owns the data or ralph reads through to it, GitHub
  Issues, and credentials for individual users stored on a server.

The service interfaces are built in the first release so that remote implementations can be added without changing the
model. Only the local implementations ship.

## Delivery

The first release is delivered in stages that each leave a working tool, in the way ralph's own middle units each leave
a stable result. The order of the stages is open. "Stage" is used because the work model's names are undecided.

## Open

See [open-questions.md](open-questions.md).
