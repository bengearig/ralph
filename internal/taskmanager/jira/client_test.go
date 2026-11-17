package jira

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
)

func TestNewRejectsBadConfig(t *testing.T) {
	tok := func() (string, error) { return "x", nil }
	for name, cfg := range map[string]Config{
		"http non-loopback":  {Site: "http://example.atlassian.net", Project: "RAL", Token: tok},
		"credentials":        {Site: "https://u:p@example.atlassian.net", Project: "RAL", Token: tok},
		"query":              {Site: "https://example.atlassian.net?a=b", Project: "RAL", Token: tok},
		"empty query mark":   {Site: "https://example.atlassian.net/?", Project: "RAL", Token: tok},
		"fragment":           {Site: "https://example.atlassian.net#x", Project: "RAL", Token: tok},
		"empty site":         {Project: "RAL", Token: tok},
		"no host":            {Site: "https://", Project: "RAL", Token: tok},
		"other scheme":       {Site: "ftp://example.atlassian.net", Project: "RAL", Token: tok},
		"nil token function": {Site: "https://example.atlassian.net", Project: "RAL"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
	c, err := New(Config{Site: "https://example.atlassian.net/", Project: "RAL", Token: tok})
	if err != nil || c.Name() != "jira" {
		t.Fatalf("New = %v, %v", c, err)
	}
}

func TestNewAllowsLoopbackHTTP(t *testing.T) {
	tok := func() (string, error) { return "x", nil }
	for _, site := range []string{"http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:80"} {
		if _, err := New(Config{Site: site, Project: "RAL", Token: tok}); err != nil {
			t.Errorf("%s: %v", site, err)
		}
	}
}

func TestAuthBasicHeader(t *testing.T) {
	var got http.Header
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, `{}`)
	}, func(c *Config) {
		c.User = "jürgen@example.com"
		c.Token = func() (string, error) { return "tok:en-é", nil }
		c.UserAgent = "ralph-test"
	})
	_, _ = c.Check(ctx)
	if a := got.Get("Authorization"); a != "Basic asO8cmdlbkBleGFtcGxlLmNvbTp0b2s6ZW4tw6k=" {
		t.Errorf("Authorization = %q", a)
	}
	if got.Get("Accept") != "application/json" || got.Get("User-Agent") != "ralph-test" {
		t.Errorf("headers = %v", got)
	}
	if got.Get("Content-Type") != "" {
		t.Errorf("Content-Type = %q on a request without a body", got.Get("Content-Type"))
	}
}

func TestAuthContentTypeOnBody(t *testing.T) {
	var ct string
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		_, _ = io.WriteString(w, `{"issues":[],"isLast":true}`)
	}, nil)
	if _, err := c.Find(ctx, "ralph-x"); err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestAuthTokenReadPerRequest(t *testing.T) {
	var seen []string
	secret := "first"
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{}`)
	}, func(c *Config) { c.Token = func() (string, error) { return secret, nil } })
	_, _ = c.Check(ctx)
	secret = "second"
	_, _ = c.Check(ctx)
	if seen[0] != basic("me@example.com", "first") || seen[len(seen)-1] != basic("me@example.com", "second") {
		t.Errorf("headers = %q", seen)
	}
}

func basic(user, token string) string {
	return "Basic " + b64(user+":"+token)
}

func TestAuthMissingTokenNoRequest(t *testing.T) {
	hits := 0
	for name, tok := range map[string]func() (string, error){
		"empty": func() (string, error) { return "", nil },
		"error": func() (string, error) { return "", errors.New("env JIRA_TOKEN is not set") },
	} {
		c := rawClient(t, func(http.ResponseWriter, *http.Request) { hits++ }, func(c *Config) { c.Token = tok })
		_, err := c.Check(ctx)
		setting, ok := taskmanager.CredentialSetting(err)
		if !errors.Is(err, taskmanager.ErrUnauthorized) || !ok || setting != "jira.token" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if hits != 0 {
		t.Errorf("the server saw %d requests", hits)
	}
}

func TestAuthEmptyUserNamesUser(t *testing.T) {
	hits := 0
	c := rawClient(t, func(http.ResponseWriter, *http.Request) { hits++ }, func(c *Config) { c.User = "" })
	_, err := c.Types(ctx)
	setting, ok := taskmanager.CredentialSetting(err)
	if !errors.Is(err, taskmanager.ErrUnauthorized) || !ok || setting != "jira.user" || hits != 0 {
		t.Errorf("err = %v, hits = %d", err, hits)
	}
}

func TestStatusMapping(t *testing.T) {
	later := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
	respond := func(status int, hdr map[string]string, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	for _, tc := range []struct {
		name   string
		h      http.HandlerFunc
		want   error
		modify func(*Config)
	}{
		{"401", respond(401, nil, `{"errorMessages":["no"]}`), taskmanager.ErrUnauthorized, nil},
		{"403", respond(403, nil, `{"errorMessages":["no"]}`), taskmanager.ErrUnauthorized, nil},
		{"404", respond(404, nil, `{"errorMessages":["gone"]}`), taskmanager.ErrNotFound, nil},
		{"429 seconds", respond(429, map[string]string{"Retry-After": "90"}, `{}`), taskmanager.ErrRateLimited, nil},
		{"429 date", respond(429, map[string]string{"Retry-After": later.Format(http.TimeFormat)}, `{}`), taskmanager.ErrRateLimited, nil},
		{"429 none", respond(429, nil, `{}`), taskmanager.ErrRateLimited, nil},
		{"400", respond(400, nil, `{"errorMessages":["bad"]}`), taskmanager.ErrInvalid, nil},
		{"422", respond(422, nil, `{"errorMessages":["bad"]}`), taskmanager.ErrInvalid, nil},
		{"500", respond(500, nil, `oops`), taskmanager.ErrUnavailable, nil},
		{"503", respond(503, nil, ``), taskmanager.ErrUnavailable, nil},
		{"unreadable", respond(200, nil, `not json`), taskmanager.ErrUnavailable, nil},
		{"closed connection", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, taskmanager.ErrUnavailable, nil},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		}, taskmanager.ErrUnavailable, func(c *Config) { c.Timeout = 50 * time.Millisecond }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := rawClient(t, tc.h, tc.modify)
			_, err := c.Types(ctx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var rl *taskmanager.RateLimitError
			if strings.HasPrefix(tc.name, "429") {
				if !errors.As(err, &rl) {
					t.Fatalf("err = %v, want a *RateLimitError", err)
				}
				switch tc.name {
				case "429 none":
					if !rl.ResetAt.IsZero() {
						t.Errorf("ResetAt = %v", rl.ResetAt)
					}
				case "429 date":
					if !rl.ResetAt.Equal(later) {
						t.Errorf("ResetAt = %v, want %v", rl.ResetAt, later)
					}
				default:
					if d := time.Until(rl.ResetAt); d < 85*time.Second || d > 91*time.Second {
						t.Errorf("ResetAt in %v, want about 90s", d)
					}
				}
			}
			if tc.want == taskmanager.ErrUnauthorized {
				if s, _ := taskmanager.CredentialSetting(err); s != "jira.token" || !strings.Contains(err.Error(), "jira.user") {
					t.Errorf("err = %v, want it to name jira.token and jira.user", err)
				}
			}
		})
	}
	t.Run("cancelled context", func(t *testing.T) {
		c := rawClient(t, respond(200, nil, `{}`), nil)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := c.Types(cctx); !errors.Is(err, context.Canceled) || errors.Is(err, taskmanager.ErrUnavailable) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

func TestStatusMappingMessageFromErrorMessagesAndErrors(t *testing.T) {
	c := rawClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"errorMessages":["Something is wrong"],"errors":{"summary":"too long","labels":"bad label"}}`)
	}, nil)
	_, err := c.Types(ctx)
	for _, want := range []string{"Something is wrong", "summary: too long", "labels: bad label"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}

	long := strings.Repeat("x", 1000)
	c = rawClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, long)
	}, nil)
	_, err = c.Types(ctx)
	if err == nil || strings.Contains(err.Error(), strings.Repeat("x", 301)) || !strings.Contains(err.Error(), strings.Repeat("x", 300)) {
		t.Errorf("err = %v, want the text cut at 300 characters", err)
	}
}

func TestResponseCap(t *testing.T) {
	for _, status := range []int{200, 500} {
		c := rawClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			chunk := strings.Repeat(" ", 1<<20)
			for range 11 {
				_, _ = io.WriteString(w, chunk)
			}
		}, nil)
		if _, err := c.Types(ctx); !errors.Is(err, taskmanager.ErrUnavailable) {
			t.Errorf("status %d: err = %v, want ErrUnavailable", status, err)
		}
	}
}

func TestRedactionNoTokenInAnyError(t *testing.T) {
	auth := basic("me@example.com", testToken)
	for _, status := range []int{400, 401, 403, 404, 422, 500, 200} {
		c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			echo := fmt.Sprintf("token %s header %s raw %s", testToken, r.Header.Get("Authorization"), strings.TrimPrefix(auth, "Basic "))
			_, _ = fmt.Fprintf(w, `{"errorMessages":[%q],"errors":{"summary":%q}}`, echo, echo)
			if status == 200 {
				_, _ = io.WriteString(w, "garbage")
			}
		}, nil)
		ops := map[string]func() error{
			"Check":  func() error { _, err := c.Check(ctx); return err },
			"Types":  func() error { _, err := c.Types(ctx); return err },
			"Find":   func() error { _, err := c.Find(ctx, "r"); return err },
			"Create": func() error { _, err := c.Create(ctx, taskmanager.New{Type: "Task", Title: "t"}); return err },
			"Update": func() error { return c.Update(ctx, "RAL-1", taskmanager.Fields{Title: "t"}) },
			"Get":    func() error { _, err := c.Get(ctx, []string{"RAL-1"}); return err },
		}
		for op, f := range ops {
			err := f()
			if err == nil {
				continue
			}
			for _, secret := range []string{testToken, auth, strings.TrimPrefix(auth, "Basic ")} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("status %d %s: error %q contains a secret", status, op, err)
				}
			}
		}
	}
}

func TestRedactionConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	site := srv.URL
	srv.Close()
	c, err := New(Config{Site: site, Project: "RAL", User: "u", Token: func() (string, error) { return testToken, nil }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Types(ctx)
	if !errors.Is(err, taskmanager.ErrUnavailable) || strings.Contains(err.Error(), testToken) {
		t.Errorf("err = %v", err)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestNewWithoutProject(t *testing.T) {
	c, err := New(Config{Site: "https://example.atlassian.net", Token: func() (string, error) { return "x", nil }})
	if err != nil || c.Name() != "jira" {
		t.Fatalf("New = %v, %v", c, err)
	}
}
