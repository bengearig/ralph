package cli

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/headless"
)

func TestReadOnlyToolsAreReadOnly(t *testing.T) {
	newStatusEnv(t)
	cs := mcpSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, tool := range list.Tools {
		registered[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	}
	found := 0
	for _, name := range headless.ReadOnlyTools {
		readOnly, ok := registered[name]
		if !ok {
			continue
		}
		found++
		if !readOnly {
			t.Errorf("tool %s is allowed in restricted runs but is not annotated read-only", name)
		}
	}
	if found == 0 {
		t.Error("no tool of headless.ReadOnlyTools is registered")
	}
}
