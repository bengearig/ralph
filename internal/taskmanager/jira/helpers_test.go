package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

var ctx = context.Background()

func newStub(t *testing.T, opts jiratest.Options) (*jiratest.Server, *Client) {
	t.Helper()
	srv := jiratest.NewServer(t, opts)
	c, err := New(Config{
		Site:    srv.URL(),
		User:    srv.User(),
		Project: srv.Project(),
		Token:   func() (string, error) { return srv.Token(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

// rawClient is a client of a handler written by the test.
func rawClient(t *testing.T, h http.HandlerFunc, mod func(*Config)) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := Config{
		Site:    srv.URL,
		User:    "me@example.com",
		Project: "RAL",
		Token:   func() (string, error) { return testToken, nil },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const testToken = "s3cr3t-t0ken"

func requestsTo(srv *jiratest.Server, method, path string) []jiratest.Request {
	var out []jiratest.Request
	for _, r := range srv.Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// roundTrip converts v to what json.Unmarshal into any would give.
func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
