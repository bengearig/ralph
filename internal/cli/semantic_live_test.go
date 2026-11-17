package cli

import (
	"io"
	"os"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/search"
)

func liveEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestLiveSemanticSearch(t *testing.T) {
	if os.Getenv("RALPH_OLLAMA_LIVE") != "1" {
		t.Skip("set RALPH_OLLAMA_LIVE=1 to run against a real Ollama (RALPH_OLLAMA_URL, RALPH_OLLAMA_MODEL)")
	}
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	backoff := e2eOK(t, cs, "doc_create", map[string]any{
		"kind":  "design",
		"title": "Retry failed payments with exponential backoff",
		"body": "When the card processor rejects a charge, wait before trying again and make each wait longer than the last. " +
			"Add random jitter to every delay so that many clients do not retry at the same moment, and stop after a fixed number of attempts.",
	})["id"].(string)
	decoy := e2eOK(t, cs, "doc_create", map[string]any{
		"kind":  "reference",
		"title": "Brand colours",
		"body":  "The palette uses a deep teal, a warm orange and a pale grey. Headings are teal and buttons are orange.",
	})["id"].(string)
	_ = cs.Close()

	e.env = append(e.env,
		"RALPH_EMBEDDINGS_PROVIDER=ollama",
		"RALPH_EMBEDDINGS_URL="+liveEnv("RALPH_OLLAMA_URL", "http://localhost:11434"),
		"RALPH_EMBEDDINGS_MODEL="+liveEnv("RALPH_OLLAMA_MODEL", "nomic-embed-text"),
	)
	if r := e.run(t, "index"); r.code != 0 {
		t.Fatalf("index: exit %d: %s", r.code, r.stderr)
	}

	const query = "how do we handle a payment that keeps failing"
	res := e.searchJSON(t, query)
	if res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Fatalf("mode %q notice %q", res.Mode, res.Notice)
	}
	h, ok := findHit(res, backoff)
	if !ok || !slices.Contains(h.Sources, "semantic") {
		t.Fatalf("the backoff document is not found by meaning: %+v", res.Hits)
	}
	if i, j := hitIndex(res, backoff), hitIndex(res, decoy); j >= 0 && i > j {
		t.Errorf("the decoy ranks above the backoff document: %v", hitIDs(res))
	}

	e.env = append(e.env, "RALPH_EMBEDDINGS_PROVIDER=none")
	if kw := e.searchJSON(t, query); !kw.Relaxed {
		t.Errorf("keyword search found a record with every word of the query: %+v", kw.Hits)
	}
}
