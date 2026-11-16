package bitbucket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const (
	testToken = "bbtok_SECRETvalue123"
	testEmail = "person@example.com"
)

func tokenFn() (string, error) { return testToken, nil }

func newTestProvider(t *testing.T, h http.HandlerFunc, mod ...func(*Config)) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := Config{APIURL: srv.URL, Workspace: "ws", Repo: "rp", Token: tokenFn, Timeout: 5 * time.Second}
	for _, m := range mod {
		m(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newPR() hosting.NewPullRequest {
	return hosting.NewPullRequest{Head: "feat", Base: "main", Title: "T", Body: "B", Draft: true}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string, fields map[string]any) map[string]any {
	e := map[string]any{"message": msg}
	if fields != nil {
		e["fields"] = fields
	}
	return map[string]any{"type": "error", "error": e}
}

func prBody(id int, state, head, repo string) map[string]any {
	return map[string]any{
		"id": id, "title": "T", "description": "B", "state": state, "draft": true,
		"source":      map[string]any{"branch": map[string]any{"name": head}, "repository": map[string]any{"full_name": repo}},
		"destination": map[string]any{"branch": map[string]any{"name": "main"}},
		"links":       map[string]any{"html": map[string]any{"href": fmt.Sprintf("https://bitbucket.org/ws/rp/pull-requests/%d", id)}},
	}
}

func TestNewBitbucketValidation(t *testing.T) {
	tok := tokenFn
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"empty workspace", Config{Repo: "r", Token: tok}, false},
		{"empty repo", Config{Workspace: "w", Token: tok}, false},
		{"http non-loopback", Config{APIURL: "http://bb.example.com", Workspace: "w", Repo: "r", Token: tok}, false},
		{"no token function", Config{Workspace: "w", Repo: "r"}, false},
		{"no scheme", Config{APIURL: "bb.example.com", Workspace: "w", Repo: "r", Token: tok}, false},
		{"http loopback", Config{APIURL: "http://127.0.0.1:1", Workspace: "w", Repo: "r", Token: tok}, true},
		{"http localhost", Config{APIURL: "http://localhost:1", Workspace: "w", Repo: "r", Token: tok}, true},
		{"http ipv6 loopback", Config{APIURL: "http://[::1]:1", Workspace: "w", Repo: "r", Token: tok}, true},
		{"defaults", Config{Workspace: "w", Repo: "r", Token: tok}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.cfg)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok %v", err, c.ok)
			}
		})
	}
	p, err := New(Config{APIURL: "https://bb.example.com/2.0/", Workspace: "w", Repo: "r", Token: tok})
	if err != nil || p.apiURL != "https://bb.example.com/2.0" || p.userAgent != "ralph" || p.client.Timeout != 30*time.Second {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	p, err = New(Config{Workspace: "w", Repo: "r", Token: tok})
	if err != nil || p.apiURL != "https://api.bitbucket.org/2.0" {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
}

func TestBitbucketCreateSendsRequest(t *testing.T) {
	var got map[string]any
	var method, path, accept, ua, ctype string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, accept, ua, ctype = r.Method, r.URL.EscapedPath(), r.Header.Get("Accept"), r.UserAgent(), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		writeJSON(w, 201, prBody(7, "OPEN", "feat", "ws/rp"))
	}, func(c *Config) { c.Workspace = "w s"; c.UserAgent = "ralph/test" })
	if _, err := p.CreatePullRequest(context.Background(), newPR()); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || path != "/repositories/w%20s/rp/pullrequests" {
		t.Fatalf("%s %s", method, path)
	}
	if accept != "application/json" || ua != "ralph/test" || ctype != "application/json" {
		t.Fatalf("accept = %q ua = %q ctype = %q", accept, ua, ctype)
	}
	src := got["source"].(map[string]any)["branch"].(map[string]any)["name"]
	dst := got["destination"].(map[string]any)["branch"].(map[string]any)["name"]
	if got["title"] != "T" || got["description"] != "B" || src != "feat" || dst != "main" || got["draft"] != true || got["close_source_branch"] != false {
		t.Fatalf("body = %v", got)
	}
}

func TestBitbucketCreateOmitsDestinationWithoutBase(t *testing.T) {
	var got map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		writeJSON(w, 201, prBody(7, "OPEN", "feat", "ws/rp"))
	})
	pr := newPR()
	pr.Base = ""
	if _, err := p.CreatePullRequest(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["destination"]; ok {
		t.Fatalf("body = %v", got)
	}
}

func TestBitbucketCreateReturnsPullRequest(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, prBody(7, "OPEN", "feat", "ws/rp"))
	})
	got, err := p.CreatePullRequest(context.Background(), newPR())
	if err != nil {
		t.Fatal(err)
	}
	want := hosting.PullRequest{Number: 7, URL: "https://bitbucket.org/ws/rp/pull-requests/7", Title: "T", Body: "B", Head: "feat", Base: "main", Draft: true, State: hosting.StateOpen}
	if got != want {
		t.Fatalf("got %+v", got)
	}
}

func authCapture(auth *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		writeJSON(w, 200, prBody(1, "OPEN", "feat", "ws/rp"))
	}
}

func TestBitbucketBearerWhenNoUser(t *testing.T) {
	var auth string
	p := newTestProvider(t, authCapture(&auth))
	if _, err := p.GetPullRequest(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+testToken {
		t.Fatalf("auth = %q", auth)
	}
}

func TestBitbucketBasicWithUser(t *testing.T) {
	var auth string
	p := newTestProvider(t, authCapture(&auth), func(c *Config) { c.User = testEmail })
	if _, err := p.GetPullRequest(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testToken)); auth != want {
		t.Fatalf("auth = %q", auth)
	}
}

func TestBitbucketTokenResolvedPerRequest(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var n atomic.Int32
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		writeJSON(w, 200, prBody(1, "OPEN", "feat", "ws/rp"))
	}, func(c *Config) {
		c.Token = func() (string, error) { return fmt.Sprintf("t%d", n.Add(1)), nil }
	})
	for range 2 {
		if _, err := p.GetPullRequest(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != "Bearer t1" || seen[1] != "Bearer t2" {
		t.Fatalf("seen = %v", seen)
	}
}

func TestBitbucketMissingToken(t *testing.T) {
	var called atomic.Bool
	for name, fn := range map[string]func() (string, error){
		"empty":   func() (string, error) { return "", nil },
		"failing": func() (string, error) { return "", errors.New("env BITBUCKET_TOKEN is not set") },
	} {
		t.Run(name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) { called.Store(true) }, func(c *Config) { c.Token = fn })
			_, err := p.GetPullRequest(context.Background(), 1)
			if !errors.Is(err, hosting.ErrUnauthorized) {
				t.Fatalf("err = %v", err)
			}
			if s, ok := hosting.CredentialSetting(err); !ok || s != "bitbucket.token" {
				t.Fatalf("setting = %q, %v", s, ok)
			}
		})
	}
	if called.Load() {
		t.Fatal("request sent without a token")
	}
}

func TestBitbucketErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		user    string
		status  int
		header  map[string]string
		body    any
		check   func(t *testing.T, err error)
		closeIt bool
	}{
		{name: "401 bearer", status: 401, body: errBody("Bad credentials", nil), check: func(t *testing.T, err error) {
			wantCredential(t, err, "bitbucket.token", "bitbucket.user", "not set")
		}},
		{name: "401 basic", user: testEmail, status: 401, body: errBody("Bad credentials", nil), check: func(t *testing.T, err error) {
			wantCredential(t, err, "bitbucket.token", "bitbucket.user")
		}},
		{name: "403", status: 403, body: errBody("no", nil), check: func(t *testing.T, err error) {
			wantCredential(t, err, "bitbucket.token", "lacks permission", "pull requests read and write")
		}},
		{name: "404", status: 404, body: errBody("missing", nil), check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrNotFound) || !strings.Contains(err.Error(), "cannot see") {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "400 already", status: 400, body: errBody("There is already an open pull request (#3) for this source branch", nil), check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrConflict) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "409 already", status: 409, body: errBody("pull request already exists", nil), check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrConflict) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "400 fields", status: 400, body: errBody("Bad request", map[string]any{"source.branch.name": []string{"Branch not found"}, "title": "required"}), check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrInvalid) || !strings.Contains(err.Error(), "Bad request") ||
				!strings.Contains(err.Error(), "source.branch.name: Branch not found") || !strings.Contains(err.Error(), "title: required") {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "429 retry-after", status: 429, header: map[string]string{"Retry-After": "30"}, body: errBody("slow", nil), check: func(t *testing.T, err error) {
			var rl *hosting.RateLimitError
			if !errors.As(err, &rl) || !errors.Is(err, hosting.ErrRateLimited) {
				t.Fatalf("err = %v", err)
			}
			if d := time.Until(rl.ResetAt); d < 25*time.Second || d > 31*time.Second {
				t.Fatalf("ResetAt in %v", d)
			}
		}},
		{name: "429 bare", status: 429, body: errBody("slow", nil), check: func(t *testing.T, err error) {
			var rl *hosting.RateLimitError
			if !errors.As(err, &rl) || !rl.ResetAt.IsZero() {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "500", status: 500, body: errBody("oops", nil), check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "closed connection", closeIt: true, check: func(t *testing.T, err error) {
			if !errors.Is(err, hosting.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if c.closeIt {
					hj, _ := w.(http.Hijacker)
					conn, _, _ := hj.Hijack()
					_ = conn.Close()
					return
				}
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				writeJSON(w, c.status, c.body)
			}, func(cfg *Config) { cfg.User = c.user })
			_, err := p.GetPullRequest(context.Background(), 1)
			if err == nil {
				t.Fatal("no error")
			}
			c.check(t, err)
		})
	}
}

func wantCredential(t *testing.T, err error, setting string, contains ...string) {
	t.Helper()
	if !errors.Is(err, hosting.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if s, ok := hosting.CredentialSetting(err); !ok || s != setting {
		t.Fatalf("setting = %q", s)
	}
	for _, c := range contains {
		if !strings.Contains(err.Error(), c) {
			t.Fatalf("err %q lacks %q", err, c)
		}
	}
}

func TestBitbucketErrorsNeverContainTokenOrEmail(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500, 418} {
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, status, errBody("echo "+testToken+" for "+testEmail, map[string]any{"f": testToken + testEmail}))
		}, func(c *Config) { c.User = testEmail })
		_, err := p.GetPullRequest(context.Background(), 1)
		if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testEmail) {
			t.Fatalf("status %d: err = %v", status, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	p, err := New(Config{APIURL: addr, Workspace: "w", Repo: "r", User: testEmail, Token: tokenFn})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GetPullRequest(context.Background(), 1)
	if !errors.Is(err, hosting.ErrUnavailable) || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testEmail) {
		t.Fatalf("err = %v", err)
	}
}

func TestBitbucketErrorMessageTruncated(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 500, errBody(strings.Repeat("x", 2000), nil))
	})
	_, err := p.GetPullRequest(context.Background(), 1)
	if err == nil || strings.Count(err.Error(), "x") > 300 {
		t.Fatalf("err has %d x's", strings.Count(fmt.Sprint(err), "x"))
	}
}

func TestBitbucketTimeout(t *testing.T) {
	done := make(chan struct{})
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}, func(c *Config) { c.Timeout = 50 * time.Millisecond })
	defer close(done)
	_, err := p.GetPullRequest(context.Background(), 1)
	if !errors.Is(err, hosting.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

type findServer struct {
	mu      sync.Mutex
	queries []url.Values
	respond func(call int, q url.Values) any
}

func (f *findServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.Query())
	n := len(f.queries)
	f.mu.Unlock()
	writeJSON(w, 200, f.respond(n, r.URL.Query()))
}

func values(prs ...map[string]any) map[string]any { return map[string]any{"values": prs} }

func TestBitbucketFindPrefersOpen(t *testing.T) {
	f := &findServer{respond: func(int, url.Values) any {
		return values(prBody(4, "OPEN", "feat", "WS/RP"), prBody(5, "OPEN", "feat", "ws/rp"))
	}}
	p := newTestProvider(t, f.handler)
	got, ok, err := p.FindPullRequest(context.Background(), "feat")
	if err != nil || !ok || got.Number != 4 {
		t.Fatalf("got %+v ok %v err %v", got, ok, err)
	}
	if len(f.queries) != 1 {
		t.Fatalf("calls = %d", len(f.queries))
	}
	q := f.queries[0]
	if q.Get("q") != `source.branch.name="feat"` || strings.Join(q["state"], ",") != "OPEN" || q.Get("pagelen") != "50" {
		t.Fatalf("query = %v", q)
	}
}

func TestBitbucketFindFallsBackToNewest(t *testing.T) {
	f := &findServer{respond: func(call int, _ url.Values) any {
		if call == 1 {
			return values()
		}
		return values(prBody(9, "MERGED", "feat", "ws/rp"), prBody(8, "DECLINED", "feat", "ws/rp"))
	}}
	p := newTestProvider(t, f.handler)
	got, ok, err := p.FindPullRequest(context.Background(), "feat")
	if err != nil || !ok || got.Number != 9 {
		t.Fatalf("got %+v ok %v err %v", got, ok, err)
	}
	q := f.queries[1]
	if strings.Join(q["state"], ",") != "OPEN,MERGED,DECLINED,SUPERSEDED" || q.Get("sort") != "-created_on" || q.Get("pagelen") != "50" || q.Get("q") != `source.branch.name="feat"` {
		t.Fatalf("query = %v", q)
	}
}

func TestBitbucketFindNone(t *testing.T) {
	f := &findServer{respond: func(int, url.Values) any { return values() }}
	p := newTestProvider(t, f.handler)
	_, ok, err := p.FindPullRequest(context.Background(), "feat")
	if ok || err != nil || len(f.queries) != 2 {
		t.Fatalf("ok %v err %v calls %d", ok, err, len(f.queries))
	}
}

func TestBitbucketFindQueryEscapesBranchName(t *testing.T) {
	f := &findServer{respond: func(int, url.Values) any { return values() }}
	p := newTestProvider(t, f.handler)
	if _, _, err := p.FindPullRequest(context.Background(), `a"b\c`); err != nil {
		t.Fatal(err)
	}
	if got, want := f.queries[0].Get("q"), `source.branch.name="a\"b\\c"`; got != want {
		t.Fatalf("q = %s, want %s", got, want)
	}
}

func TestBitbucketFindIgnoresForkPullRequests(t *testing.T) {
	f := &findServer{respond: func(int, url.Values) any {
		return values(prBody(1, "OPEN", "feat", "fork/rp"), prBody(2, "OPEN", "feat", "ws/rp"))
	}}
	p := newTestProvider(t, f.handler)
	got, ok, err := p.FindPullRequest(context.Background(), "feat")
	if err != nil || !ok || got.Number != 2 {
		t.Fatalf("got %+v ok %v err %v", got, ok, err)
	}
}

func TestBitbucketStateMapping(t *testing.T) {
	cases := []struct {
		state string
		want  hosting.PRState
		merge string
	}{
		{"OPEN", hosting.StateOpen, ""},
		{"MERGED", hosting.StateMerged, "abc123"},
		{"DECLINED", hosting.StateClosed, ""},
		{"SUPERSEDED", hosting.StateClosed, ""},
	}
	for _, c := range cases {
		t.Run(c.state, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				b := prBody(3, c.state, "feat", "ws/rp")
				b["merge_commit"] = map[string]any{"hash": "abc123"}
				writeJSON(w, 200, b)
			})
			got, err := p.GetPullRequest(context.Background(), 3)
			if err != nil || got.State != c.want || got.MergeCommit != c.merge {
				t.Fatalf("got %+v err %v", got, err)
			}
		})
	}
}

func TestBitbucketGetNotFound(t *testing.T) {
	var path string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeJSON(w, 404, errBody("No pull request", nil))
	})
	_, err := p.GetPullRequest(context.Background(), 42)
	if !errors.Is(err, hosting.ErrNotFound) || path != "/repositories/ws/rp/pullrequests/42" {
		t.Fatalf("err = %v path = %s", err, path)
	}
}

func TestBitbucketUnknownFieldsIgnored(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b := prBody(3, "OPEN", "feat", "ws/rp")
		b["participants"] = []any{map[string]any{"state": "approved"}}
		b["brand_new"] = map[string]any{"x": 1}
		writeJSON(w, 200, b)
	})
	if got, err := p.GetPullRequest(context.Background(), 3); err != nil || got.Number != 3 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestBitbucketNextLinkSameHostFollowed(t *testing.T) {
	var srvURL string
	var calls atomic.Int32
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeJSON(w, 200, map[string]any{"values": []int{1, 2}, "next": srvURL + "/repositories/ws/rp/pullrequests?page=2"})
			return
		}
		if r.URL.Query().Get("page") != "2" || r.Header.Get("Authorization") == "" {
			t.Errorf("second request %s auth %q", r.URL, r.Header.Get("Authorization"))
		}
		writeJSON(w, 200, map[string]any{"values": []int{3}})
	}, func(c *Config) { srvURL = c.APIURL })
	got, err := collect[int](context.Background(), p, p.repoPath("/pullrequests"), nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestBitbucketNextLinkOtherHostRefused(t *testing.T) {
	var other atomic.Int32
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Add(1) }))
	t.Cleanup(otherSrv.Close)
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"values": []int{1}, "next": otherSrv.URL + "/x"})
	})
	_, err := collect[int](context.Background(), p, p.repoPath("/pullrequests"), nil)
	if err == nil || !strings.Contains(err.Error(), "leaves the configured API address") || other.Load() != 0 {
		t.Fatalf("err = %v, other requests = %d", err, other.Load())
	}
}

func TestBitbucketNextLinkPageLimit(t *testing.T) {
	var srvURL string
	var calls atomic.Int32
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, 200, map[string]any{"values": []int{1}, "next": srvURL + "/again"})
	}, func(c *Config) { srvURL = c.APIURL })
	_, err := collect[int](context.Background(), p, p.repoPath("/pullrequests"), nil)
	if err == nil || calls.Load() != 50 {
		t.Fatalf("err = %v calls = %d", err, calls.Load())
	}
}

func TestBitbucketReviewFeedbackNotImplementedYet(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := p.ReviewFeedback(context.Background(), 1, hosting.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("err = %v", err)
	}
}
