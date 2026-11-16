package search

import (
	"encoding/json"
	"testing"
)

func TestResultJSONShape(t *testing.T) {
	full, err := json.Marshal(Result{Mode: ModeKeyword, Relaxed: true, Hits: []Hit{{
		Kind: KindTask, ID: "T-1", Title: "t", Snippet: "[x]", Status: "TODO", Score: 1.5, Unit: "T-2", Inactive: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(full, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"mode", "relaxed", "hits"} {
		if _, ok := m[k]; !ok {
			t.Errorf("result lacks %q: %s", k, full)
		}
	}
	hit := m["hits"].([]any)[0].(map[string]any)
	for _, k := range []string{"kind", "id", "title", "snippet", "status", "score", "unit", "inactive"} {
		if _, ok := hit[k]; !ok {
			t.Errorf("hit lacks %q: %s", k, full)
		}
	}

	bare, err := json.Marshal(Hit{Kind: KindBrief, ID: "B-1", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	hit = nil
	if err := json.Unmarshal(bare, &hit); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"status", "unit", "snippet"} {
		if _, ok := hit[k]; ok {
			t.Errorf("empty %q not omitted: %s", k, bare)
		}
	}
	for _, k := range []string{"kind", "id", "title", "score", "inactive"} {
		if _, ok := hit[k]; !ok {
			t.Errorf("hit lacks %q: %s", k, bare)
		}
	}
}
