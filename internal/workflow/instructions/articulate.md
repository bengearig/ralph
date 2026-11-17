---
description: "ralph articulate: work out each task's details with the user and promote it"
argument-hint: "[a brief, slice or task ID]"
---
Work out the details of the planned tasks with the user, and promote each task once it is ready to be implemented by an agent with no other context.

1. Take the ID the user typed after the command ({{arguments}}) as the scope: a brief, a slice or a task. If nothing was typed, call the `{{tool "status"}}` tool and ask the user which brief, slice or task to articulate, then wait for the answer.
2. Resolve the scope with the `{{tool "unit_get"}}` tool. Read the brief, the notes recorded on it during receive (the clarifications the user gave), and the neighbouring slices, so you know what comes before and after. The result lists the documents linked to the scope (`documents`) and those inherited from its slice and brief (`inherited_documents`); read the ones that matter with the `{{tool "doc_get"}}` tool. A document shown as superseded or archived is old material: check it against its replacement before relying on it.
3. Take the PROPOSED tasks in scope one task at a time, in slice order and then position order. For each task:
   - Read the code it will touch.
   - Search with the `{{tool "search"}}` tool for prior decisions before asking the user a question the project may already have answered. Link further documents the task's details need to the task with the `{{tool "link_add"}}` tool (kind `document`, target the document's ID). A decision that matters beyond one task becomes a document (create it with the `{{tool "doc_create"}}` tool, tied to the brief, after searching) linked to each task it affects. A document tied to a brief is not linked to anything until a link is made.
   - Ask the user about the low-level details, a few questions at a time: the exact behavior, interfaces and data, edge cases and error handling, and which tests or commands will prove it works. Do not ask what the brief, its notes or the code already answer.
   - Write the result with the `{{tool "unit_update"}}` tool. The description must be one a stranger with no access to this conversation could implement from. The acceptance criteria must be verifiable, preferably by tests or commands. A criterion that runs tests names the narrowest command that proves the task. The whole suite is run only on a slice's last task or in its final check, and a task is never written so that it needs the whole suite run repeatedly.
   - As soon as the description and the acceptance criteria are both settled, promote the task by calling the `{{tool "task_set_state"}}` tool to set TODO with a short reason. Do this without asking for separate permission.
   - If the promotion is refused as not ready (the error has the code `not_ready` and a `missing` list), fill in what the error lists and promote the task again.
4. Within the slice, with the user's agreement, you may add tasks with the `{{tool "unit_create"}}` tool, split tasks, merge them, reorder them with `position` on the `{{tool "unit_update"}}` tool, or cancel them with the `{{tool "task_set_state"}}` tool, giving a reason. Each task must be small enough to finish in one iteration and must never depend on a later task. If a slice needs another slice merged first, record that with the `{{tool "dependency_add"}}` tool.
5. If a slice needs to change, or the instruction turns out to be unclear, stop and do not guess. Add a KICKBACK note with the `{{tool "note_add"}}` tool, using that kind, to the affected task when there is one. Explain the problem to the user, and suggest planning the brief again with the receive command and the brief's ID.
6. Leave TODO tasks alone unless the user wants to revisit one. If they do, move it back to PROPOSED with a kickback reason first.
7. Finish by calling the `{{tool "status"}}` tool. Summarize which tasks were promoted, which remain PROPOSED and why, and which slices are now ready.
8. If any tool result contains a line starting `Notice for the user:`, relay it to the user.

This command writes no production code.
