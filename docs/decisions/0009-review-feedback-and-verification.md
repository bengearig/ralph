# 0009: Review feedback and verification

Status: draft

## Context

Reviewers request changes on pull requests, and the loop's results must be checked by honest tests (Article III).
Different people and projects verify differently.

## Decision

- Feedback received on a pull request becomes a new bottom unit appended to that pull request's middle unit. A ralph
  action may read the feedback back from the pull request and create the bottom unit.
- Verification can be dictated by project configuration, by a task's acceptance criteria, or both. The two can differ
  across people and projects. Precedence: configured verification commands are enforced by ralph, which runs them, and a failure
  stops delivery. Acceptance criteria are for the agent and the reviewer: ralph shows them and does not run them.

## Alternatives rejected

- Review feedback as an orphan task: it belongs to the slice under review, not to nothing.
- Handling feedback outside ralph's model: loses the record and the loop's help.

## Consequences

- The pull request provider integration must read review feedback, not only create pull requests.
- A middle unit can gain bottom units after its pull request is open, so its state must allow that.
- The loop runs the configured verification commands after each bottom unit, before committing it, and a failure fails
  the attempt. See [workflow.md](../workflow.md).
