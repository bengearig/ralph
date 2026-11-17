package embed

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/embed/ollama"
)

const (
	noPrefix       = "none"
	nomicModel     = "nomic-embed-text"
	nomicDocPrefix = "search_document: "
	nomicQryPrefix = "search_query: "
)

// Settings are the embeddings.* configuration values.
type Settings struct {
	Provider       string
	URL            string
	Model          string
	DocumentPrefix string
	QueryPrefix    string
	ChunkChars     int
	Timeout        time.Duration
}

// FromConfig reads the settings and builds the provider they name; the provider is nil for none.
func FromConfig(cfg *config.Config) (Provider, Settings, error) {
	var s Settings
	get := func(path string) (string, error) {
		v, ok := cfg.Get(path)
		if !ok && !strings.HasSuffix(path, "_prefix") {
			return "", fmt.Errorf("setting %s has no value", path)
		}
		return v.Value, nil
	}
	for _, f := range []struct {
		path string
		dst  *string
	}{
		{"embeddings.provider", &s.Provider},
		{"embeddings.url", &s.URL},
		{"embeddings.model", &s.Model},
		{"embeddings.document_prefix", &s.DocumentPrefix},
		{"embeddings.query_prefix", &s.QueryPrefix},
	} {
		v, err := get(f.path)
		if err != nil {
			return nil, Settings{}, err
		}
		*f.dst = v
	}
	chars, err := get("embeddings.chunk_chars")
	if err != nil {
		return nil, Settings{}, err
	}
	if s.ChunkChars, err = strconv.Atoi(chars); err != nil {
		return nil, Settings{}, fmt.Errorf("embeddings.chunk_chars: %q is not a whole number", chars)
	}
	timeout, err := get("embeddings.timeout")
	if err != nil {
		return nil, Settings{}, err
	}
	if s.Timeout, err = time.ParseDuration(timeout); err != nil {
		return nil, Settings{}, fmt.Errorf("embeddings.timeout: %q is not a duration", timeout)
	}
	switch s.Provider {
	case "none":
		return nil, s, nil
	case "ollama":
		p, err := ollama.New(ollama.Config{URL: s.URL, Model: s.Model, Timeout: s.Timeout})
		if err != nil {
			return nil, Settings{}, err
		}
		return p, s, nil
	default:
		return nil, Settings{}, fmt.Errorf("embeddings.provider: unknown provider %q", s.Provider)
	}
}

// Prefixes returns the document and query prefixes: an empty setting means the model's built-in prefix,
// none means no prefix and anything else is used as written.
func Prefixes(s Settings) (document, query string) {
	builtinDoc, builtinQry := "", ""
	name, _, _ := strings.Cut(s.Model, ":")
	if name == nomicModel {
		builtinDoc, builtinQry = nomicDocPrefix, nomicQryPrefix
	}
	return resolvePrefix(s.DocumentPrefix, builtinDoc), resolvePrefix(s.QueryPrefix, builtinQry)
}

func resolvePrefix(setting, builtin string) string {
	switch setting {
	case "":
		return builtin
	case noPrefix:
		return ""
	}
	return setting
}
