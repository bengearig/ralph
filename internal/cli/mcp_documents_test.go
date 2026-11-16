package cli

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	a := loadedApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, a, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		if err := <-done; err != nil {
			t.Errorf("runMCP: %v", err)
		}
	})
	return cs
}

func mcpCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func mcpOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := mcpCall(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: error result: %v", name, res.Content)
	}
	m, _ := res.StructuredContent.(map[string]any)
	if m == nil {
		t.Fatalf("%s: structured content = %#v", name, res.StructuredContent)
	}
	return m
}

func TestMCPDocumentsServed(t *testing.T) {
	newStatusEnv(t)
	cs := mcpSession(t)

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range list.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"doc_create", "doc_get", "doc_list", "doc_update", "doc_archive", "doc_supersede", "doc_restore", "doc_versions"} {
		if !got[name] {
			t.Errorf("tool %s not listed", name)
		}
	}

	created := mcpOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Cache", "body": "Use a cache."})
	id, _ := created["id"].(string)
	if id != "D-1" || created["fingerprint"] == "" {
		t.Fatalf("created = %#v", created)
	}
	read := mcpOK(t, cs, "doc_get", map[string]any{"id": id})
	if read["title"] != "Cache" || read["body"] != "Use a cache." || read["fingerprint"] != created["fingerprint"] {
		t.Errorf("read = %#v", read)
	}
}

func TestMCPDocumentsShareDatabaseWithUnits(t *testing.T) {
	newStatusEnv(t)
	cs := mcpSession(t)

	brief := mcpOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "the brief", "body": "words"})
	briefID, _ := brief["id"].(string)
	doc := mcpOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick", "body": "We pick.", "brief_id": briefID})
	if doc["brief_id"] != briefID {
		t.Errorf("brief_id = %v, want %s", doc["brief_id"], briefID)
	}
	listed := mcpOK(t, cs, "doc_list", map[string]any{"brief_id": briefID})
	if docs, _ := listed["documents"].([]any); len(docs) != 1 {
		t.Errorf("listed = %#v", listed)
	}
	if got := mcpOK(t, cs, "unit_get", map[string]any{"id": briefID}); got["id"] != briefID {
		t.Errorf("unit_get = %#v", got)
	}
}

func TestMCPDocumentsNotInitialized(t *testing.T) {
	inRepo(t)
	cs := mcpSession(t)
	res := mcpCall(t, cs, "doc_list", nil)
	if !res.IsError {
		t.Fatalf("doc_list succeeded: %v", res.Content)
	}
	if code := e2eErrorCode(t, res); code != "not_initialized" {
		t.Errorf("error_code = %q, want not_initialized", code)
	}
}
