package all

import (
	"sort"
	"testing"

	"github.com/bengearig/ralph/internal/harness/harnesstest"
)

func TestAdaptersRegistry(t *testing.T) {
	names := Names()
	if !sort.StringsAreSorted(names) {
		t.Errorf("Names() not sorted: %v", names)
	}
	if len(names) != len(Adapters()) {
		t.Errorf("Names() has %d entries for %d adapters", len(names), len(Adapters()))
	}
	for _, n := range names {
		a, ok := ByName(n)
		if !ok || a.Name() != n {
			t.Errorf("ByName(%q) = %v, %v", n, a, ok)
		}
	}
	if a, ok := ByName("no-such-harness"); ok || a != nil {
		t.Errorf("ByName(unknown) = %v, %v; want not found", a, ok)
	}
}

func TestCheckAllOnRegisteredAdapters(t *testing.T) {
	harnesstest.CheckAll(t, Adapters())
}
