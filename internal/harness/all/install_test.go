package all

import (
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/harnesstest"
)

func TestCheckInstallClaudeCode(t *testing.T) { checkInstallOne(t, "claude-code") }

func TestCheckInstallOpenCode(t *testing.T) { checkInstallOne(t, "opencode") }

func TestCheckInstallAllAdaptersBothOrders(t *testing.T) {
	harnesstest.CheckInstall(t, Adapters())
}

func checkInstallOne(t *testing.T, name string) {
	t.Helper()
	a, ok := ByName(name)
	if !ok {
		t.Fatalf("no adapter %q", name)
	}
	harnesstest.CheckInstall(t, []harness.Adapter{a})
}
