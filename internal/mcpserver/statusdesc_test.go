package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestStatusDescriptionsMentionMergedAndInReview(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(seeded(t))})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range res.Tools {
		if tool.Name != "status" {
			continue
		}
		found = true
		for _, word := range []string{"MERGED", "IN_REVIEW"} {
			if !strings.Contains(tool.Description, word) {
				t.Errorf("description lacks %q", word)
			}
		}
	}
	if !found {
		t.Fatal("no status tool")
	}
}
