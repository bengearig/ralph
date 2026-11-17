package config

import (
	"strings"
	"testing"
)

var embeddingsPaths = []string{
	"embeddings.provider", "embeddings.url", "embeddings.model", "embeddings.document_prefix",
	"embeddings.query_prefix", "embeddings.chunk_chars", "embeddings.timeout",
}

func TestEmbeddingsSettings(t *testing.T) {
	r := DefaultRegistry()
	defaults := map[string]string{
		"embeddings.provider": "none", "embeddings.url": "http://localhost:11434", "embeddings.model": "nomic-embed-text",
		"embeddings.document_prefix": "", "embeddings.query_prefix": "", "embeddings.chunk_chars": "1200", "embeddings.timeout": "30s",
	}
	for _, path := range embeddingsPaths {
		s, ok := r.Lookup(path)
		if !ok || s.Default != defaults[path] || s.Scope != ScopeUser || s.Description == "" || !strings.Contains(s.Description, "embeddings.url") && path != "embeddings.url" && path != "embeddings.provider" && path != "embeddings.model" {
			t.Errorf("%s = %+v, %v", path, s, ok)
		}
	}
	check := func(path, v string, ok bool) {
		t.Helper()
		s, _ := r.Lookup(path)
		if p := valueProblem(s, v); (p == "") != ok {
			t.Errorf("%s=%q: problem %q, accepted want %v", path, v, p, ok)
		}
	}
	for v, ok := range map[string]bool{"none": true, "ollama": true, "openai": false} {
		check("embeddings.provider", v, ok)
	}
	for v, ok := range map[string]bool{
		"http://localhost:11434": true, "https://h.example/": true, "ftp://x": false, "localhost:11434": false,
		"http://u:p@h": false, "http://h/?q=1": false, "http://h/#f": false, "": false,
	} {
		check("embeddings.url", v, ok)
	}
	s, _ := r.Lookup("embeddings.url")
	if p := valueProblem(s, "http://u:p@h"); !strings.Contains(p, "embeddings.url must not contain credentials") || strings.Contains(p, ":p@") {
		t.Errorf("credentials problem = %q", p)
	}
	for v, ok := range map[string]bool{
		"nomic-embed-text": true, "nomic-embed-text:latest": true, "a b": false, "-m": false, "a\x00b": false,
		"": false, strings.Repeat("m", 200): true, strings.Repeat("m", 201): false,
	} {
		check("embeddings.model", v, ok)
	}
	for _, path := range []string{"embeddings.document_prefix", "embeddings.query_prefix"} {
		for v, ok := range map[string]bool{
			"": true, "none": true, "search_query: ": true, "a\nb": false, "a\rb": false,
			strings.Repeat("p", 200): true, strings.Repeat("p", 201): false,
		} {
			check(path, v, ok)
		}
	}
	for v, ok := range map[string]bool{"199": false, "200": true, "6000": true, "6001": false, "x": false, "": false} {
		check("embeddings.chunk_chars", v, ok)
	}
	for v, ok := range map[string]bool{"0": false, "-1s": false, "11m": false, "10m": true, "30s": true, "1ms": true, "x": false} {
		check("embeddings.timeout", v, ok)
	}
}

func TestEmbeddingsIgnoredInProjectFile(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	write(t, f.projFile, "embeddings:\n  provider: ollama\n  url: https://evil.example\n  model: evil\n  document_prefix: x\n  query_prefix: y\n  chunk_chars: 300\n  timeout: 1s\n")
	c := mustLoad(t, f.opts(nil, nil))
	got := map[string]bool{}
	for _, w := range c.Warnings() {
		got[w.Path] = true
	}
	for _, path := range embeddingsPaths {
		if !got[path] {
			t.Errorf("no warning for %s: %+v", path, c.Warnings())
		}
		s, _ := f.reg.Lookup(path)
		if v, _ := c.Get(path); v.Value != s.Default || v.Layer != LayerDefault {
			t.Errorf("%s = %+v, want the default", path, v)
		}
	}
}

func TestEmbeddingsFromEnvironment(t *testing.T) {
	f := newFixture(t)
	f.reg = DefaultRegistry()
	c := mustLoad(t, f.opts(map[string]string{"RALPH_EMBEDDINGS_PROVIDER": "ollama", "RALPH_EMBEDDINGS_TIMEOUT": "2m"}, nil))
	if v, _ := c.Get("embeddings.provider"); v.Value != "ollama" || v.Layer != LayerEnv {
		t.Errorf("provider = %+v", v)
	}
	if v, _ := c.Get("embeddings.timeout"); v.Value != "2m" {
		t.Errorf("timeout = %+v", v)
	}
}
