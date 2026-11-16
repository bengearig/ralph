package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const testToken = "ghp_SECRETtokenvalue123"

func staticToken(string) func() (string, error) {
	return func() (string, error) { return testToken, nil }
}

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(Config{APIURL: srv.URL, Owner: "o", Repo: "r", Token: staticToken(""), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newPR() hosting.NewPullRequest {
	return hosting.NewPullRequest{Head: "feat", Base: "main", Title: "T", Body: "B", Draft: true}
}

func TestNewGitHubValidation(t *testing.T) {
	tok := func() (string, error) { return "x", nil }
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"empty owner", Config{Repo: "r", Token: tok}, false},
		{"empty repo", Config{Owner: "o", Token: tok}, false},
		{"http non-loopback", Config{APIURL: "http://ghe.example.com", Owner: "o", Repo: "r", Token: tok}, false},
		{"no token function", Config{Owner: "o", Repo: "r"}, false},
		{"no scheme", Config{APIURL: "ghe.example.com", Owner: "o", Repo: "r", Token: tok}, false},
		{"http loopback ip", Config{APIURL: "http://127.0.0.1:1", Owner: "o", Repo: "r", Token: tok}, true},
		{"http localhost", Config{APIURL: "http://localhost:1", Owner: "o", Repo: "r", Token: tok}, true},
		{"http ipv6 loopback", Config{APIURL: "http://[::1]:1", Owner: "o", Repo: "r", Token: tok}, true},
		{"https enterprise", Config{APIURL: "https://ghe.example.com/api/v3/", Owner: "o", Repo: "r", Token: tok}, true},
		{"defaults", Config{Owner: "o", Repo: "r", Token: tok}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.cfg)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok %v", err, c.ok)
			}
		})
	}

	p, err := New(Config{Owner: "o", Repo: "r", Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	if p.apiURL != "https://api.github.com" || p.userAgent != "ralph" || p.client.Timeout != 30*time.Second {
		t.Errorf("defaults = %q %q %v", p.apiURL, p.userAgent, p.client.Timeout)
	}
	p, err = New(Config{APIURL: "https://ghe.example.com/api/v3/", Owner: "o", Repo: "r", Token: tok})
	if err != nil || p.apiURL != "https://ghe.example.com/api/v3" {
		t.Errorf("apiURL = %q, err = %v", p.apiURL, err)
	}
}

func TestGitHubCreateSendsRequest(t *testing.T) {
	var method, path, auth, accept, version, ua string
	var got map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, accept, version, ua = r.Header.Get("Authorization"), r.Header.Get("Accept"), r.Header.Get("X-GitHub-Api-Version"), r.UserAgent()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":1}`))
	})
	p.userAgent = "ralph/test"
	if _, err := p.CreatePullRequest(context.Background(), newPR()); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || path != "/repos/o/r/pulls" {
		t.Errorf("request = %s %s", method, path)
	}
	if auth != "Bearer "+testToken || accept != "application/vnd.github+json" || version != "2022-11-28" || ua != "ralph/test" {
		t.Errorf("headers = %q %q %q %q", auth, accept, version, ua)
	}
	want := map[string]any{"title": "T", "head": "feat", "base": "main", "body": "B", "draft": true}
	if len(got) != len(want) {
		t.Errorf("body = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestGitHubCreateReturnsPullRequest(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/o/r/pull/42","title":"T","body":"B","state":"open","draft":true,"head":{"ref":"feat"},"base":{"ref":"main"}}`))
	})
	pr, err := p.CreatePullRequest(context.Background(), newPR())
	if err != nil {
		t.Fatal(err)
	}
	want := hosting.PullRequest{Number: 42, URL: "https://github.com/o/r/pull/42", Title: "T", Body: "B", Head: "feat", Base: "main", Draft: true, State: hosting.StateOpen}
	if pr != want {
		t.Fatalf("pr = %+v", pr)
	}
}

func TestGitHubTokenResolvedPerRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"number":1}`))
	}))
	t.Cleanup(srv.Close)
	var calls int
	tokens := []string{"first", "second"}
	p, err := New(Config{APIURL: srv.URL, Owner: "o", Repo: "r", Token: func() (string, error) {
		calls++
		return tokens[calls-1], nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.CreatePullRequest(context.Background(), newPR()); err != nil {
			t.Fatal(err)
		}
		if calls != i+1 {
			t.Fatalf("calls = %d after %d requests", calls, i+1)
		}
	}
	if len(seen) != 2 || seen[0] != "Bearer first" || seen[1] != "Bearer second" {
		t.Fatalf("seen = %v", seen)
	}
}

func TestGitHubMissingToken(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	t.Cleanup(srv.Close)
	for name, tok := range map[string]func() (string, error){
		"empty":   func() (string, error) { return "", nil },
		"failing": func() (string, error) { return "", errors.New("variable GITHUB_TOKEN is unset") },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := New(Config{APIURL: srv.URL, Owner: "o", Repo: "r", Token: tok})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.CreatePullRequest(context.Background(), newPR())
			if !errors.Is(err, hosting.ErrUnauthorized) || !strings.Contains(err.Error(), "github.token") {
				t.Fatalf("err = %v", err)
			}
			_, _, err = p.FindPullRequest(context.Background(), "b")
			if !errors.Is(err, hosting.ErrUnauthorized) {
				t.Fatalf("find err = %v", err)
			}
		})
	}
	if hit.Load() {
		t.Fatal("a request was sent without a token")
	}
}

func TestGitHubErrorMapping(t *testing.T) {
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		is      error
		resetOK bool
	}{
		{"401", 401, nil, `{"message":"Bad credentials"}`, hosting.ErrUnauthorized, false},
		{"403", 403, nil, `{"message":"Forbidden"}`, hosting.ErrUnauthorized, false},
		{"404", 404, nil, `{"message":"Not Found"}`, hosting.ErrNotFound, false},
		{"422 exists", 422, nil, `{"message":"Validation Failed","errors":[{"message":"A pull request already exists for o:feat."}]}`, hosting.ErrConflict, false},
		{"422 other", 422, nil, `{"message":"Validation Failed","errors":[{"message":"base invalid"}]}`, hosting.ErrInvalid, false},
		{"429", 429, map[string]string{"Retry-After": "60"}, `{"message":"slow down"}`, hosting.ErrRateLimited, true},
		{"403 rate limit", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset.Unix(), 10)}, `{"message":"rate limit exceeded"}`, hosting.ErrRateLimited, true},
		{"500", 500, nil, `oops`, hosting.ErrUnavailable, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			_, err := p.CreatePullRequest(context.Background(), newPR())
			if !errors.Is(err, c.is) {
				t.Fatalf("err = %v, want %v", err, c.is)
			}
			var rl *hosting.RateLimitError
			if c.resetOK {
				if !errors.As(err, &rl) || rl.ResetAt.IsZero() || !rl.ResetAt.After(time.Now()) {
					t.Fatalf("rate limit = %+v", rl)
				}
				if c.status == 403 && !rl.ResetAt.Equal(reset) {
					t.Fatalf("ResetAt = %v, want %v", rl.ResetAt, reset)
				}
			}
			if c.is == hosting.ErrNotFound && !strings.Contains(err.Error(), "cannot see") {
				t.Errorf("no visibility hint: %v", err)
			}
			if c.is == hosting.ErrInvalid && !strings.Contains(err.Error(), "base invalid") {
				t.Errorf("message missing: %v", err)
			}
		})
	}

	t.Run("closed connection", func(t *testing.T) {
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		})
		if _, err := p.CreatePullRequest(context.Background(), newPR()); !errors.Is(err, hosting.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGitHubErrorsNeverContainToken(t *testing.T) {
	statuses := []int{401, 403, 404, 422, 429, 500}
	for _, status := range statuses {
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			// A hostile or buggy server echoing the credential back.
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"message":"you sent %s"}`, r.Header.Get("Authorization"))
		})
		_, err := p.CreatePullRequest(context.Background(), newPR())
		if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "Bearer "+testToken) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	})
	_, err := p.CreatePullRequest(context.Background(), newPR())
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "Bearer "+testToken) {
		t.Errorf("network err = %v", err)
	}
}

func TestGitHubErrorBodyTruncated(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 2000)))
	})
	_, err := p.CreatePullRequest(context.Background(), newPR())
	if err == nil {
		t.Fatal("no error")
	}
	if n := strings.Count(err.Error(), "x"); n != 300 {
		t.Fatalf("body characters in error = %d", n)
	}
}

func TestGitHubTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	p, err := New(Config{APIURL: srv.URL, Owner: "o", Repo: "r", Token: staticToken(""), Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CreatePullRequest(context.Background(), newPR())
	if !errors.Is(err, hosting.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestGitHubCancelledContext(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.CreatePullRequest(ctx, newPR()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

const (
	openOld   = `{"number":1,"state":"open","html_url":"u1","head":{"ref":"b"}}`
	openNew   = `{"number":3,"state":"open","html_url":"u3","head":{"ref":"b"}}`
	closedNew = `{"number":4,"state":"closed","html_url":"u4","head":{"ref":"b"}}`
)

func listServer(t *testing.T, body string) *Provider {
	return newTestProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
}

func TestGitHubFindPrefersOpen(t *testing.T) {
	p := listServer(t, "["+closedNew+","+openNew+","+openOld+"]")
	pr, found, err := p.FindPullRequest(context.Background(), "b")
	if err != nil || !found || pr.Number != 3 || pr.State != hosting.StateOpen {
		t.Fatalf("pr = %+v, found = %v, err = %v", pr, found, err)
	}
}

func TestGitHubFindNewestWhenNoneOpen(t *testing.T) {
	older := `{"number":2,"state":"closed","head":{"ref":"b"}}`
	p := listServer(t, "["+closedNew+","+older+"]")
	pr, found, err := p.FindPullRequest(context.Background(), "b")
	if err != nil || !found || pr.Number != 4 || pr.State != hosting.StateClosed {
		t.Fatalf("pr = %+v, found = %v, err = %v", pr, found, err)
	}
}

func TestGitHubFindNone(t *testing.T) {
	p := listServer(t, "[]")
	pr, found, err := p.FindPullRequest(context.Background(), "b")
	if err != nil || found || pr != (hosting.PullRequest{}) {
		t.Fatalf("pr = %+v, found = %v, err = %v", pr, found, err)
	}
}

func TestGitHubFindQueryUsesOwnerAndBranch(t *testing.T) {
	var method, path string
	var q map[string][]string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, q = r.Method, r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte("[]"))
	})
	if _, _, err := p.FindPullRequest(context.Background(), "ralph/feat"); err != nil {
		t.Fatal(err)
	}
	if method != "GET" || path != "/repos/o/r/pulls" {
		t.Fatalf("request = %s %s", method, path)
	}
	want := map[string]string{"head": "o:ralph/feat", "state": "all", "sort": "created", "direction": "desc", "per_page": "100"}
	for k, v := range want {
		if len(q[k]) != 1 || q[k][0] != v {
			t.Errorf("query %s = %v, want %s", k, q[k], v)
		}
	}
}

func TestGitHubStateMapping(t *testing.T) {
	cases := map[string]hosting.PRState{
		`{"number":1,"state":"open"}`: hosting.StateOpen,
		`{"number":1,"state":"closed","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"abc"}`: hosting.StateMerged,
		`{"number":1,"state":"closed","merged_at":null}`:                                            hosting.StateClosed,
	}
	for body, want := range cases {
		p := listServer(t, "["+body+"]")
		pr, found, err := p.FindPullRequest(context.Background(), "b")
		if err != nil || !found || pr.State != want {
			t.Errorf("%s: pr = %+v, found = %v, err = %v", body, pr, found, err)
		}
		if want == hosting.StateMerged && pr.MergeCommit != "abc" {
			t.Errorf("merge commit = %q", pr.MergeCommit)
		}
	}
}

func TestGitHubCredentialErrorsNameSetting(t *testing.T) {
	check := func(t *testing.T, err error) {
		t.Helper()
		var ce *hosting.CredentialError
		if !errors.As(err, &ce) || ce.Setting != "github.token" || !strings.Contains(err.Error(), "github.token") {
			t.Fatalf("err = %v", err)
		}
	}
	for name, tok := range map[string]func() (string, error){
		"empty":   func() (string, error) { return "", nil },
		"failing": func() (string, error) { return "", errors.New("variable GITHUB_TOKEN is unset") },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := New(Config{APIURL: "http://127.0.0.1:1", Owner: "o", Repo: "r", Token: tok})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = p.FindPullRequest(context.Background(), "b")
			check(t, err)
		})
	}
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			})
			_, err := p.CreatePullRequest(context.Background(), newPR())
			check(t, err)
			if want := "unauthorized: GitHub answered " + strconv.Itoa(status) + ": nope; check the token in setting github.token"; err.Error() != want {
				t.Fatalf("err = %q, want %q", err, want)
			}
		})
	}
}
