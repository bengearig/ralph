# Task manager and issue source

Status: draft

How ralph's records relate to tickets in outside systems. Rationale is in
[0020](decisions/0020-task-manager-and-issue-source.md). Terms are in [concepts.md](concepts.md), and the principle
behind it is in [0005](decisions/0005-external-work-items-linking.md).

## Two roles

An outside system plays one or both of two roles. The roles are separate interfaces, each a service in the sense of
[architecture.md](architecture.md), so each can be local or remote. Remote lets a server hold the credentials, as with
pull requests in [0015](decisions/0015-pull-request-identity.md).

- Task manager: it keeps a copy of ralph's units so the work can be seen and reported on outside ralph. Data flows from
  ralph to the system, and ralph is the source of truth.
- Issue source: items originate there, such as bug reports and requests, and work is started from them. Data flows from
  the system into ralph.

The first release supports Jira in both roles. GitHub Issues is a later issue source. Both interfaces are written so
other systems can be added without changing the model.

## Link

Every ralph record that corresponds to a ticket or issue carries a link to it. The link is stored in ralph's record and
identifies the system, the project in it, and the item. For Jira the link is an external link on the unit of the form
`jira:<KEY>`, such as `jira:ABC-12`; the project is the key's prefix. Removing the link detaches the unit, as described
under [Detaching](#detaching). A unit started from a ticket also carries `issue:jira:<KEY>`, described under
[Starting work from an issue](#starting-work-from-an-issue).

## Task manager

### The interface

The task manager role is an interface that names no Jira specific. A system holds tickets, each with a key, a type, a
title, a Markdown body, a status and a parent. The interface can check the credentials and the project, list the ticket
types, find the ticket made for a ralph reference, create, update, read, move a ticket to a status, put it in a named
plan (Jira calls it a sprint), and say what the system would hold if given a title and a body. Its errors are the ones
of the pull request providers: not found, unauthorized (naming the setting), invalid, rate limited and unavailable.

Jira Cloud is the system the first release supports, through its REST API at the site's address with an Atlassian account
email and an API token. Jira Data Center and Server are not supported. The requests were written from Atlassian's
published API and are checked against a stub of it, which is how the tests run; the live test is how they are checked on
a real site (see the README).

### Settings

`taskmanager.provider` is `none` by default, which makes no ticket and no request, or `jira`. The Jira settings are in
[configuration.md](configuration.md): where the site is and who acts (`jira.site`, `jira.user`, `jira.token`, user-only),
the project (`jira.project`), the types of the levels, the status mapping (`jira.states`, `jira.flow`), the sprint
(`jira.sprint`, `jira.board`, `jira.sprint_levels`) and the timing of automatic runs.

### Mapping

Which ralph levels correspond to tickets is configurable per level. Any level can be mapped to a ticket type, such as an
epic for a top-level unit. A level that is not mapped has no presence in the task manager. By default a brief is a
`Feature` and a slice is a `Task`, and a task has no ticket, because tasks are sized to a commit and would flood the
system. The types are the settings `jira.type.brief`, `jira.type.slice` and `jira.type.task`; an empty one unmaps its
level.

Each ticket has:

- the type of its level, and the unit's title and body. A task's body is followed by its acceptance criteria under a
  heading.
- the labels `ralph` and `ralph-<project id>-<unit id>`. The second names the unit, so a ticket made just before ralph
  could record it is found again by its label and adopted instead of made twice.
- a parent: the ticket of the nearest mapped ancestor, so a slice's ticket sits under its brief's. A unit whose
  ancestor has no ticket yet waits and says so.
- a status, from the state of the unit.
- a sprint, when one is configured.

Nothing is created for cancelled work. A ticket that exists is still updated and moved.

### Source of truth

Ralph owns the data and pushes changes to the task manager.

- Ralph's record holds the data, such as title and description.
- When a record changes, ralph updates the linked ticket. When work moves through its states, ralph moves the ticket
  through the system's states. The mapping between ralph's states and the system's is configurable.
- Creating a record creates the ticket, when its level is mapped.

The arrangements in which the outside system owns the data, or ralph keeps no copy, are later. See
[0005](decisions/0005-external-work-items-linking.md).

### The ticket rows

The local database keeps one row for each unit that has a ticket: the key, the system's own identifier, what was last
pushed (the title and body as hashes, the status and the parent), whether the ticket was put in its sprint, any
unresolved difference found in Jira, and the last failure with when and how often. Ralph does not queue pushes. Each
pass works out what every ticket should be, compares it with the row and does the difference, so nothing is lost when a
push fails. A row and a link that disagree are settled in favor of the link: if the link was removed the unit is
detached, and if the link names a ticket that has no row, a row starts from it without creating anything.

### What is pushed and when

A pass creates the ticket of each mapped unit that has none (it looks for the unit's label first), records the row and
the link together, and puts the ticket in the sprint. For the tickets that exist, it pushes a changed title or body and
moves the ticket when the status the unit maps to differs from the one last pushed. A failure on one unit is recorded
on its row and does not stop the others. A rate limit, or Jira refusing the credentials, stops the pass, because every
further request would fail the same way; a rate limit also pauses automatic passes until the time Jira names.

A pass runs automatically after a command or a tool call that wrote something, when `taskmanager.provider` is `jira` and
`jira.auto_sync` is not `false`. It is bounded by `jira.sync_timeout`, and it never fails the command or the tool call: a
failure is recorded on the row, the command prints one warning line on standard error and exits as it would have, and
the next pass tries again, an automatic one no sooner than `jira.poll_interval` after a step failed. The MCP server runs
its pass in the background after a tool call. A read command makes no request. `ralph tickets sync` does the same pass
on demand, always tries, and exits 1 when anything failed. `ralph tickets` lists the tickets from the local rows, with
any difference or failure; `--json` is available on each.

### Status mapping

The ticket's status follows the unit's state: a task's own state, or the status derived for a slice or a brief. The
setting `jira.states` is a list of items `STATE=Status` that replace the built-in mapping. Without an item the built-in
mapping is: empty, articulating, ready, proposed and to do map to `To Do`; in progress and needs attention map to
`In Progress`; done and merged map to `Done`. Blocked, in review and cancelled have no built-in status and leave the
ticket where it is until the user maps them. A new ticket is made in the project's initial status and moved only when
its mapped status is another.

Ralph moves a ticket with a transition Jira lists as available from the status it is in. When none leads to the target
and the setting `jira.flow` lists the statuses in the order a ticket normally moves, and both statuses are in it, ralph
steps through the statuses between them. Otherwise the move fails, the ticket stays where it is, and the failure names
the statuses reachable from the current one. A transition that needs fields Jira will not take from ralph fails the same
way with Jira's message. Ralph never walks transitions on its own guess.

### Sprints and parents

When `jira.sprint` names a sprint, a ticket is put in it once, after it is created, if its level is one of
`jira.sprint_levels` (slice and task when the setting is empty). `jira.board` is needed only when the project has more
than one board. A sprint that cannot be used does not undo the ticket: the step is retried by later passes. A ticket's
parent is the ticket of its nearest mapped ancestor.

### Descriptions

A body is written to Jira as Atlassian Document Format from a small Markdown subset: paragraphs, headings, emphasis,
code, links, quotes, rules and lists. What Jira holds is compared and read back through the same conversion, so a
construct outside the subset reads back as its plain text and is not mistaken for an edit.

### Edits made in Jira

Ralph looks for tickets changed in Jira when a person asks with `ralph tickets check` and, in an automatic pass, when the
last check is older than `jira.poll_interval`. It compares the ticket with what it last pushed and records a difference
in the title, the body or the status, or that the ticket no longer exists. A ticket moved to a new key is followed
silently. Reading never writes to Jira, and an unreachable Jira records no difference.

A unit with a difference is held: ralph pushes nothing for it until the user chooses, so neither side loses an edit
unseen. `ralph status`, `ralph monitor` and the MCP server say so (see [Notices](#notices)). Two commands resolve it, and
neither runs on its own:

- `ralph tickets accept <unit>` imports Jira's values: the title is set, the body is set from the ticket's description
  converted back to Markdown, a different status is acknowledged without moving anything, and a deleted ticket is
  detached. The unit changes like any edit, a note records what was accepted, and what was pushed follows.
  Accepting a status leaves the ticket where it is until the unit's mapped status changes (the unit's state changes,
  or `jira.states` maps the same state elsewhere); overwriting a status moves the ticket to the mapped status.
- `ralph tickets overwrite <unit>` pushes ralph's title, body and status over Jira's. A deleted ticket is created again
  with a new key.

Both take `--field` (`title`, `body`, `status` or `deleted`) to resolve only some differences. Without `--yes` they print
what would change and change nothing.

### Detaching

Removing the unit's `jira:<KEY>` link detaches it. Ralph leaves the ticket alone from then on and does not make another
for the unit; `ralph tickets` shows it as detached. Adding a link `jira:<KEY>` to a unit that has none adopts that ticket
instead of creating one: the unit's title and body are pushed on the next pass.

### Notices

`ralph status` and `ralph monitor` print `tickets: <n> with differences in Jira; run ralph tickets` and
`tickets: <n> failed to sync; run ralph tickets` when something needs the user, from the local rows and with no request,
and print nothing when the provider is `none`. The MCP server adds a notice with the code `tickets_need_attention` once
for each session, saying the same and telling the agent to ask the user before accepting or overwriting anything.

## Issue source

### The interface

The issue source role is an interface that names no Jira specific. It parses a reference into a ticket reference (a
system and a key) and reads the ticket: its key, address, type, status, title, parent and a Markdown body. Reading is the
whole of it. Listing and searching for tickets are not part of the first release, so the user names the ticket to start
from. Its errors are the ones of the task manager: not found, unauthorized (naming the setting), invalid, rate limited and
unavailable. It also says that the issue source is not configured, naming the setting to set.

`issues.provider` is `none` by default, which reads nothing, or `jira`. The issue source reuses the `jira.*` credentials of
the task manager (`jira.site`, `jira.user`, `jira.token`, user-only), so one login serves both roles, and it works with
`taskmanager.provider` at `none`. The provider may be committed so a team shares it; without the user's own site it does
nothing, and the command that needs it says that `jira.site` is not set. The settings are in
[configuration.md](configuration.md).

The Jira reads were written from Atlassian's published API and are checked against a stub of it, which is how the tests
run. The live test is how they are checked on a real site (see the README).

### References

A reference is a ticket key such as `ABC-123` (any case, read as upper case), the address of the ticket on the configured
site (`https://<site>/browse/ABC-123`), or an address on that site that selects the ticket in a board or backlog with
`selectedIssue`. An address on another site, or anything else, is refused and the message says what a reference looks
like. Ralph reads the ticket under the key the site reports for it, so a ticket that moved to a new key is followed.

`ralph issue show <ref>` prints the ticket (`--json` prints it as data) and the `issue_get` tool returns it. Both read
only: nothing is written in ralph or in Jira.

### Starting work from an issue

- Quick work starts from a ticket with `ralph quick start --issue <ref>`, or the `issue` argument of `quick_start`. The
  title may be left out and is the ticket's title, and the task's body is the ticket's body. See
  [0007](decisions/0007-orphan-task-delivery.md).
- Larger work starts the Receive flow with the ticket as its input. The agent reads it with `issue_get`, carries the
  returned `instruction` into the interview as the user's own words, and creates the brief with `unit_create` and the
  `issue` argument, with that text as the brief's body. `issue` is allowed for a brief and for a task that has no parent;
  a slice or a task under a slice cannot start from a ticket of their own, because they take it from their brief.
- Ralph reads the ticket before anything is created. A ticket that cannot be read, a reference that does not parse, an
  issue source that is not configured, or a ticket that a unit already holds ends the command with an error, and no
  task, branch, link or row is left behind. A ticket is held by one unit: the error names the unit.
- After the first read the unit's own record holds the data. The ticket is not kept in step with it.

Starting from a ticket adds two links to the unit, both external:

- `issue:jira:<KEY>` says the unit was started from that ticket, so the branch name, the commit subject and the pull
  request carry its key. The slices of the brief and their tasks take the ticket
  of the brief, so the key follows the work down.
- `jira:<KEY>` is the link of [Link](#link): it says the unit has that ticket as its ticket, which is what lets the task
  manager role find it.

The unit's row in [the ticket rows](#the-ticket-rows) is marked with the origin `issue`. A ticket that was already there
is not made again: no second ticket is created for the unit, whatever the level mapping says. The ticket's title and
body are left alone: ralph never pushes a title or a body over a ticket it started from, and a person's edit to them in
Jira is not reported as a difference. When the unit's level is mapped to a ticket type, the ticket's status still follows
the unit, moving once when the status the unit maps to changes and never first moving the ticket back, and a ticket that
no longer exists is reported by `ralph tickets check`. When the level is not mapped, as for a task by default, the
ticket is not touched at all. The ticket's own type is never checked against the mapping: the unit links to the ticket
whatever its type.

### Branches, commits and pull requests

When a unit has a ticket, its key is part of the names ralph makes. With the default templates the branch is
`ralph/abc-7-t-1-fix-the-login-crash`, the commit subject is `ABC-7 T-1: Fix the login crash`, the pull request title
starts with `ABC-7`, and the pull request body has a line `Issue: ABC-7 <site>/browse/ABC-7` before the closing line, in a
form Jira understands so it can link the pull request itself. Without a ticket the names are exactly what they were. A
branch that was already made keeps its name when a ticket is linked later, because the branch is recorded on the unit.
The templates are in [configuration.md](configuration.md).

### Completing work

Ralph writes nothing back to the issue source: it posts no comment on the ticket, does not edit it and does not move it
because a pull request was opened or merged, beyond the status move of a mapped level above. The reference in the pull
request is what lets the system link the ticket and close it itself.

## Detecting changes

Ralph learns that a ticket or issue changed by polling the system, or by a command the user runs. Both feed the shared
change handler of [0010](decisions/0010-sequencing-and-merge-detection.md). Webhooks are later.

What ralph does when a ticket whose data it owns is changed in the task manager is described under
[Edits made in Jira](#edits-made-in-jira).

## Open

See [open-questions.md](open-questions.md).
