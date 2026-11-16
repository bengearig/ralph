package mcpserver

import (
	"context"
	"fmt"

	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const planningSettingsDescription = `Report how planning is configured. An agent running the receive flow calls this before creating anything.

confirm_outline says whether a new outline must be shown and approved before it is created (setting plan.confirm_outline). revisions_always_ask is always true: a revision of an existing outline needs approval whatever the setting says. The values are fixed when the server starts.`

// PlanningSettings is how planning is configured for this server.
type PlanningSettings struct {
	ConfirmOutline bool
}

type planningSettingsInput struct{}

type planningSettingsResult struct {
	ConfirmOutline     bool `json:"confirm_outline" jsonschema:"whether a new outline needs approval before it is created"`
	RevisionsAlwaysAsk bool `json:"revisions_always_ask" jsonschema:"always true: a revision of an existing outline needs approval"`
}

func planningText(confirm bool) string {
	state := "not required"
	if confirm {
		state = "required"
	}
	return fmt.Sprintf("Outline approval before creation: %s (setting plan.confirm_outline). Revisions of an existing outline always need approval.", state)
}

func (s *server) addPlanningTool(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:         "planning_settings",
		Description:  planningSettingsDescription,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  schemaFor[planningSettingsInput](nil),
		OutputSchema: outputFor[planningSettingsResult](),
	}, func(_ context.Context, _ units.Service, _ planningSettingsInput) (any, string, error) {
		confirm := s.opts.Planning.ConfirmOutline
		return planningSettingsResult{ConfirmOutline: confirm, RevisionsAlwaysAsk: true}, planningText(confirm), nil
	})
}
