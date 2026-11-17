package ollama_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/ollama"
)

func TestLiveOllama(t *testing.T) {
	if os.Getenv("RALPH_OLLAMA_LIVE") != "1" {
		t.Skip("set RALPH_OLLAMA_LIVE=1 to run against a real Ollama (RALPH_OLLAMA_URL, RALPH_OLLAMA_MODEL)")
	}
	url := envOr("RALPH_OLLAMA_URL", "http://localhost:11434")
	model := envOr("RALPH_OLLAMA_MODEL", "nomic-embed-text")
	ctx := context.Background()
	p, err := ollama.New(ollama.Config{URL: url, Model: model, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	info, err := p.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Digest == "" || info.Dimensions <= 0 || info.ContextTokens <= 0 {
		t.Errorf("info = %+v", info)
	}
	vecs, err := p.Embed(ctx, []string{
		"search_document: The cat sat on the warm windowsill.",
		"search_document: A cat is resting on a sunny window ledge.",
		"search_document: Quarterly tax filings are due next month.",
	})
	if err != nil || len(vecs) != 3 {
		t.Fatalf("Embed = %v, %v", vecs, err)
	}
	dot := func(a, b []float32) float32 {
		var s float32
		for i := range a {
			s += a[i] * b[i]
		}
		return s
	}
	if len(vecs[0]) != info.Dimensions {
		t.Errorf("length %d, want %d", len(vecs[0]), info.Dimensions)
	}
	if d := dot(vecs[0], vecs[0]); d < 0.99 || d > 1.01 {
		t.Errorf("vector length squared = %v", d)
	}
	if related, unrelated := dot(vecs[0], vecs[1]), dot(vecs[0], vecs[2]); related <= unrelated {
		t.Errorf("related %v <= unrelated %v", related, unrelated)
	}
	missing, err := ollama.New(ollama.Config{URL: url, Model: "ralph-no-such-model", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Embed(ctx, []string{"x"}); !errors.Is(err, embed.ErrModelNotFound) {
		t.Errorf("unknown model err = %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
