---
description: "ralph receive: capture instructions and plan them at a high level"
argument-hint: "[the instructions, or the ID of a brief to revise]"
---
Capture the user's instructions in ralph and plan them at a high level: a brief, its slices in order, and the unplanned tasks under each slice. Working out each task's details is a later step.

1. Take what the user typed after the command ({{arguments}}) as the input.
   - If nothing was typed, ask the user what they want done, and wait for the answer.
   - If it is only the ID of an existing brief (for example B-1), follow the steps under "Revising a brief" below instead of the steps that follow.
   - Otherwise it is the instructions.
2. Call the `{{tool "unit_list"}}` tool to look for related briefs. Mention anything relevant to the user, and let them say whether the new instructions belong with an existing brief.
3. Call the `{{tool "search"}}` tool to search for related briefs, tasks and documents. Mention what is relevant to the user, and once the brief exists, link the documents the new work relies on to it (see step 7). A document tied to a brief is not linked to anything until a link is made.
4. Call the `{{tool "planning_settings"}}` tool. If `confirm_outline` is true, the outline needs the user's approval before anything in it is created.
5. Create the brief with the `{{tool "unit_create"}}` tool: level brief, a short title, and the user's instructions word for word as the body. A brief's body is never edited, so nothing the user said is lost. Do not shorten or reword it. The one exception is when creating anything is not possible yet, as the approval text in step 9 explains; then create the brief later, as that text says.
6. Read the project's code only as far as you need to see where the work lands.
7. Ask the user high-level questions, a few questions at a time, the most consequential first. Cover what matters from these areas: goals and what success looks like; scope and non-goals; who is affected; constraints; integrations; risks; and the order in which value should be delivered. Leave implementation details to the articulate command. Do not ask what the instructions or the code already answer. After each answer, record it with the `{{tool "note_add"}}` tool on the brief. A decision, design or architecture that matters beyond a single note becomes a document tied to the brief: search first so nothing is duplicated, then create it with the `{{tool "doc_create"}}` tool, or change the existing one with the `{{tool "doc_update"}}` tool. Link it with the `{{tool "link_add"}}` tool (kind `document`, target the document's ID) to the brief, or to the slices and tasks it affects.
8. Draw an outline.
   - Slices come in order. Each slice must be independently reviewable and leave the result working. Prefer one slice over two that could not stand alone.
   - Under each slice, list its tasks. Each task is an unplanned item with an imperative title and, optionally, a one-sentence summary as its body. Do not write acceptance criteria or detailed descriptions.
   - Where a slice needs another slice merged first, record that with the `{{tool "dependency_add"}}` tool. Order alone is not a dependency: only record one when the later slice cannot work without the earlier one.
9. Create the outline. Approval comes first when `confirm_outline` is true, when the outline revises an existing one, or when the user asked to approve changes first, whether in the request, in this conversation or in the project's instruction file. Approval text for this session:

   {{harnessText "approval"}}

   When approval is not needed, create everything at once: the slices and their tasks with the `{{tool "unit_create"}}` tool, leaving the tasks as they start, and the dependencies. Then show the outline with the new IDs and invite the user to ask for changes. Do not end by asking whether they agree.
10. When the user asks for changes, make them in place: edit with the `{{tool "unit_update"}}` tool, add with the `{{tool "unit_create"}}` tool, and for anything no longer wanted call the `{{tool "task_set_state"}}` tool to set CANCELLED with a reason. Never delete anything.
11. Finish by calling the `{{tool "status"}}` tool. Summarize what was received and planned. Suggest running the articulate command with the brief's ID, or with a slice's ID to start with one slice.

Revising a brief:

1. Call the `{{tool "unit_get"}}` tool on the brief and on its slices and tasks. Read the brief's notes, in particular any KICKBACK notes that prompted the revision, and the documents linked to the brief (`documents` in the result) and the documents that changed it.
2. Discuss the change with the user. Note each decision on the brief with the `{{tool "note_add"}}` tool.
3. Always get the user's approval before applying a revision, as step 9 describes.
4. Once approved, apply it: create new slices and tasks, edit existing ones, and record dependencies with the `{{tool "dependency_add"}}` tool. Cancel what is dropped with the `{{tool "task_set_state"}}` tool, giving a reason, and never delete. If a task that is already ready to work on (TODO) changed in scope, move it back to PROPOSED with a kickback reason so it is worked out again.
5. Finish as in step 11 above.

If any tool result contains a line starting `Notice for the user:`, relay it to the user.

This command does not work out task details, write acceptance criteria, promote any task, or write code.
