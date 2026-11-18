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
- `ralph index` embeds the records for semantic search, and `ralph index --status` shows the provider, the model and what
  is indexed. Semantic search is off until `embeddings.provider` is set to `ollama`; it and the other `embeddings.*`
  settings are user-only, and the vectors are kept in the local file `.ralph/index.db`. When the provider fails, search
  falls back to keyword search and says so. When the model or another embedding setting changes the index is stale and
  semantic search stays off until `ralph index --rebuild --yes` builds it again (without `--yes` it prints the plan).
  `RALPH_OLLAMA_LIVE=1` runs the checks against a real Ollama; `RALPH_OLLAMA_MODEL2` names a second model for the
  rebuild check.
- `ralph mcp` serves ralph's records to an agent over the Model Context Protocol on standard input and output.
- `ralph loop <slice>` works a slice's ready tasks in order on its own branch, one verified commit per task and nothing
  pushed; it retries a failed attempt and blocks the task after `loop.max_failures`. `--dry-run` prints the plan.
- `ralph pr create <slice>` verifies a finished slice, pushes its branch and opens a draft pull request, or publishes
  new commits to the one that is open. `ralph pr feedback <slice>` lists the review feedback, and with `--create` turns
  inline comments and requests for changes into tasks for the loop. `ralph pr check <slice>` records a pull request
  closed without merging, which stops the slice until `ralph pr reopen`, `ralph pr rearticulate` or `ralph pr cancel`.
  Nothing is merged.
- `ralph monitor` shows the briefs and slices that are not finished, with their pull requests as recorded and as the host
  reports them and the follow-ups that would apply, and changes nothing (`--watch`, `--no-poll`, `--all`, `--json`).
  `ralph apply` records what the host reports and applies follow-ups named by `--close`, `--feedback` and `--continue`,
  or by the user-only setting `follow_up.auto`, which is empty by default; `follow_up.delete_branches` (`local` by
  default, `both` or `none`) says which branches a close deletes, and only when the branch holds nothing the merged pull
  request did not have.
- Jira Cloud can hold a ticket for each brief and slice: set `taskmanager.provider` to `jira` and the user-only
  `jira.site`, `jira.user` and `jira.token` (a reference such as `env:JIRA_TOKEN`) and the project `jira.project`. Ralph
  pushes after each command or tool call that wrote something, `ralph tickets` lists the tickets and
  `ralph tickets sync` pushes on demand, and a ticket a person edited in Jira is held until `ralph tickets accept` or
  `ralph tickets overwrite`; see [docs/task-manager-and-issues.md](docs/task-manager-and-issues.md). The requests were
  written from Atlassian's published API and are checked against a stub; `RALPH_JIRA_LIVE=1` with `RALPH_JIRA_SITE`,
  `RALPH_JIRA_USER`, `RALPH_JIRA_TOKEN` and `RALPH_JIRA_PROJECT` runs the checks against a real site, in a project you
  can write to, and `RALPH_JIRA_DELETE=1` removes the tickets the checks made.
- Work can start from a Jira ticket: set `issues.provider` to `jira` (it uses the same `jira.*` settings) and run
  `ralph issue show ABC-123` to read a ticket or `ralph quick start --issue ABC-123` to start quick work from it; in an
  agent session, give `/ralph:quick` or `/ralph:receive` a ticket key or address. The ticket's key is then part of the
  branch, the commit subject and the pull request, and nothing is written to the ticket. `RALPH_JIRA_LIVE=1` with
  `RALPH_JIRA_SITE`, `RALPH_JIRA_USER`, `RALPH_JIRA_TOKEN` and `RALPH_JIRA_ISSUE` (the key of a ticket the account can
  read) runs the check against a real site; it only reads. See
  [docs/task-manager-and-issues.md](docs/task-manager-and-issues.md).
- `ralph runs purge` removes run records under `.ralph/runs/` (`--older-than DURATION`, `--slice <ID>`; it lists unless
  `--yes`), and a loop removes those older than `runs.keep_days` when it starts.
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
