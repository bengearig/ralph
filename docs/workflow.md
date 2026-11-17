# Workflow

Status: draft

How work moves through the method in [the Manifesto](../MANIFESTO.md). This doc covers feature work, which is broken
down into top-level, middle and bottom units. Quick work follows [0007](decisions/0007-orphan-task-delivery.md). Terms
are defined in [concepts.md](concepts.md).

## Receive

Input: an instruction from the user.

- The instruction is stored verbatim as the brief of a top-level unit.
- Ambiguity is resolved by asking the user, before anything is planned (Article I). Each answer is recorded as a note
  on the brief, so the clarifications stay with the instruction.
- The command creates the outline with the same tools an agent uses elsewhere: it reads related briefs, reports how
  planning is configured, creates the units, records dependencies, and finishes by showing the status.
- Receive proposes an outline: the top-level unit, its middle units, and rough bottom units. Rough bottom units are
  unplanned: a title and perhaps a summary.
- The setting `plan.confirm_outline`, off by default, says whether a new outline needs the user's approval before it is
  created. Without a required approval, the outline is created and then shown. Revisions to an existing outline always
  ask, whatever the setting says. Where the harness has a plan mode, as Claude Code does, the approval uses it; other
  harnesses show the outline in the conversation and wait.
- Receive records the dependencies between middle units when one needs another finished first. A middle unit waits for
  another, in the same top-level unit or another, and a dependency is satisfied when the unit it waits on is done. A
  cycle is rejected. See [0010](decisions/0010-sequencing-and-merge-detection.md).
- Receive can start from an issue in an issue source, which becomes its input and is linked to the resulting units. See
  [task-manager-and-issues.md](task-manager-and-issues.md).
- Receive searches the existing documents before it writes new ones, stores what the work needs as documents, and links
  the documents the work relies on to its units. See [documents-and-search.md](documents-and-search.md).

## Articulate

Input: a top-level unit with unplanned bottom units.

- The command reads the unit and its notes, then each bottom unit's details are worked out with the user: a description and acceptance criteria that let an agent with
  no other context implement and verify it (Article II).
- Articulate searches and reads documents, stores a design the unit needs as a document, and links further documents to
  a unit where its details need them.
- Details are written to the unit and the unit is then promoted, with a reason. A promotion that lacks a description or
  acceptance criteria is refused and says what is missing.
- Promoting a unit makes it ready. Only ready units are worked by the loop.
- A bottom unit can be made ready only when it has a description and, if it belongs to a middle unit, acceptance
  criteria. Blocking, kicking back and cancelling a unit each need a reason, which is recorded as a note.
- A unit that cannot be made ready is kicked back instead of guessed at (Kickback Clause): it is moved back to unplanned
  with a reason, and when the plan around it is wrong, the plan is made again. The kickback is also noted on the unit,
  and Receive revises the plan, always asking first, leaving the history intact.

## Loop

Input: a middle unit whose bottom units are ready.

- The loop runs headless, outside the user's session, as a ralph command that drives the harness non-interactively. See
  [0008](decisions/0008-feature-flow.md).
- `ralph loop <slice>` works one middle unit, named by its ID. Each iteration works one ready bottom unit, in order,
  producing one commit. A middle unit that waits for another that is not done is not started, and its bottom units are
  not worked. The loop stops at the first bottom unit that is not ready, and `--dry-run` prints the plan and changes
  nothing. Nothing is pushed.
- The prompt includes the unit and the documents linked to it, and the agent can search for more through the MCP
  server. The `loop.prompt_documents_bytes` setting limits the linked-document text, and a document that does not fit
  is replaced by a pointer to read it with `doc_get`.
- The loop works in the user's checkout, on the middle unit's branch. It starts from the base branch and creates the
  branch, or continues on the branch when the checkout is already on it, and refuses to start on any other branch, when
  the branch already exists while the checkout is on the base, or when the working tree has uncommitted changes. Only
  one loop runs in a project at a time. See [0023](decisions/0023-loop-workspace.md).
- Each iteration leaves a run record, and the unit gets a summary. See [local-state.md](local-state.md).
- Ralph, not the agent, makes the commit. After the agent finishes, ralph runs the verification commands and then
  commits the whole tree once, with the unit's ID in the subject and a `Ralph-Task` trailer. An attempt that changes
  nothing fails.
- A failing iteration is retried, and the retry prompt says what failed. After `loop.max_failures` consecutive
  failures on a unit, the loop blocks the unit with the reason, leaves the tree as the last attempt left it, stops with
  exit status 2 and asks for help (Stall Statute). The next run refuses until the tree is committed or discarded; once
  the unit is unblocked with a reason, the count starts again.
- Verification is dictated by project configuration, by the unit's acceptance criteria, or both. The configured
  commands run after each bottom unit, before it is committed; a failure fails the attempt. Acceptance criteria are for
  the agent and the reviewer, and ralph does not run them. See
  [0009](decisions/0009-review-feedback-and-verification.md).
- An interrupt ends the harness's whole process group, returns the unit to ready with the reason `loop interrupted`
  and leaves the tree as it is; the loop exits with status 130. A unit left in progress by a loop that was killed is
  settled by the next run: done when the branch holds its commit, ready again when it does not.

## Pull request

- Each middle unit has its own branch and becomes a pull request that leaves the result stable and usable.
- Middle units of one top-level unit are worked sequentially where order matters. By default a dependent middle unit
  waits for the previous one to merge; stacking is configurable. See
  [0010](decisions/0010-sequencing-and-merge-detection.md).
- Creating the pull request is a ralph command. The hosting provider is configurable. GitHub and Bitbucket Cloud are
  supported in the first release. See [0021](decisions/0021-first-release-scope.md).
- `ralph pr create <slice>` opens the pull request of a finished slice. It refuses unless the slice has a done task and
  no task that is unfinished, the checkout is on the slice's branch and the working tree has no changes. It then runs
  the configured verification commands, and a failure stops it before anything is pushed. It pushes the branch without
  forcing it and opens a pull request, a draft by default, from the branch to the base branch, titled with the slice's
  ID and title. The body names the brief, gives the slice's body, lists the tasks with their short commits, shows the
  verification results and says which slice ralph opened it for. The pull request is recorded on the slice as a link, a
  note and a record that `ralph status <slice>` lists.
- Fixes made after review are published with the same command: when a pull request is already open for the branch, it
  pushes the new commits and reports that the pull request was reused.
- A pull request closed without merging is recorded and the unit stops for a human decision. See
  [0011](decisions/0011-after-merge-and-closed-pull-requests.md).

## Human review

- A person reviews and merges. Ralph and agents never merge (Article V).
- Feedback on the pull request becomes a new bottom unit appended to its middle unit, and the loop works it like any
  other. `ralph pr feedback <slice>` reads the feedback on the slice's latest pull request and lists what it found,
  changing nothing. With `--create`, each inline comment and each review that requests changes becomes a ready task, once:
  a task that already exists for an item is shown instead of a second one. With `--all`, general comments and plain
  reviews that have text are eligible too; an approval never is. Tasks are only created while the pull request is open.
  See [0009](decisions/0009-review-feedback-and-verification.md).
- Merges and other pull request changes are detected by polling, a webhook, or a user command. `ralph pr check <slice>`
  is the user command: it reads the slice's latest pull request from the host and records what changed. Every path
  records through the same handler. See [0010](decisions/0010-sequencing-and-merge-detection.md).
- `ralph monitor` shows every brief and slice that is not finished, with the pull request of each slice as recorded and,
  unless `--no-poll` is given, as the host reports it now, and the follow-ups that would apply. It changes nothing and
  exits 0 when the host cannot be used. `--watch` repeats it every `--interval` (at least 10 seconds) until interrupted,
  `--all` includes finished work and `--json` prints the view as JSON.
- `ralph apply [slice]` is the separate action: it records what the host reports and applies follow-ups of the kinds
  named by flags. `--close` deletes the branch of a merged slice and updates the base branch, `--feedback` creates tasks
  from review feedback as `ralph pr feedback --create` does, and `--continue` runs the loop for the next slice. With no
  flags it applies the kinds named by the user-only setting `follow_up.auto`, which is empty by default, so a bare
  `ralph apply` only records. `ralph monitor --watch --apply` applies those kinds each round before showing the view.
  Viewing and acting never mix: without `--apply` the monitor writes nothing.
- A pull request that was closed without merging stops the slice, which shows `NEEDS_ATTENTION`, and `ralph loop` and
  `ralph pr create` refuse it until a person chooses. `ralph pr reopen <slice>` opens a new pull request from the same
  branch, `ralph pr rearticulate <slice>` moves the unfinished tasks back to proposed so the plan is made again, and
  `ralph pr cancel <slice>` cancels the unfinished tasks, and with `--dependents` those of the slices waiting for it;
  without `--yes` it only says what it would do. Finished tasks and commits are never touched. See
  [0011](decisions/0011-after-merge-and-closed-pull-requests.md).
- A slice shows `IN_REVIEW` while its pull request is open and `MERGED` once the merge is recorded. A slice that waits
  for another waits until that one is merged, and a brief whose slices are all merged is `DONE`. Closing a merged slice
  with `ralph apply --close` deletes its local branch, and the remote one too when the setting `follow_up.delete_branches`
  is `both` (`local` by default, `none` deletes nothing), and fast-forwards the base branch from the remote when the
  checkout is on it with no changes. A branch is deleted only when its tip is the commit the merged pull request had, so
  a squash merge is recognised and a branch with later commits, or one that is checked out, is kept with the reason
  given. The close is noted on the slice.
- The next middle unit waits for the user to start the loop, unless auto-continue is on: `continue` in `follow_up.auto`,
  or `ralph apply --continue`. It runs the loop, with what the loop prints indented, for at most one next slice per run:
  the nearest slice after the merged one in the brief that is ready, waits for nothing and was not started this way before. It starts only when
  the checkout is on the base branch, with no changes and up to date with the remote, and says why when it does not. The
  loop spends money, which is why it is opt-in and user-only. See
  [0011](decisions/0011-after-merge-and-closed-pull-requests.md).

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
