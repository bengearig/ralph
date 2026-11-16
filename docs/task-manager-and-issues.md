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
identifies the system, the project in it, and the item.

## Task manager

### Mapping

Which ralph levels correspond to tickets is configurable per level. Any level can be mapped to a ticket type, such as an
epic for a top-level unit. A level that is not mapped has no presence in the task manager. Whether the bottom unit is
mapped by default is open.

### Source of truth

Ralph owns the data and pushes changes to the task manager.

- Ralph's record holds the data, such as title and description.
- When a record changes, ralph updates the linked ticket. When work moves through its states, ralph moves the ticket
  through the system's states. The mapping between ralph's states and the system's is configurable.
- Creating a record creates the ticket, when its level is mapped.

The arrangements in which the outside system owns the data, or ralph keeps no copy, are later. See
[0005](decisions/0005-external-work-items-linking.md).

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

What ralph does when a ticket whose data it owns is changed in the task manager is open.

## Open

See [open-questions.md](open-questions.md).
