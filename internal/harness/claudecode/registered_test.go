package claudecode_test

import (
	"testing"

	"github.com/bengearig/ralph/internal/harness/all"
)

func TestClaudeCodeRegistered(t *testing.T) {
	a, ok := all.ByName("claude-code")
	if !ok || a.Name() != "claude-code" {
		t.Fatalf("ByName = %v, %v", a, ok)
	}
}
