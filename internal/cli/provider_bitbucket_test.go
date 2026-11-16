package cli

import (
	"context"
	"encoding/base64"
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

type bitbucketEnv struct {
	app  *app
	mu   sync.Mutex
	path string
	ua   string
	auth string
}

func newBitbucketEnv(t *testing.T, remoteURL, projectYAML, userYAML string) *bitbucketEnv {
	t.Helper()
	e := &bitbucketEnv{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.path, e.ua, e.auth = r.URL.Path, r.UserAgent(), r.Header.Get("Authorization")
		e.mu.Unlock()
		_, _ = w.Write([]byte(`{"values":[]}`))
	}))
	t.Cleanup(srv.Close)
	repo := testutil.NewRepo(t)
	if remoteURL != "" {
		repo.Git("remote", "add", "origin", remoteURL)
	}
	if projectYAML != "" {
		repo.WriteFile(".ralph/project.yaml", projectYAML)
	}
	userFile := filepath.Join(t.TempDir(), "user.yaml")
	user := "bitbucket:\n  api_url: " + srv.URL + "\n" + userYAML
	if err := os.WriteFile(userFile, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    userFile,
		ProjectRoot: repo.Dir,
		Getenv: func(k string) (string, bool) {
			if k == "BITBUCKET_TOKEN" {
				return "tok", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.app = &app{root: repo.Dir, cfg: cfg}
	return e
}

func (e *bitbucketEnv) requestedPath(t *testing.T) string {
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
	return e.path
}

func TestOpenProviderBitbucketFromRemote(t *testing.T) {
	e := newBitbucketEnv(t, "git@bitbucket.org:acme/widgets.git", "", "")
	if got := e.requestedPath(t); got != "/repositories/acme/widgets/pullrequests" {
		t.Fatalf("path = %q", got)
	}
	if e.auth != "Bearer tok" || !strings.HasPrefix(e.ua, "ralph/") {
		t.Fatalf("auth = %q, ua = %q", e.auth, e.ua)
	}
}

func TestOpenProviderBitbucketSettingsOverride(t *testing.T) {
	e := newBitbucketEnv(t, "git@bitbucket.org:acme/widgets.git", "pr:\n  provider: bitbucket\nbitbucket:\n  workspace: other\n  repo: gadgets\n", "")
	if got := e.requestedPath(t); got != "/repositories/other/gadgets/pullrequests" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderBitbucketNonCloudHost(t *testing.T) {
	e := newBitbucketEnv(t, "git@git.example.com:acme/widgets.git", "pr:\n  provider: bitbucket\n", "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || !strings.Contains(err.Error(), "bitbucket.workspace") || !strings.Contains(err.Error(), "not Bitbucket Cloud") || !strings.Contains(err.Error(), "git.example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderAutoBitbucketSettingsPair(t *testing.T) {
	e := newBitbucketEnv(t, "git@git.example.com:acme/widgets.git", "bitbucket:\n  workspace: other\n  repo: gadgets\n", "")
	if got := e.requestedPath(t); got != "/repositories/other/gadgets/pullrequests" {
		t.Fatalf("path = %q", got)
	}
}

func TestOpenProviderAutoBothPairsAmbiguous(t *testing.T) {
	e := newBitbucketEnv(t, "git@git.example.com:acme/widgets.git", "bitbucket:\n  workspace: o\n  repo: r\ngithub:\n  owner: o\n  repo: r\n", "")
	_, err := openProvider(context.Background(), e.app)
	if err == nil || !strings.Contains(err.Error(), "pr.provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenProviderBitbucketUserFromSetting(t *testing.T) {
	e := newBitbucketEnv(t, "git@bitbucket.org:acme/widgets.git", "", "  user: me@example.com\n")
	e.requestedPath(t)
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me@example.com:tok"))
	if e.auth != want {
		t.Fatalf("auth = %q", e.auth)
	}
}
