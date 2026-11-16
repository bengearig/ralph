package workflow

import (
	"fmt"
	"strings"
)

// ProjectNotes is the text every session in the project reads. Tool names are
// written through tool.
func ProjectNotes(instructions []Instruction, tool func(name string) string) string {
	var b strings.Builder
	para := func(format string, args ...any) {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, format, args...)
	}

	para("This project is planned with ralph. The plan lives in ralph, reachable through ralph's tools, not in files in this repository.")
	para("Use those tools to read and change work, starting with `%s` for an overview and `%s` to read one item. Every update must pass the fingerprint from the latest read of that item; if it has changed since, read it again and reapply your change.",
		tool("status"), tool("unit_get"))
	para("Work has three levels:\n- A brief is a request or goal as the user stated it.\n- A slice is one independently deliverable part of a brief.\n- A task is one concrete step; a task with no slice is an orphan task.")
	para("If a tool result contains a line beginning `Notice for the user:`, relay that line to the user.")
	para("Do not edit anything under `.ralph` by hand; change the plan only through ralph's tools.")

	if len(instructions) > 0 {
		cmds := make([]string, len(instructions))
		for i, in := range instructions {
			cmds[i] = "`/ralph:" + in.Name + "`"
		}
		para("Ralph's commands: %s.", strings.Join(cmds, ", "))
	}
	return b.String()
}
