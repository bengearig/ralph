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
under [Detaching](#detaching).

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

### Starting work from an issue

- Quick work starts from an issue by passing its reference to the quick-work entry point of
  [0007](decisions/0007-orphan-task-delivery.md). Larger work starts the Receive flow with the issue as its input.
- Ralph reads the issue to give the interview its starting point. After that, the unit's own record holds the data, and
  the issue is not kept in step with it.
- The unit stores a link to the issue.
- When the issue is itself a ticket in the task manager, the unit links to that ticket and the task manager role
  updates it. It does not create a second ticket. How this works when the ticket's type is not one the mapping covers
  is open.

### Completing work

Pull requests that ralph creates carry a reference to the linked issue in a form the system understands, so the system
can link or close the item itself. This reuses what the system already does instead of ralph pushing states. Whether
ralph also comments on or moves the issue is open.

## Detecting changes

Ralph learns that a ticket or issue changed by polling the system, or by a command the user runs. Both feed the shared
change handler of [0010](decisions/0010-sequencing-and-merge-detection.md). Webhooks are later.

What ralph does when a ticket whose data it owns is changed in the task manager is described under
[Edits made in Jira](#edits-made-in-jira).

## Open

See [open-questions.md](open-questions.md).
