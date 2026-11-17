package embed_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/embed"
)

func TestNormalizeLengthOne(t *testing.T) {
	in := []float32{3, 4, 0}
	got, err := embed.Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, x := range got {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) > 1e-6 || in[0] != 3 {
		t.Errorf("got %v (sum %v), input %v", got, sum, in)
	}
}

func TestNormalizeRejectsZeroNaNInf(t *testing.T) {
	for name, v := range map[string][]float32{
		"zero":  {0, 0},
		"empty": {},
		"nan":   {1, float32(math.NaN())},
		"inf":   {1, float32(math.Inf(1))},
		"-inf":  {float32(math.Inf(-1))},
	} {
		if _, err := embed.Normalize(v); !errors.Is(err, embed.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPrefixesBuiltInForNomic(t *testing.T) {
	for _, model := range []string{"nomic-embed-text", "nomic-embed-text:latest", "nomic-embed-text:v1.5"} {
		d, q := embed.Prefixes(embed.Settings{Model: model})
		if d != "search_document: " || q != "search_query: " {
			t.Errorf("%s: %q %q", model, d, q)
		}
	}
}

func TestPrefixesOtherModelNone(t *testing.T) {
	for _, model := range []string{"mxbai-embed-large", "nomic-embed-text-v2", "my/nomic-embed-text"} {
		if d, q := embed.Prefixes(embed.Settings{Model: model}); d != "" || q != "" {
			t.Errorf("%s: %q %q", model, d, q)
		}
	}
}

func TestPrefixesNoneKeywordAndExplicit(t *testing.T) {
	d, q := embed.Prefixes(embed.Settings{Model: "nomic-embed-text", DocumentPrefix: "none", QueryPrefix: "Q: "})
	if d != "" || q != "Q: " {
		t.Errorf("%q %q", d, q)
	}
	d, q = embed.Prefixes(embed.Settings{Model: "other", DocumentPrefix: "D: ", QueryPrefix: "none"})
	if d != "D: " || q != "" {
		t.Errorf("%q %q", d, q)
	}
}

func load(t *testing.T, userYAML string, env map[string]string) (*config.Config, error) {
	t.Helper()
	user := filepath.Join(t.TempDir(), "user.yaml")
	if err := os.WriteFile(user, []byte(userYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    user,
		ProjectRoot: t.TempDir(),
		Getenv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
	})
}

func TestFromConfigNone(t *testing.T) {
	cfg, err := load(t, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, s, err := embed.FromConfig(cfg)
	if err != nil || p != nil {
		t.Fatalf("provider = %v, err = %v", p, err)
	}
	want := embed.Settings{Provider: "none", URL: "http://localhost:11434", Model: "nomic-embed-text", ChunkChars: 1200, Timeout: 30 * time.Second}
	if s != want {
		t.Errorf("settings = %+v", s)
	}
}

func TestFromConfigOllama(t *testing.T) {
	cfg, err := load(t, "embeddings:\n  provider: ollama\n  url: http://127.0.0.1:9/\n  model: mxbai-embed-large\n  document_prefix: 'D: '\n  query_prefix: none\n  chunk_chars: 900\n  timeout: 5s\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, s, err := embed.FromConfig(cfg)
	if err != nil || p == nil {
		t.Fatalf("provider = %v, err = %v", p, err)
	}
	want := embed.Settings{Provider: "ollama", URL: "http://127.0.0.1:9/", Model: "mxbai-embed-large", DocumentPrefix: "D: ", QueryPrefix: "none", ChunkChars: 900, Timeout: 5 * time.Second}
	if s != want {
		t.Errorf("settings = %+v", s)
	}
}

func TestFromConfigErrors(t *testing.T) {
	if _, err := load(t, "embeddings:\n  provider: openai\n", nil); err == nil {
		t.Error("an unknown provider loaded")
	}
	cfg, err := load(t, "embeddings:\n  provider: ollama\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := embed.FromConfig(cfg); err != nil {
		t.Errorf("valid config: %v", err)
	}
	if _, _, err := embed.FromConfig(emptyConfig(t)); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("config without the settings: err = %v", err)
	}
}

func emptyConfig(t *testing.T) *config.Config {
	t.Helper()
	reg, err := config.NewRegistry(config.Setting{Path: "log.level", Default: "info"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Registry:    reg,
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: t.TempDir(),
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
