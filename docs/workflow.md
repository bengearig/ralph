# Workflow

Status: draft

How work moves through the method in [the Manifesto](../MANIFESTO.md). This doc covers feature work, which is broken
down into top-level, middle and bottom units. Quick work follows [0007](decisions/0007-orphan-task-delivery.md). Terms
are defined in [concepts.md](concepts.md).

## Receive

Input: an instruction from the user.

- The instruction is stored verbatim as the brief of a top-level unit.
- Ambiguity is resolved by asking the user, before anything is planned (Article I).
- Receive proposes an outline: the top-level unit, its middle units, and rough bottom units. Rough bottom units are
  unplanned: a title and perhaps a summary.
- Whether the outline needs the user's approval before it is created is configurable. Without a required approval, the
  outline is created and then shown. Revisions to an existing outline always ask.
- Receive records the dependencies between middle units when one needs another merged first. Open.
- Receive can start from an issue in an issue source, which becomes its input and is linked to the resulting units. See
  [task-manager-and-issues.md](task-manager-and-issues.md).
- Receive links the documents the work relies on to its units. See [documents-and-search.md](documents-and-search.md).

## Articulate

Input: a top-level unit with unplanned bottom units.

- Each bottom unit's details are worked out with the user: a description and acceptance criteria that let an agent with
  no other context implement and verify it (Article II).
- Articulate links further documents to a unit where its details need them.
- Promoting a unit makes it ready. Only ready units are worked by the loop.
- A unit that cannot be made ready is kicked back instead of guessed at (Kickback Clause). Where it goes back to is
  open.

## Loop

Input: a middle unit whose bottom units are ready.

- The loop runs headless, outside the user's session, as a ralph command that drives the harness non-interactively. See
  [0008](decisions/0008-feature-flow.md).
- Each iteration works one ready bottom unit, in order, producing one commit. The prompt includes the unit and the
  documents linked to it, and the agent can search for more through the MCP server.
- The loop works in the user's checkout, on the middle unit's branch, and refuses to start when the working tree has
  uncommitted changes. See [0023](decisions/0023-loop-workspace.md).
- Each iteration leaves a run record, and the unit gets a summary. See [local-state.md](local-state.md).
- A failing iteration is retried. After a configured number of consecutive failures on a unit, the loop stops and asks
  for help (Stall Statute).
- Verification is dictated by project configuration, by the unit's acceptance criteria, or both. Whether it runs after
  each bottom unit, after each middle unit, or both is open.

## Pull request

- Each middle unit has its own branch and becomes a pull request that leaves the result stable and usable.
- Middle units of one top-level unit are worked sequentially where order matters. By default a dependent middle unit
  waits for the previous one to merge; stacking is configurable. See
  [0010](decisions/0010-sequencing-and-merge-detection.md).
- Creating the pull request is a ralph command. The hosting provider is configurable. GitHub and Bitbucket Cloud are
  supported in the first release. See [0021](decisions/0021-first-release-scope.md).
- A pull request closed without merging is recorded and the unit stops for a human decision. See
  [0011](decisions/0011-after-merge-and-closed-pull-requests.md).

## Human review

- A person reviews and merges. Ralph and agents never merge (Article V).
- Feedback on the pull request becomes a new bottom unit appended to its middle unit, and the loop works it like any
  other. See [0009](decisions/0009-review-feedback-and-verification.md).
- Merges and other pull request changes are detected by polling, a webhook, or a user command. `ralph monitor` shows
  them, and a separate action applies the follow-up. See
  [0010](decisions/0010-sequencing-and-merge-detection.md).
- After a merge the units close and the branch is deleted. The next middle unit waits for the user to start the loop,
  unless an auto mode is active. See [0011](decisions/0011-after-merge-and-closed-pull-requests.md).

## Quick work

Small work with no breakdown, such as a bugfix, follows [0007](decisions/0007-orphan-task-delivery.md). It is a task with
no slice, delivered as its own pull request.

1. The entry point is `/ralph:quick <instruction>` in the harness session, or `ralph quick start <title>` on the command
   line. The agent asks the user only where the instruction is ambiguous, and says so when the request is much larger than
   quick work; the user decides.
2. Before anything is created, ralph checks that the checkout is on the base branch, that the working tree has no changes
   other than the files ralph keeps local, and that the base branch is not ahead of its remote. A failed check names what to
   fix and creates nothing.
3. Ralph creates the task and its branch from the base branch, and moves the task to in progress.
4. The agent implements the task in the session.
5. `quick_finish` (or `ralph quick finish`) runs the commands in the `verify.commands` setting, showing them and where
   they came from first. Ralph enforces these commands; the acceptance criteria are for the agent and the reviewer and are
   not run. The first failure stops delivery and changes nothing.
6. When verification passes, ralph commits the changes, pushes the branch and opens a pull request, a draft by default,
   then marks the task done.
7. After a failure the task stays in progress and `ralph status` lists it as unfinalized. Calling finish again resumes: an
   existing commit, pushed branch and open pull request are reused.
8. Nothing merges. A person reviews and merges the pull request.

## Documents

Documents hold architecture, design, decisions and reference material, project-wide or tied to a top-level unit. They
are stored records, and the store is their source. Agents in the loop retrieve them as context, and search covers them.
They can be exported to files in the repository. See [documents-and-search.md](documents-and-search.md).
