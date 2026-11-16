package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	planningRequired    = "Outline approval before creation: required (setting plan.confirm_outline). Revisions of an existing outline always need approval."
	planningNotRequired = "Outline approval before creation: not required (setting plan.confirm_outline). Revisions of an existing outline always need approval."
)

func planningServer(t *testing.T, confirm bool) *mcp.ClientSession {
	t.Helper()
	return connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t)), Planning: PlanningSettings{ConfirmOutline: confirm}})
}

func callPlanning(t *testing.T, cs *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "planning_settings"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("error result: %v", res.Content)
	}
	return res
}

func checkPlanning(t *testing.T, confirm bool, text string) {
	t.Helper()
	res := callPlanning(t, planningServer(t, confirm))
	raw, _ := json.Marshal(res.StructuredContent)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["confirm_outline"] != confirm || got["revisions_always_ask"] != true {
		t.Errorf("structured = %s", raw)
	}
	if g := lastText(t, res); g != text {
		t.Errorf("text = %q, want %q", g, text)
	}
}

func findTool(t *testing.T, cs *mcp.ClientSession, name string) *mcp.Tool {
	t.Helper()
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not listed", name)
	return nil
}

func TestPlanningSettingsToolListed(t *testing.T) {
	tool := findTool(t, planningServer(t, false), "planning_settings")
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("annotations = %+v", tool.Annotations)
	}
	raw, _ := json.Marshal(tool.InputSchema)
	var in struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Required) != 0 {
		t.Errorf("required arguments = %v", in.Required)
	}
}

func TestPlanningSettingsFalse(t *testing.T) { checkPlanning(t, false, planningNotRequired) }

func TestPlanningSettingsTrue(t *testing.T) { checkPlanning(t, true, planningRequired) }

func TestPlanningSettingsDescriptionMentionsReceive(t *testing.T) {
	d := findTool(t, planningServer(t, false), "planning_settings").Description
	if !strings.Contains(d, "receive flow") || !strings.Contains(d, "how planning is configured") {
		t.Errorf("description = %q", d)
	}
}

func TestPlanningSettingsHasNotices(t *testing.T) {
	structuredNotices(t, callPlanning(t, planningServer(t, false)))
	tool := findTool(t, planningServer(t, false), "planning_settings")
	raw, _ := json.Marshal(tool.OutputSchema)
	if !strings.Contains(string(raw), `"notices"`) {
		t.Errorf("output schema lacks notices: %s", raw)
	}
}

func TestPlanningSettingsWithoutOptionDefaultsToFalse(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t))})
	if g := lastText(t, callPlanning(t, cs)); g != planningNotRequired {
		t.Errorf("text = %q", g)
	}
}
