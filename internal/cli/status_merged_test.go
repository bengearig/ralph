package cli

import (
	"strings"
	"testing"
)

func TestStatusDescriptionsMentionMergedAndInReview(t *testing.T) {
	code, stdout, stderr := run(t, "status", "--help")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	for _, word := range []string{"MERGED", "IN_REVIEW"} {
		if !strings.Contains(stdout, word) {
			t.Errorf("help lacks %q: %s", word, stdout)
		}
	}
}
