package opencode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReceiveCommandUsesNeutralApproval(t *testing.T) {
	receive, ok, err := workflow.Get("receive")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	c := oneCommand(t, receive, "ralph")
	for _, want := range []string{
		"Show the outline to the user in this conversation and wait. Create or apply nothing until the user approves it. If they reject it with feedback, revise the outline and show it again. Create or apply it only once they approve.",
		"planning_settings",
		"$ARGUMENTS",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("content lacks %q", want)
		}
	}
	for _, bad := range []string{"EnterPlanMode", "ExitPlanMode", "plan mode", "mcp__", "{{"} {
		if strings.Contains(c, bad) {
			t.Errorf("content contains %q", bad)
		}
	}
}

type stubQuick struct{}

func (stubQuick) Start(context.Context, quick.StartInput) (quick.Started, error) {
	return quick.Started{}, nil
}

func (stubQuick) Finish(context.Context, quick.FinishInput) (quick.Finished, error) {
	return quick.Finished{}, nil
}

func TestToolNamesExistOnTheServerOpenCode(t *testing.T) {
	ctx := context.Background()
	srv := mcpserver.New(mcpserver.Options{
		Name:    "ralph",
		Version: "test",
		Open:    func(context.Context) (units.Service, error) { return nil, nil },
		Quick:   func(context.Context, units.Service) (mcpserver.Quick, error) { return stubQuick{}, nil },
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range res.Tools {
		have[tool.Name] = true
	}
	for _, ins := range realInputs(t).Instructions {
		for _, tool := range ins.Tools() {
			if !have[tool] {
				t.Errorf("instruction %s references tool %q, which the server does not register", ins.Name, tool)
			}
		}
	}
}

func TestArticulateCommandUsesBareNames(t *testing.T) {
	articulate, ok, err := workflow.Get("articulate")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	c := oneCommand(t, articulate, "ralph")
	for _, want := range []string{"unit_update", "task_set_state", "$ARGUMENTS"} {
		if !strings.Contains(c, want) {
			t.Errorf("content lacks %q", want)
		}
	}
	for _, bad := range []string{"mcp__", "{{"} {
		if strings.Contains(c, bad) {
			t.Errorf("content contains %q", bad)
		}
	}
}
