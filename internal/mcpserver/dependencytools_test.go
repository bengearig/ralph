package mcpserver

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// twoSlices creates B-1 with slices S-1 and S-2, each holding one ready task.
func twoSlices(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	for _, title := range []string{"first", "second"} {
		s := create(t, cs, map[string]any{"level": "slice", "parent_id": "B-1", "title": title, "body": "b"})
		create(t, cs, map[string]any{"level": "task", "parent_id": s["id"], "title": "T " + title, "body": "b", "acceptance_criteria": "c"})
	}
}

func dependsOn(t *testing.T, res map[string]any) []string {
	t.Helper()
	var out []string
	deps, _ := res["depends_on"].([]any)
	for _, d := range deps {
		out = append(out, d.(map[string]any)["id"].(string))
	}
	return out
}

func TestDependencyToolsListed(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	for _, name := range []string{"dependency_add", "dependency_remove"} {
		tool, found := got[name]
		if !found {
			t.Fatalf("tool %s not listed", name)
		}
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is read-only", name)
		}
		req := schemaOf(t, tool).Required
		if !slices.Contains(req, "id") || !slices.Contains(req, "depends_on") {
			t.Errorf("%s required = %v", name, req)
		}
	}
}

func TestDependencyAddRemove(t *testing.T) {
	cs, _ := newClient(t)
	twoSlices(t, cs)
	args := map[string]any{"id": "S-2", "depends_on": "S-1"}

	got := ok(t, cs, "dependency_add", args)
	if got["id"] != "S-2" || !slices.Equal(dependsOn(t, got), []string{"S-1"}) {
		t.Errorf("after add = %v", got)
	}
	if w, _ := got["waiting_on"].([]any); len(w) != 1 || w[0] != "S-1" {
		t.Errorf("waiting_on = %v", got["waiting_on"])
	}
	if got := ok(t, cs, "dependency_add", args); !slices.Equal(dependsOn(t, got), []string{"S-1"}) {
		t.Errorf("adding twice = %v", got)
	}
	if got := ok(t, cs, "unit_get", map[string]any{"id": "S-2"}); !slices.Equal(dependsOn(t, got), []string{"S-1"}) {
		t.Errorf("unit_get = %v", got)
	}
	got = ok(t, cs, "dependency_remove", args)
	if len(dependsOn(t, got)) != 0 || got["waiting_on"] != nil {
		t.Errorf("after remove = %v", got)
	}
	ok(t, cs, "dependency_remove", args)
}

func TestDependencyAddCycleInvalid(t *testing.T) {
	cs, _ := newClient(t)
	twoSlices(t, cs)
	ok(t, cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	res := fails(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "S-2"}, "invalid")
	if !strings.Contains(text(res), "cycle") || !strings.Contains(text(res), "S-1 -> S-2") {
		t.Errorf("text = %q", text(res))
	}
}

func TestDependencyAddNotSlicesInvalid(t *testing.T) {
	cs, _ := newClient(t)
	twoSlices(t, cs)
	fails(t, cs, "dependency_add", map[string]any{"id": "T-1", "depends_on": "S-1"}, "invalid")
	fails(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "B-1"}, "invalid")
	fails(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "S-1"}, "invalid")
	fails(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "nope"}, "invalid")
	fails(t, cs, "dependency_add", map[string]any{"id": "S-1", "depends_on": "S-9"}, "not_found")
}

func TestDependencyAddNeedsNoFingerprint(t *testing.T) {
	cs, _ := newClient(t)
	for _, name := range []string{"dependency_add", "dependency_remove"} {
		if _, has := schemaOf(t, tools(t, cs)[name]).Properties["fingerprint"]; has {
			t.Errorf("%s has a fingerprint property", name)
		}
	}
}

func TestDependencyDescriptionsMentionSatisfied(t *testing.T) {
	cs, _ := newClient(t)
	got := tools(t, cs)
	for _, name := range []string{"dependency_add", "dependency_remove"} {
		for _, want := range []string{"satisfied", "DONE", "different briefs", "cycle", "fingerprint"} {
			if !strings.Contains(got[name].Description, want) {
				t.Errorf("%s description lacks %q", name, want)
			}
		}
	}
	if d := got["task_set_state"].Description; !strings.Contains(d, "waiting") || !strings.Contains(d, "dependency_remove") {
		t.Errorf("task_set_state description = %q", d)
	}
	if d := got["unit_get"].Description; !strings.Contains(d, "depends_on") {
		t.Errorf("unit_get description = %q", d)
	}
}

func TestTaskSetStateWaitingCode(t *testing.T) {
	cs, _ := newClient(t)
	twoSlices(t, cs)
	ok(t, cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	tk := ok(t, cs, "unit_get", map[string]any{"id": "T-2"})
	todo := ok(t, cs, "task_set_state", map[string]any{"id": "T-2", "fingerprint": tk["fingerprint"], "state": "TODO"})
	res := fails(t, cs, "task_set_state", map[string]any{"id": "T-2", "fingerprint": todo["fingerprint"], "state": "IN_PROGRESS"}, "waiting")
	for _, want := range []string{"S-1", "dependency_remove"} {
		if !strings.Contains(text(res), want) {
			t.Errorf("text %q lacks %q", text(res), want)
		}
	}
	waiting, _ := res.StructuredContent.(map[string]any)["waiting_on"].([]any)
	if len(waiting) != 1 {
		t.Fatalf("waiting_on = %v", waiting)
	}
	w := waiting[0].(map[string]any)
	if w["id"] != "S-1" || w["title"] != "first" || w["status"] != "ARTICULATING" {
		t.Errorf("waiting_on[0] = %v", w)
	}
	ok(t, cs, "dependency_remove", map[string]any{"id": "S-2", "depends_on": "S-1"})
	ok(t, cs, "task_set_state", map[string]any{"id": "T-2", "fingerprint": todo["fingerprint"], "state": "IN_PROGRESS"})
}

func TestUnitGetSliceShowsDependencies(t *testing.T) {
	cs, _ := newClient(t)
	twoSlices(t, cs)
	if got := ok(t, cs, "unit_get", map[string]any{"id": "S-2"}); got["depends_on"] != nil || got["waiting_on"] != nil {
		t.Errorf("no dependencies yet = %v", got)
	}
	ok(t, cs, "dependency_add", map[string]any{"id": "S-2", "depends_on": "S-1"})
	got := ok(t, cs, "unit_get", map[string]any{"id": "S-2"})
	d := got["depends_on"].([]any)[0].(map[string]any)
	if d["id"] != "S-1" || d["title"] != "first" || d["satisfied"] != false {
		t.Errorf("depends_on = %v", d)
	}
	res := call(t, cs, "unit_get", map[string]any{"id": "S-2"})
	if !strings.Contains(text(res), "depends on: S-1") || !strings.Contains(text(res), "(waiting)") {
		t.Errorf("text = %q", text(res))
	}
}
