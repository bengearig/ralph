# ralph

ralph is a developer-side tool that wraps a coding harness and keeps development to a plan. It follows the
[Manifesto](MANIFESTO.md).

The project is under construction. The design is in [docs/](docs/README.md).

## Usage

- `ralph init` sets ralph up in the current git repository.
- `ralph init --harness <name>` (repeatable) also installs ralph's integration for that agent harness, such as
  `claude-code` or `opencode`; without the flag it installs for each harness it detects, so it also installs the
  OpenCode integration when OpenCode is detected. When ralph's integration files are out of
  date, `ralph status` and the MCP server say so; run `ralph init` to update them.
- In an agent session, `/ralph:receive <instructions>` captures and plans work, and `/ralph:articulate <ID>` works out
  its tasks with you; OpenCode runs them as `/ralph/receive` and `/ralph/articulate`. A project can replace or extend any
  instruction with files in `.ralph/workflow/`, which `ralph init` applies.
- `ralph status` shows briefs, slices and tasks with their statuses.
- `ralph quick start <title>` creates a task and its own branch for a small piece of work; `ralph quick finish` runs the
  commands in the `verify.commands` setting, then commits, pushes and opens a draft pull request. In an agent session,
  `/ralph:quick <instruction>` does both. Nothing is merged.
- Pull requests go to GitHub or Bitbucket Cloud, chosen from the remote (`pr.provider`, see
  [docs/pull-request-providers.md](docs/pull-request-providers.md)). For Bitbucket, set `bitbucket.token` to a reference
  such as `env:BITBUCKET_TOKEN` and, for an Atlassian API token, `bitbucket.user` to the account email; without a user
  the token is sent as an access token. `bitbucket.workspace` and `bitbucket.repo` override what the remote says.
- `ralph docs purge` removes earlier document versions: give `--keep N`, `--older-than DURATION` or both, and it lists
  what it would remove unless `--yes` is given. The `documents.versions.keep` setting limits how many earlier versions
  of each document are kept; the default keeps all.
- `ralph docs export` writes each active document to a generated markdown file in the directory of the
  `documents.export.dir` setting (default `docs/ralph`), removes generated files that are stale and never touches other
  files; `--check` writes nothing and exits 1 if the files are out of step. Nothing is committed.
- `ralph docs link <document> <unit>` links an active document to a brief, slice or task, `ralph docs unlink` removes
  the link, and `ralph docs links <id>` shows a document's units or a unit's documents. A tie to a brief is not a link.
- `ralph search <words>` finds documents, briefs, slices, tasks and notes by their words; `--kind` and `--limit` narrow
  the results and `--all` includes archived and superseded documents and cancelled tasks.
- `ralph mcp` serves ralph's records to an agent over the Model Context Protocol on standard input and output.
- Headless runs, which the loop uses, are set by `headless.permissions` (user-only, `restricted` by default or `auto`),
  `headless.allow` (user-only shell command patterns that widen a restricted run), `headless.budget_usd` (a cost cap per
  run) and `headless.timeout`. A restricted run cannot be widened from a committed file. See
  [docs/harness-integration.md](docs/harness-integration.md).

## Development

### Prerequisites

- git
- Go, at the version named in `go.mod`

### Commands

- `make build` builds the `ralph` binary into `bin/`.
- `make test` runs the tests.
- `make vet` runs `go vet`.
- `make lint` runs the linter.
- `make check` runs vet, lint, then test.

`make lint` runs golangci-lint at the version pinned in the `Makefile` and downloads it on first use.

### Live OpenCode test

`TestLiveOpenCode` in `internal/harness/opencode` checks that the real OpenCode reads the files ralph writes. It is
skipped unless `RALPH_OPENCODE_LIVE=1` and `opencode` is on the path. It uses a temporary home, starts OpenCode with
`--standalone` and never runs a model. For example:

```sh
RALPH_OPENCODE_LIVE=1 go test ./internal/harness/opencode -run TestLiveOpenCode -v
```

### Live headless tests

The headless runs are checked against the real harnesses by tests skipped unless `RALPH_CLAUDE_LIVE=1` (Claude Code,
which spends a small amount) or `RALPH_OPENCODE_LIVE=1` (OpenCode, with a free model by default) is set. They use a
temporary project. See [docs/harness-integration.md](docs/harness-integration.md).

### Live GitHub test

`TestLiveGitHub` in `internal/hosting/github` checks the GitHub provider against real GitHub. It is skipped unless `RALPH_GITHUB_LIVE=1`, and it only reads. Set `RALPH_GITHUB_LIVE_REPO` to `owner/repo`, `RALPH_GITHUB_LIVE_BRANCH` to a branch to look up, and put a token in the variable named by `RALPH_GITHUB_LIVE_TOKEN_ENV` (default `GITHUB_TOKEN`). For example:

```sh
RALPH_GITHUB_LIVE=1 RALPH_GITHUB_LIVE_REPO=owner/repo RALPH_GITHUB_LIVE_BRANCH=main \
  go test ./internal/hosting/github -run TestLiveGitHub -v
```

If a pull request exists for the branch, the test also reads that pull request and its review feedback.

### Live Bitbucket test

`TestLiveBitbucket` in `internal/hosting/bitbucket` checks the Bitbucket Cloud provider against the real service. It is
skipped unless `RALPH_BITBUCKET_LIVE=1`, and it only reads. Set `RALPH_BITBUCKET_LIVE_REPO` to `workspace/repo` and put a
token in the variable named by `RALPH_BITBUCKET_LIVE_TOKEN_ENV` (default `BITBUCKET_TOKEN`). Set
`RALPH_BITBUCKET_LIVE_USER` to the account email when the token is an API token, which sends it with Basic
authentication; leave it unset for a repository, project or workspace access token. `RALPH_BITBUCKET_LIVE_BRANCH` names
the branch to look up and defaults to `main`. For example:

```sh
RALPH_BITBUCKET_LIVE=1 RALPH_BITBUCKET_LIVE_REPO=workspace/repo RALPH_BITBUCKET_LIVE_BRANCH=main \
  go test ./internal/hosting/bitbucket -run TestLiveBitbucket -v
```

If a pull request exists for the branch, the test also reads that pull request and its review feedback.

The binary is built without cgo, so it stays static.

The older tool's files in this repository are never overwritten: `ralph init` keeps any file it did not write and
puts the new version beside it.

### Repository layout

- `cmd/` holds the program entry points.
- `internal/` holds the packages the program is built from, including the command-line interface and shared test
  helpers.
- `internal/units` holds the work records (briefs, slices and tasks) and the service interface that reads and
  changes them.
- `docs/` holds the design documents, indexed by `docs/README.md`.
- `Makefile` holds the build, test and lint commands.
