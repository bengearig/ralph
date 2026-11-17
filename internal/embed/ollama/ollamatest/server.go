// Package ollamatest is a stub Ollama server for tests.
package ollamatest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/embed/embedtest"
)

const (
	defaultModel = "nomic-embed-text:latest"
	defaultDims  = 64
	defaultCtx   = 2048
)

// Options configures a Server; the zero value serves nomic-embed-text with 64 dimensions.
type Options struct {
	// Models are the installed model names; nil means nomic-embed-text:latest.
	Models []string
	// Dimensions is the length of the vectors and of embedding_length.
	Dimensions int
	// ContextTokens is the context_length reported by /api/show.
	ContextTokens int
	// MaxInputChars, if positive, makes /api/embed answer 400 for a longer input.
	MaxInputChars int
}

// Request is one request the server received.
type Request struct {
	Method string
	Path   string
	Body   map[string]any
}

// Server speaks /api/embed, /api/tags and /api/show.
type Server struct {
	srv  *httptest.Server
	opts Options

	mu       sync.Mutex
	models   map[string]string
	requests []Request
	status   int
}

// NewServer starts a server that is closed when the test ends.
func NewServer(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.Dimensions <= 0 {
		opts.Dimensions = defaultDims
	}
	if opts.ContextTokens <= 0 {
		opts.ContextTokens = defaultCtx
	}
	s := &Server{opts: opts, models: map[string]string{}}
	models := opts.Models
	if models == nil {
		models = []string{defaultModel}
	}
	for _, m := range models {
		s.AddModel(m, "")
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// URL is the address of the server.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server, so later connections are refused.
func (s *Server) Close() { s.srv.Close() }

// AddModel installs a model; a name with no tag means :latest and an empty digest gets one derived from the name.
func (s *Server) AddModel(name, digest string) {
	name = withTag(name)
	if digest == "" {
		digest = "sha256:" + strings.NewReplacer(":", "-", "/", "-").Replace(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models[name] = digest
}

// RemoveModel uninstalls a model.
func (s *Server) RemoveModel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.models, withTag(name))
}

// FailWith makes every request answer with status until it is called with 0.
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// Requests returns the requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func withTag(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return name + ":latest"
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Body: body})
	status := s.status
	digest, installed := "", false
	if m, ok := body["model"].(string); ok {
		digest, installed = s.models[withTag(m)]
	}
	listed := make(map[string]string, len(s.models))
	for k, v := range s.models {
		listed[k] = v
	}
	s.mu.Unlock()

	if status != 0 {
		writeJSON(w, status, map[string]string{"error": "stub failure"})
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/embed":
		s.embed(w, body, installed)
	case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
		var models []map[string]any
		for name, d := range listed {
			models = append(models, map[string]any{"name": name, "model": name, "digest": d})
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
	case r.Method == http.MethodPost && r.URL.Path == "/api/show":
		if !installed {
			notFound(w, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"model_info": map[string]any{
			"general.architecture":        "nomic-bert",
			"nomic-bert.embedding_length": s.opts.Dimensions,
			"nomic-bert.context_length":   s.opts.ContextTokens,
		}, "digest": digest})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) embed(w http.ResponseWriter, body map[string]any, installed bool) {
	if !installed {
		notFound(w, body)
		return
	}
	input, _ := body["input"].([]any)
	vectors := make([][]float32, 0, len(input))
	for _, in := range input {
		text, _ := in.(string)
		if s.opts.MaxInputChars > 0 && len(text) > s.opts.MaxInputChars {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the input length exceeds the context length"})
			return
		}
		vectors = append(vectors, embedtest.Vector(text, s.opts.Dimensions))
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": body["model"], "embeddings": vectors})
}

func notFound(w http.ResponseWriter, body map[string]any) {
	m, _ := body["model"].(string)
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "model '" + m + "' not found"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
