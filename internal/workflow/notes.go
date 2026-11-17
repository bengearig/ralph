package workflow

import (
	"fmt"
	"strings"
)

// ProjectNotes is the text every session in the project reads. It names tools
// by their bare names and is the same for every harness.
func ProjectNotes(instructions []Instruction) string {
	var b strings.Builder
	para := func(format string, args ...any) {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, format, args...)
	}

	para("This project is planned with ralph. The plan lives in ralph, reachable through ralph's tools, not in files in this repository.")
	para("A ralph tool is a tool from the MCP server named ralph. Use those tools to read and change work, starting with `status` for an overview and `unit_get` to read one item. Every update must pass the fingerprint from the latest read of that item; if it has changed since, read it again and reapply your change.")
	para("Work has three levels:\n- A brief is a request or goal as the user stated it.\n- A slice is one independently deliverable part of a brief.\n- A task is one concrete step; a task with no slice is an orphan task.")
	para("Documents hold architecture, design, decisions and reference material. `search` finds work and documents, and the `doc_` tools read and change documents. `link_add` with kind `document` gives a brief, slice or task the documents it needs, which also reach the units below it.")
	para("If a tool result contains a line beginning `Notice for the user:`, relay that line to the user. A notice can ask for a decision: relay it, ask the user, and do nothing until they answer. Ralph can mirror units to Jira tickets and keeps ralph's values; if a notice says tickets differ or failed, tell the user and let them choose between `ralph tickets accept` and `ralph tickets overwrite`.")
	para("Do not edit anything under `.ralph` by hand; change the plan only through ralph's tools.")

	if len(instructions) > 0 {
		cmds := make([]string, len(instructions))
		for i, in := range instructions {
			cmds[i] = "`/ralph:" + in.Name + "` (`/ralph/" + in.Name + "` in OpenCode)"
		}
		para("Ralph's workflow commands are written `/ralph:<name>` in Claude Code and `/ralph/<name>` in OpenCode: %s.", strings.Join(cmds, ", "))
	}
	return b.String()
}
