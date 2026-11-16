package opencode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness/opencode"
	"github.com/bengearig/ralph/internal/integration"
)

type configSource struct {
	Type string         `json:"type"`
	Path string         `json:"path"`
	Info map[string]any `json:"info"`
}

// TestLiveOpenCode runs the real OpenCode, never a model, in a private home
// and with its own server.
func TestLiveOpenCode(t *testing.T) {
	if os.Getenv("RALPH_OPENCODE_LIVE") != "1" {
		t.Skip("set RALPH_OPENCODE_LIVE=1 to check against the real OpenCode")
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skipf("opencode is not on PATH: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(home, k))
	}

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	a := opencode.New()
	d := a.Detect(ctx, repo)
	if !d.Found || d.Version == "" || d.Unsupported != "" {
		t.Fatalf("Detect = %+v", d)
	}

	in, err := integration.InputsFor(repo)
	if err != nil {
		t.Fatal(err)
	}
	arts, err := a.Artifacts(in)
	if err != nil {
		t.Fatal(err)
	}
	results, err := integration.Apply(ctx, repo, a.Name(), arts, "live")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Action != integration.ActionInstalled {
			t.Errorf("%s was %s", r.Path, r.Action)
		}
	}

	cmd := exec.CommandContext(ctx, bin, "debug", "config", "--standalone")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("opencode debug config: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "Error") || strings.Contains(stderr.String(), "Error") {
		t.Errorf("output mentions an error:\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}

	var sources []configSource
	if err := json.Unmarshal(stdout.Bytes(), &sources); err != nil {
		t.Fatalf("decoding %q: %v", stdout.String(), err)
	}
	want := filepath.Join(repo, "opencode.json")
	for _, s := range sources {
		if s.Type != "document" || s.Path != want {
			continue
		}
		server, _ := lookup(s.Info, "mcp", "servers", "ralph").(map[string]any)
		if server == nil {
			t.Fatalf("%s lacks mcp.servers.ralph: %v", want, s.Info)
		}
		if server["type"] != "local" {
			t.Errorf("type = %v, want local", server["type"])
		}
		if got := server["command"]; !reflect.DeepEqual(got, []any{"ralph", "mcp"}) {
			t.Errorf("command = %v, want [ralph mcp]", got)
		}
		return
	}
	t.Fatalf("no document source for %s in %s", want, stdout.String())
}

func lookup(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}
