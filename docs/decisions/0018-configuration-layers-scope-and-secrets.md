# 0018: Configuration layers, scope and secrets

Status: draft

## Context

The principles call for high configurability that fits many workflows, kept minimal. Some settings must not be
accepted from a file that arrives with a cloned repository.

## Decision

- Layers, lowest to highest precedence: built-in defaults, user file, committed project file, uncommitted project-local
  file, environment variables, command flags. Layers merge per setting.
- Each setting declares its scope as project-allowed or user-only. A user-only setting in the committed project file is
  ignored, and ralph says so.
- A setting names the source of a secret and never holds the value.
- Configuration is YAML.
- The user file lives in the user's configuration directory. The project file and the project-local file live in the
  project's ralph directory, and only the project-local file is excluded from version control.
- A command shows the effective configuration with the layer each value came from, and ships in the first release.
- An unknown setting or invalid value in a file the user controls is an error. A user-only setting in the committed
  project file is a warning and is ignored.
- A setting is added when a decision needs it. A setting without a documented default, scope and meaning is not
  accepted.

## Alternatives rejected

- No project-local layer: personal overrides would have to go in the user file, which cannot differ per project.
- Asking the user to trust each project's file once: a prompt that gets clicked through, and it cannot say which
  settings are risky.
- Secret values in the user file: plain text tokens are easy to leak.
- TOML or JSON: YAML supports comments and fits the tooling users already have.
- A shared layer held by a remote instance: deferred. See [open-questions.md](../open-questions.md).

## Consequences

- The project-local file must be excluded from version control. How ralph ensures that is open.
- Every setting needs its scope decided when it is introduced.
- An ignored setting is reported, so a user whose setting does not take effect can find out why.
