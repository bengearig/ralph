---
description: "ralph quick: do a small piece of work on its own branch and open a pull request"
argument-hint: "[what to do]"
---
Do a small piece of work on its own branch and deliver it as a pull request for a person to review. You do the work in this session; ralph creates the branch, verifies, commits, pushes and opens the pull request.

1. Take what the user typed after the command ({{arguments}}) as the request. If nothing was typed, ask the user what they want done, and wait for the answer.
   - What was typed may be a ticket key such as ABC-123 or the address of a ticket. In that case read it with the `{{tool "issue_get"}}` tool. If the tool is not available, or says no issue source is configured, tell the user and continue with what was typed as the request.
   - Treat the ticket's title and description as the request: they are the user's words. Ask questions only where they are ambiguous, and say so if the ticket is much larger than a small change.
2. Ask the user questions only where the request is ambiguous. Ask a few at a time, the most consequential first. If the request is unambiguous, ask none: for example, changing a colour to a given value needs no questions.
3. Judge the size. If the request is much larger than a small change (several independent outcomes, several areas of the project, or work that would need several commits), say so plainly, recommend planning it as larger work instead, and stop without starting anything. The user decides; never switch to another way of working by yourself.
4. Once the request is clear, call the `{{tool "quick_start"}}` tool with:
   - a one-line title;
   - a body, where the title is not enough;
   - acceptance criteria, written as short statements a reviewer can check. Leave them out when the title is the whole story.
   - for a ticket, the key or address as `issue`; give a title or body only where you have a better one than the ticket's.
5. If `{{tool "quick_start"}}` is refused (for example a working tree with changes, the wrong branch, a base branch ahead of its remote, or a detached head), report what the result says and stop. Never stash, commit, reset or switch branches on the user's behalf.
6. Implement only what the task says, in the project's own style. Never touch anything under `.ralph`. As you go, run the verification commands listed in the `{{tool "quick_start"}}` result.
7. When the work is done, call the `{{tool "quick_finish"}}` tool with a short summary of what changed.
   - If the result is `verification_failed`, fix the cause and call it again. Never weaken or remove a test, and never edit configuration to skip verification. If the same failure remains after two attempts, stop and tell the user.
   - If the result is `push_failed`, `commit_failed` or `pull_request_failed`, report the result and what it says to do. Call again only when the cause is fixed.
8. After success, report the pull request address and whether it is a draft. Say that nothing has been merged and that a person reviews the pull request. Give the command to return to the base branch.

Never merge anything, never push by yourself, and never force anything. If any tool result contains a line starting `Notice for the user:`, relay it to the user.
