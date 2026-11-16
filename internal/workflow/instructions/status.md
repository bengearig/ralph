---
description: "ralph status: summarize the work and suggest what to do next"
argument-hint: "[unit ID, for example B-1, S-2 or T-3]"
---
Summarize where the work stands in ralph and suggest what to do next.

1. If the user gave an ID after the command ({{arguments}}), call the `{{tool "unit_get"}}` tool with that ID. Otherwise call the `{{tool "status"}}` tool.
2. Report each brief, slice and task that matters:
   - blocked tasks, with the reason from their notes;
   - tasks still PROPOSED that need articulation;
   - slices whose tasks are all ready.
3. Report any unfinalized quick work (the `unfinalized` entries of the status result) with its branch, and suggest finishing it or cancelling it.
4. If any tool result contains a line starting `Notice for the user:`, mention it to the user.
5. Suggest the single most useful next step.

Change nothing: do not create, update or delete anything, and do not edit files.
