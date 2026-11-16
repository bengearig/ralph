package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/testutil"
)

type providerEnv struct {
	repo  *testutil.Repo
	app   *app
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
	ua    string
	auth  string
}

func newProviderEnv(t *testing.T, remoteURL, projectYAML, userYAML string) *providerEnv {
	t.Helper()
	e := &providerEnv{repo: testutil.NewRepo(t)}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.paths = append(e.paths, r.URL.Path)
		e.ua, e.auth = r.UserAgent(), r.Header.Get("Authorization")
		e.mu.Unlock()
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(e.srv.Close)
	if remoteURL != "" {
		e.repo.Git("remote", "add", "origin", remoteURL)
	}
	if projectYAML != "" {
		e.repo.WriteFile(".ralph/project.yaml", projectYAML)
	}
	userFile := filepath.Join(t.TempDir(), "user.yaml")
	user := "github:\n  api_url: " + e.srv.URL + "\n" + userYAML
	if err := os.WriteFile(userFile, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    userFile,
		ProjectRoot: e.repo.Dir,
		Getenv: func(k string) (string, bool) {
			if k == "GITHUB_TOKEN" {
				return "tok", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.app = &app{root: e.repo.Dir, cfg: cfg}
	return e
}

func (e *providerEnv) requestedPath(t *testing.T) string {
	t.Helper()
	p, err := openProvider(context.Background(), e.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.FindPullRequest(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.paths) != 1 {
		t.Fatalf("paths = %v", e.paths)
	}
	return e.paths[0]
}

func TestOpenProviderFromRemote(t *testing.T) {
	e := newProviderEnv(t, "git@github.com:acme/widgets.git", "", "")
	if got := e.requestedPath(t); got != "/repos/acme/widgets/pulls" {
		t.Fatalf("path = %q", got)
	}
	if e.auth != "Bearer tok" || !strings.HasPrefix(e.ua, "ralph/") {
		t.Fatalf("auth = %q, ua = %q", e.auth, e.ua)
	}
}

func TestOpenProviderSettingsOverride(t *testing.T) {
	e := newProviderEnv(t, "git@github.com:acme/widgets.git", "github:\n  owner: other\n  repo: gadgets\n", "")
	if got := e.requestedPath(t); got != "/repos/other/gadgets/pulls" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderSettingsOverrideWithoutRemote(t *testing.T) {
	e := newProviderEnv(t, "", "github:\n  owner: other\n  repo: gadgets\n", "")
	if got := e.requestedPath(t); got != "/repos/other/gadgets/pulls" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderNonGitHubHost(t *testing.T) {
	e := newProviderEnv(t, "git@git.example.com:acme/widgets.git", "", "")
	e.app.cfg = mustConfig(t, e.repo.Dir, "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || !strings.Contains(err.Error(), "github.api_url") || !strings.Contains(err.Error(), "git.example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderNonGitHubHostWithAPIURL(t *testing.T) {
	e := newProviderEnv(t, "git@git.example.com:acme/widgets.git", "", "")
	if got := e.requestedPath(t); got != "/repos/acme/widgets/pulls" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderOutsideRepository(t *testing.T) {
	e := newProviderEnv(t, "git@github.com:acme/widgets.git", "", "")
	e.app.root = ""
	_, err := openProvider(context.Background(), e.app)
	if err == nil || err.Error() != "not in a git repository" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderNoRemote(t *testing.T) {
	e := newProviderEnv(t, "", "", "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || !strings.Contains(err.Error(), "github.owner") {
		t.Fatalf("err = %v", err)
	}
}

func mustConfig(t *testing.T, root, userYAML string) *config.Config {
	t.Helper()
	userFile := filepath.Join(t.TempDir(), "user.yaml")
	if err := os.WriteFile(userFile, []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    userFile,
		ProjectRoot: root,
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const unknownHostText = `cannot tell which pull request provider remote "origin" (git.example.com) uses; set pr.provider to github or bitbucket, and for GitHub Enterprise set github.api_url`

func TestOpenProviderAutoFromGitHubRemote(t *testing.T) {
	e := newProviderEnv(t, "https://github.com/acme/widgets.git", "", "")
	e.app.cfg = mustConfig(t, e.repo.Dir, "")
	p, err := openProvider(context.Background(), e.app)
	if err != nil || p == nil {
		t.Fatalf("p = %v, err = %v", p, err)
	}
}

func TestOpenProviderAutoLocalPathRemoteWithOwnerRepo(t *testing.T) {
	e := newProviderEnv(t, t.TempDir(), "github:\n  owner: other\n  repo: gadgets\n", "")
	if got := e.requestedPath(t); got != "/repos/other/gadgets/pulls" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderAutoGitHubEnterpriseByAPIURL(t *testing.T) {
	e := newProviderEnv(t, "git@git.example.com:acme/widgets.git", "", "")
	if got := e.requestedPath(t); got != "/repos/acme/widgets/pulls" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderAutoUnknownHostErrorText(t *testing.T) {
	e := newProviderEnv(t, "git@git.example.com:acme/widgets.git", "", "")
	e.app.cfg = mustConfig(t, e.repo.Dir, "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || err.Error() != unknownHostText {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "github.api_url") || !strings.Contains(err.Error(), "pr.provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderAutoNoRemoteNoSettings(t *testing.T) {
	e := newProviderEnv(t, "", "", "")
	e.app.cfg = mustConfig(t, e.repo.Dir, "")
	_, err := openProvider(context.Background(), e.app)
	want := "cannot tell which pull request provider; set pr.provider to github or bitbucket, and for GitHub Enterprise set github.api_url"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderExplicitGitHubUnchanged(t *testing.T) {
	e := newProviderEnv(t, "git@git.example.com:acme/widgets.git", "pr:\n  provider: github\n", "")
	if got := e.requestedPath(t); got != "/repos/acme/widgets/pulls" {
		t.Fatalf("path = %q", got)
	}
	e.app.cfg = mustConfig(t, e.repo.Dir, "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || !strings.Contains(err.Error(), "which is not GitHub") {
		t.Fatalf("err = %v", err)
	}
}
