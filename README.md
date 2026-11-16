# ralph

ralph is a developer-side tool that wraps a coding harness and keeps development to a plan. It follows the
[Manifesto](MANIFESTO.md).

The project is under construction. The design is in [docs/](docs/README.md).

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

The binary is built without cgo, so it stays static.

### Repository layout

- `cmd/` holds the program entry points.
- `internal/` holds the packages the program is built from, including the command-line interface and shared test
  helpers.
- `docs/` holds the design documents, indexed by `docs/README.md`.
- `Makefile` holds the build, test and lint commands.
