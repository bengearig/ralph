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

## Articulate

Input: a top-level unit with unplanned bottom units.

- Each bottom unit's details are worked out with the user: a description and acceptance criteria that let an agent with
  no other context implement and verify it (Article II).
- Promoting a unit makes it ready. Only ready units are worked by the loop.
- A unit that cannot be made ready is kicked back instead of guessed at (Kickback Clause). Where it goes back to is
  open.

## Loop

Input: a middle unit whose bottom units are ready.

- The loop runs headless, outside the user's session, as a ralph command that drives the harness non-interactively. See
  [0008](decisions/0008-feature-flow.md).
- Each iteration works one ready bottom unit, in order, producing one commit.
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

## Documents

Documents hold architecture, design, decisions and reference material, project-wide or tied to a top-level unit. They
are stored records that agents in the loop retrieve as context, and search covers them. They can mirror files in the
repository. Details are open.
