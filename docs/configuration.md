# Configuration

Status: draft

How ralph is configured so it fits many workflows without disrupting them. Rationale is in
[0018](decisions/0018-configuration-layers-scope-and-secrets.md). The principles are in [overview.md](overview.md).

## Layers

Settings are resolved from layers. A later layer overrides an earlier one:

1. Built-in defaults.
2. The user file, which applies to every project for that user.
3. The project file, which is committed and shared by everyone on the project.
4. The project-local file, which is not committed and holds one person's overrides for one project.
5. Environment variables.
6. Command flags.

Layers are merged setting by setting. A layer that sets one setting leaves every other setting alone.

## Format

YAML. Files are plain and may contain comments.

Locations:
- The user file lives under the user's configuration directory, following the platform's convention.
- The project file lives in the project's ralph directory at the repository root and is committed.
- The project-local file lives in that same directory and is excluded from version control.

Exact file names are open.

## Inspecting and validating

- A command prints the effective configuration, merged from every layer, and shows which layer each value came from.
  It is part of the first release.
- An unknown setting or an invalid value in a file the user controls stops ralph with an error that names the file and
  the setting. A silently ignored typo is treated as worse than a failure.
- A user-only setting found in the committed project file is a warning, not an error, because the user did not write
  that file. The setting is ignored.

## Setting scope

Every setting declares where it may be set:
- Project-allowed: any layer.
- User-only: the user file, the project-local file, environment variables and flags. It is ignored in the committed
  project file, and ralph reports that it was ignored.

A setting is user-only when accepting it from a committed file would let someone else's file change what runs on a
user's machine or what they are trusted with. Examples are the permission mode that hands approval to the harness, and
the choice of remote instance. See [0017](decisions/0017-integration-files-and-headless-permissions.md).

## Secrets

A setting names where a secret comes from, such as an environment variable. It never holds the secret. Secrets do not
appear in any configuration file. Other sources, such as an operating system keychain, are possible later.

## Settings reference

Every setting is documented with its meaning, its default, its scope and the layers that may set it. The reference is
written alongside the feature that adds the setting, and a setting without an entry is not accepted. The settings
themselves are defined by the docs of the features they belong to.

### Task manager settings

The task manager of [task-manager-and-issues.md](task-manager-and-issues.md) is off until `taskmanager.provider` is
`jira`.

| Setting | Scope | Default | Meaning |
| --- | --- | --- | --- |
| `taskmanager.provider` | any layer | `none` | `none` makes no ticket and no request; `jira` pushes briefs, slices and tasks as tickets. |
| `jira.site` | user-only | none | The address of the Jira Cloud site. https, or http on a loopback host; no credentials, query or fragment. |
| `jira.user` | user-only | none | The email of the Atlassian account that owns the API token. |
| `jira.token` | user-only | `env:JIRA_TOKEN` | A reference to the API token, never the token. |
| `jira.project` | any layer | none | The key of the project that receives the tickets. |
| `jira.type.brief`, `jira.type.slice`, `jira.type.task` | any layer | `Feature`, `Task`, none | The ticket type of each level; empty means the level has no ticket. |
| `jira.states` | any layer | none | Items `STATE=Status` that replace the built-in mapping of ralph's states to Jira's statuses. |
| `jira.flow` | any layer | none | Status names in the order a ticket normally moves, so a move with no direct transition steps through them. |
| `jira.sprint`, `jira.board`, `jira.sprint_levels` | any layer | none | The sprint new tickets are put in, the board that holds it when the project has several, and the levels that are put in it (slice and task when empty). |
| `jira.auto_sync` | any layer | `true` | Whether tickets are pushed after a command or tool call that wrote something; `false` leaves it to `ralph tickets sync`. |
| `jira.poll_interval`, `jira.sync_timeout`, `jira.timeout` | any layer | `5m`, `15s`, `30s` | The least time between automatic reads for edits made in Jira, the longest an automatic push may take, and the per-request timeout. |

The site, the user and the token are user-only because they choose where ticket text is sent and whose credential sends
it. A committed file that could name the site would send a developer's briefs, slices and tasks to a place a stranger
chose, so those three are ignored in the committed project file with a warning. The provider and the project key may be
committed so a team shares them, but without the user's own site a committed `taskmanager.provider: jira` does nothing
until the user sets it up: automatic runs stay quiet and `ralph tickets` says that `jira.site` is not set.

## Minimalism

A setting is added when a decision in these docs needs it and is not added speculatively. Some configurability arrives
later by design. See [0002](decisions/0002-design-principles.md).

## Open

See [open-questions.md](open-questions.md).
