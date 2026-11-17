package ollamatest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
)

func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return resp.StatusCode, out
}

func TestOllamaTestServerSpeaksTheDocumentedShapes(t *testing.T) {
	s := ollamatest.NewServer(t, ollamatest.Options{Dimensions: 8, ContextTokens: 512})

	code, out := do(t, "POST", s.URL()+"/api/embed", `{"model":"nomic-embed-text","input":["a b","c"],"truncate":false}`)
	emb, _ := out["embeddings"].([]any)
	if code != 200 || len(emb) != 2 || len(emb[0].([]any)) != 8 || out["model"] != "nomic-embed-text" {
		t.Errorf("embed = %d %v", code, out)
	}
	code, out = do(t, "POST", s.URL()+"/api/embed", `{"model":"nomic-embed-text","input":[]}`)
	if emb, _ := out["embeddings"].([]any); code != 200 || len(emb) != 0 {
		t.Errorf("empty embed = %d %v", code, out)
	}

	code, out = do(t, "GET", s.URL()+"/api/tags", "")
	models, _ := out["models"].([]any)
	if code != 200 || len(models) != 1 {
		t.Fatalf("tags = %d %v", code, out)
	}
	m := models[0].(map[string]any)
	if m["name"] != "nomic-embed-text:latest" || m["model"] != "nomic-embed-text:latest" || m["digest"] == "" {
		t.Errorf("model = %v", m)
	}

	code, out = do(t, "POST", s.URL()+"/api/show", `{"model":"nomic-embed-text"}`)
	info, _ := out["model_info"].(map[string]any)
	if code != 200 || info["nomic-bert.embedding_length"] != float64(8) || info["nomic-bert.context_length"] != float64(512) {
		t.Errorf("show = %d %v", code, out)
	}

	s.RemoveModel("nomic-embed-text")
	code, out = do(t, "POST", s.URL()+"/api/embed", `{"model":"nomic-embed-text","input":["a"]}`)
	if code != 404 || out["error"] != "model 'nomic-embed-text' not found" {
		t.Errorf("missing = %d %v", code, out)
	}
	s.AddModel("nomic-embed-text", "")
	s.FailWith(500)
	if code, _ = do(t, "GET", s.URL()+"/api/tags", ""); code != 500 {
		t.Errorf("failing = %d", code)
	}
	s.FailWith(0)

	reqs := s.Requests()
	if len(reqs) != 6 || reqs[0].Method != "POST" || reqs[0].Path != "/api/embed" || reqs[0].Body["truncate"] != false {
		t.Errorf("requests = %+v", reqs)
	}

	s.Close()
	if _, err := http.Get(s.URL() + "/api/tags"); err == nil {
		t.Error("closed server accepted a connection")
	}
}
