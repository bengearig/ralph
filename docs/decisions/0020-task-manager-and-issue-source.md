# 0020: Task manager and issue source

Status: draft

## Context

Two uses of outside systems were being called by one name. Jira, in the author's workflow, keeps a copy of ralph's units
for reporting. GitHub Issues holds bug reports and requests that arrive from outside and that work is started from. One
system can serve both uses. [0005](0005-external-work-items-linking.md) set the principle: every integrated record is
linked, and the source of truth is configurable.

## Decision

- Two roles, each its own interface:
  - Task manager: a copy of ralph's units kept outside ralph for reporting. Ralph owns the data and pushes changes.
  - Issue source: items that originate outside ralph and from which work is started.
- The first release supports Jira in both roles. GitHub Issues is a later issue source.
- Which ralph levels correspond to tickets in the task manager is configurable per level.
- A unit started from an issue stores a link to it. When that issue is a ticket in the task manager, the unit links to
  that ticket instead of a new one being created.
- Pull requests ralph creates carry a reference to the linked issue in a form the system understands.
- Ralph learns of changes by polling and by a user command, through the shared change handler of
  [0010](0010-sequencing-and-merge-detection.md). Webhooks are later.

## Alternatives rejected

- One "tracker" interface for both uses: the data flows in opposite directions and the source of truth differs.
- GitHub Issues in the first release: Jira covers the author's workflow and the first release stays small.
- Ralph pushing states to issues in the first release: the system already closes or links an item from a pull request
  reference.
- A fixed mapping of levels: conventions differ, for example in whether epics are used.
- Webhooks in the first release: they need a reachable server, and polling plus a command covers the need.

## Consequences

- Each role's interface must stay free of Jira specifics that another system would not have. With one system in the
  first release, nothing proves this beyond discipline.
- Ralph writes to the task manager and reads from the issue source, so it needs credentials for both. Handling follows
  [0018](0018-configuration-layers-scope-and-secrets.md).
- A ticket can be created by a person, read by ralph as an issue, and then updated by ralph as a unit's mirror. Ralph
  must never create a duplicate for it.
- A change made in the task manager to data ralph owns: ralph keeps its own value and reports the difference, nothing is
  pushed over it, and the user chooses to accept Jira's value or to overwrite it. Neither is ever automatic.
- The task manager of the first release is Jira Cloud only. The requests were written from Atlassian's published API and
  are checked against a stub; a live check against a real site is how they are verified there.
- A ticket carries a label that names the unit it was made for, so a ticket made just before ralph could record it is
  found and adopted, and no duplicate is created.
- The issue source reads a ticket and nothing more: no listing, no searching, and no write to the ticket, not a comment,
  not an edit and not a move on account of a pull request. The user names the ticket to start from. It reuses the
  `jira.*` credentials, so `issues.provider` is the only new setting, and it works without the task manager.
- Starting from a ticket links the unit twice: `issue:jira:<KEY>` says where the work came from and `jira:<KEY>` makes it
  the unit's ticket. The unit's row is marked as started from the ticket, so ralph never pushes the unit's title or body
  over it, never reports a person's edit of them, and still moves its status when the unit's level is mapped. A ticket
  type the mapping does not cover is no obstacle: the unit links to the ticket whatever its type.
- The ticket's key is part of the branch name, the commit subject, the pull request title and a line of its body, through
  the `.Ticket` field of the templates. A branch already made keeps its name.
- Starting from a ticket fails closed: when it cannot be read or a unit already holds it, nothing is created.
- The Jira reads, like the task manager's requests, were written from Atlassian's published API and are checked against a
  stub; the live test is how they are checked on a real site.
- The per-level mapping adds configuration, and a level's mapping must be valid for the system's ticket types.
