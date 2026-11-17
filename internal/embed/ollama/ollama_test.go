package ollama_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/embed/ollama"
	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
)

func newProvider(t *testing.T, url string, timeout time.Duration) *ollama.Provider {
	t.Helper()
	p, err := ollama.New(ollama.Config{URL: url, Model: "nomic-embed-text", Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func handlerServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func texts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("text number %d", i)
	}
	return out
}

func TestOllamaEmbedRequestShape(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	p := newProvider(t, srv.URL()+"/", time.Second)
	if _, err := p.Embed(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Method != "POST" || reqs[0].Path != "/api/embed" {
		t.Fatalf("requests = %+v", reqs)
	}
	body := reqs[0].Body
	if body["model"] != "nomic-embed-text" || body["truncate"] != false {
		t.Errorf("body = %v", body)
	}
	if in, _ := body["input"].([]any); len(in) != 2 || in[0] != "a" || in[1] != "b" {
		t.Errorf("input = %v", body["input"])
	}
}

func TestOllamaEmbedBatchesOfSixteen(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	p := newProvider(t, srv.URL(), time.Second)
	in := texts(40)
	got, err := p.Embed(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requests", len(reqs))
	}
	for i, want := range []int{16, 16, 8} {
		if n := len(reqs[i].Body["input"].([]any)); n != want {
			t.Errorf("batch %d has %d texts", i, n)
		}
	}
	if len(got) != 40 {
		t.Fatalf("%d vectors", len(got))
	}
	for i, text := range in {
		want := embedtest.Vector(text, 64)
		for j := range want {
			if math.Abs(float64(got[i][j]-want[j])) > 1e-5 {
				t.Fatalf("vector %d differs from its text's vector", i)
			}
		}
	}
}

func TestOllamaEmbedEmptyInputNoRequest(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	p := newProvider(t, srv.URL(), time.Second)
	got, err := p.Embed(context.Background(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestOllamaEmbedNormalizes(t *testing.T) {
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[3,4]]}`))
	})
	got, err := newProvider(t, s.URL, time.Second).Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(got[0][0])-0.6) > 1e-6 || math.Abs(float64(got[0][1])-0.8) > 1e-6 {
		t.Errorf("got %v", got[0])
	}
}

func TestOllamaEmbedModelNotFound(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{Models: []string{"other"}})
	_, err := newProvider(t, srv.URL(), time.Second).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrModelNotFound) {
		t.Fatalf("err = %v", err)
	}
	if want := "model nomic-embed-text is not installed in Ollama: run ollama pull nomic-embed-text"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("message = %q", err)
	}
}

func TestOllamaEmbedInputTooLongIsInvalid(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{MaxInputChars: 10})
	_, err := newProvider(t, srv.URL(), time.Second).Embed(context.Background(), []string{strings.Repeat("x", 11)})
	if !errors.Is(err, embed.ErrInvalid) || !strings.Contains(err.Error(), "exceeds the context length") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedServerErrorUnavailable(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	srv.FailWith(503)
	_, err := newProvider(t, srv.URL(), time.Second).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrUnavailable) || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedConnectionRefusedUnavailable(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	p := newProvider(t, srv.URL(), time.Second)
	srv.Close()
	_, err := p.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrUnavailable) || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedTimeout(t *testing.T) {
	release := make(chan struct{})
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	_, err := newProvider(t, s.URL, 50*time.Millisecond).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrUnavailable) || !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedCountMismatch(t *testing.T) {
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
	})
	_, err := newProvider(t, s.URL, time.Second).Embed(context.Background(), []string{"a", "b"})
	if !errors.Is(err, embed.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedInconsistentDimensions(t *testing.T) {
	for name, body := range map[string]string{
		"differ": `{"embeddings":[[1,0],[1,0,0]]}`,
		"empty":  `{"embeddings":[[]]}`,
		"zero":   `{"embeddings":[[0,0]]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			in := []string{"a", "b"}
			if name != "differ" {
				in = in[:1]
			}
			_, err := newProvider(t, s.URL, time.Second).Embed(context.Background(), in)
			if !errors.Is(err, embed.ErrInvalid) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestOllamaEmbedBadJSON(t *testing.T) {
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"embeddings":`)) })
	_, err := newProvider(t, s.URL, time.Second).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrUnavailable) || !strings.Contains(err.Error(), "unreadable response") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedResponseCap(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 64 MiB")
	}
	chunk := []byte(strings.Repeat(" ", 1<<20))
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 65; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	_, err := newProvider(t, s.URL, 30*time.Second).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrUnavailable) || !strings.Contains(err.Error(), "64 MiB") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaErrorsNeverContainCredentials(t *testing.T) {
	if _, err := ollama.New(ollama.Config{URL: "http://user:secret@localhost:11434", Model: "m"}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("New err = %v", err)
	}
	long := strings.Repeat("secret-token ", 100)
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, long)
	})
	_, err := newProvider(t, s.URL, time.Second).Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embed.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), s.URL) || len([]rune(err.Error())) > 400 {
		t.Errorf("message too revealing or long: %q", err)
	}
	p := newProvider(t, s.URL, time.Second)
	s.Close()
	_, err = p.Embed(context.Background(), []string{"x"})
	if err == nil || strings.Contains(err.Error(), s.URL) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaEmbedCancel(t *testing.T) {
	release := make(chan struct{})
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := newProvider(t, s.URL, time.Minute).Embed(ctx, []string{"x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaDescribeDigestAndDimensions(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{Dimensions: 768, ContextTokens: 2048})
	srv.AddModel("nomic-embed-text:latest", "sha256:abc")
	info, err := newProvider(t, srv.URL(), time.Second).Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := embed.Info{Provider: "ollama", Model: "nomic-embed-text", Digest: "sha256:abc", Dimensions: 768, ContextTokens: 2048}
	if info != want {
		t.Errorf("info = %+v", info)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 || reqs[0].Method != "GET" || reqs[0].Path != "/api/tags" || reqs[1].Method != "POST" || reqs[1].Path != "/api/show" || reqs[1].Body["model"] != "nomic-embed-text" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestOllamaDescribeLatestTagAssumed(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{Models: []string{"nomic-embed-text:v1.5"}})
	p := newProvider(t, srv.URL(), time.Second)
	if _, err := p.Describe(context.Background()); !errors.Is(err, embed.ErrModelNotFound) {
		t.Fatalf("untagged name matched a different tag: %v", err)
	}
	srv.AddModel("nomic-embed-text", "sha256:l")
	info, err := p.Describe(context.Background())
	if err != nil || info.Digest != "sha256:l" {
		t.Errorf("info = %+v, %v", info, err)
	}
	tagged, err := ollama.New(ollama.Config{URL: srv.URL(), Model: "nomic-embed-text:v1.5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tagged.Describe(context.Background()); err != nil {
		t.Errorf("tagged: %v", err)
	}
}

func TestOllamaDescribeMissingModel(t *testing.T) {
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	srv.RemoveModel("nomic-embed-text")
	_, err := newProvider(t, srv.URL(), time.Second).Describe(context.Background())
	if !errors.Is(err, embed.ErrModelNotFound) || !strings.Contains(err.Error(), "ollama pull nomic-embed-text") {
		t.Errorf("err = %v", err)
	}
}

func TestOllamaDescribeMissingModelInfoKeys(t *testing.T) {
	s := handlerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest","digest":"d"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"model_info":{"general.architecture":"x"}}`))
	})
	info, err := newProvider(t, s.URL, time.Second).Describe(context.Background())
	if err != nil || info.Dimensions != 0 || info.ContextTokens != 0 || info.Digest != "d" {
		t.Errorf("info = %+v, %v", info, err)
	}
}
