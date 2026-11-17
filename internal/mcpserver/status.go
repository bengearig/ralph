package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/view"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const statusDescription = `Summarize the project's work and how far along it is. Call this first to see what exists.

Work has three levels. A brief (ID like B-1) is a request or goal as the user stated it. A slice (S-1) is one independently deliverable part of a brief. A task (T-1) is one concrete step, small enough for a single working session, and is the only level that has a state of its own: PROPOSED, TODO, IN_PROGRESS, BLOCKED, DONE or CANCELLED. A brief's and a slice's status is derived from the tasks beneath it, with one more status: NEEDS_ATTENTION, for a slice whose latest pull request was closed without merging and has no decision yet (a brief with such a slice is NEEDS_ATTENTION too unless one is BLOCKED). A task with no slice is an orphan task; orphan tasks are listed separately under "tasks".

The result lists unfinished briefs with their slices and a count of tasks per state in each slice, then the orphan tasks. A slice that needs attention is not finished and is always listed. Finished work (DONE or CANCELLED) is left out unless "all" is true.`

type statusInput struct {
	All bool `json:"all,omitempty" jsonschema:"include finished (DONE or CANCELLED) work; default false"`
}

func (s *server) addStatusTool(srv *mcp.Server) {
	schema, err := jsonschema.For[view.SummaryView](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: status output schema: %v", err))
	}
	addTool(s, srv, &mcp.Tool{
		Name:         "status",
		Description:  statusDescription,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: schema,
	}, func(ctx context.Context, svc units.Service, in statusInput) (any, string, error) {
		sum, err := view.Summary(ctx, svc, in.All)
		if err != nil {
			return nil, "", err
		}
		return sum, summaryText(sum), nil
	})
}

func summaryText(v view.SummaryView) string {
	var b strings.Builder
	for _, br := range v.Briefs {
		fmt.Fprintf(&b, "%s %s %s\n", br.ID, br.Status, br.Title)
		for _, sl := range br.Slices {
			fmt.Fprintf(&b, "  %s %s %s\n", sl.ID, sl.Status, sl.Title)
		}
	}
	if len(v.Tasks) > 0 {
		b.WriteString("Orphan tasks (no slice):\n")
		for _, t := range v.Tasks {
			fmt.Fprintf(&b, "  %s %s %s\n", t.ID, t.Status, t.Title)
		}
	}
	if v.Hidden > 0 {
		fmt.Fprintf(&b, "%d finished item(s) hidden; pass all=true to include them\n", v.Hidden)
	}
	if b.Len() == 0 {
		return "No work recorded yet."
	}
	return strings.TrimRight(b.String(), "\n")
}
