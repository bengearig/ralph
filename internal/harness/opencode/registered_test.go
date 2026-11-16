package opencode_test

import (
	"reflect"
	"testing"

	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/harness/harnesstest"
	"github.com/bengearig/ralph/internal/harness/opencode"
)

func TestOpenCodeRegistered(t *testing.T) {
	a, ok := all.ByName("opencode")
	if !ok || a.Name() != "opencode" {
		t.Fatalf("ByName = %v, %v", a, ok)
	}
	if got, want := all.Names(), []string{"claude-code", "opencode"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}

func TestOpenCodeConformance(t *testing.T) {
	harnesstest.Check(t, opencode.New())
	harnesstest.CheckAll(t, all.Adapters())
}
