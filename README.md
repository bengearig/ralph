# ralph

ralph is a developer-side tool that wraps a coding harness and keeps development to a plan. It follows the
[Manifesto](MANIFESTO.md).

The project is under construction. The design is in [docs/](docs/README.md).

## Usage

- `ralph init` sets ralph up in the current git repository.
- `ralph init --harness <name>` (repeatable) also installs ralph's integration for that agent harness, such as
  `claude-code`; without the flag it installs for each harness it detects. When ralph's integration files are out of
  date, `ralph status` and the MCP server say so; run `ralph init` to update them.
- `ralph status` shows briefs, slices and tasks with their statuses.
- `ralph mcp` serves ralph's records to an agent over the Model Context Protocol on standard input and output.

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

### Live GitHub test

`TestLiveGitHub` in `internal/hosting/github` checks the GitHub provider against real GitHub. It is skipped unless `RALPH_GITHUB_LIVE=1`, and it only reads. Set `RALPH_GITHUB_LIVE_REPO` to `owner/repo`, `RALPH_GITHUB_LIVE_BRANCH` to a branch to look up, and put a token in the variable named by `RALPH_GITHUB_LIVE_TOKEN_ENV` (default `GITHUB_TOKEN`). For example:

```sh
RALPH_GITHUB_LIVE=1 RALPH_GITHUB_LIVE_REPO=owner/repo RALPH_GITHUB_LIVE_BRANCH=main \
  go test ./internal/hosting/github -run TestLiveGitHub -v
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
