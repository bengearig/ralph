package jira

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/issuesource/issuetest"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira/jiratest"
)

var ctx = context.Background()

func newStub(t *testing.T) (*jiratest.Server, *Source) {
	t.Helper()
	srv := jiratest.NewServer(t, jiratest.Options{Project: "RAL"})
	s, err := New(Config{
		Site:  srv.URL(),
		User:  srv.User(),
		Token: func() (string, error) { return srv.Token(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, s
}

func offline(t *testing.T, site string) *Source {
	t.Helper()
	s, err := New(Config{Site: site, User: "u", Token: func() (string, error) { return "x", nil }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseKeyUpperCasesAndTrims(t *testing.T) {
	s := offline(t, "https://example.atlassian.net")
	for in, key := range map[string]string{"abc-123": "ABC-123", " ABC-123 ": "ABC-123", "A1_B-9": "A1_B-9"} {
		got, err := s.Parse(in)
		if err != nil || got != (issuesource.Ref{System: "jira", Key: key}) {
			t.Errorf("Parse(%q) = %v, %v", in, got, err)
		}
	}
}

func TestParseBrowseURL(t *testing.T) {
	s := offline(t, "https://example.atlassian.net/")
	for _, in := range []string{
		"https://example.atlassian.net/browse/ABC-123",
		"https://example.atlassian.net/browse/ABC-123/",
		"https://example.atlassian.net/browse/abc-123?focusedCommentId=5",
		"https://example.atlassian.net/browse/ABC-123#comment",
		"https://Example.Atlassian.NET/browse/ABC-123",
		"  https://example.atlassian.net/browse/ABC-123  ",
	} {
		got, err := s.Parse(in)
		if err != nil || got.Key != "ABC-123" {
			t.Errorf("Parse(%q) = %v, %v", in, got, err)
		}
	}
}

func TestParseSelectedIssueURL(t *testing.T) {
	s := offline(t, "https://example.atlassian.net")
	got, err := s.Parse("https://example.atlassian.net/jira/software/projects/ABC/boards/1?selectedIssue=abc-7")
	if err != nil || got.Key != "ABC-7" {
		t.Errorf("Parse = %v, %v", got, err)
	}
}

func TestParseRejectsOtherHost(t *testing.T) {
	s := offline(t, "https://example.atlassian.net")
	_, err := s.Parse("https://other.atlassian.net/browse/ABC-1")
	if err == nil || err.Error() != "other.atlassian.net is not the Jira site example.atlassian.net" || !errors.Is(err, issuesource.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	s := offline(t, "https://example.atlassian.net")
	const want = "not a Jira ticket: use a key such as ABC-123 or a URL such as https://example.atlassian.net/browse/ABC-123"
	for _, in := range []string{"abc", "123", "ABC-", "ABC-0", "ABC 123", "", "https://example.atlassian.net/", "https://example.atlassian.net/browse/", "ftp://example.atlassian.net/browse/ABC-1"} {
		_, err := s.Parse(in)
		if err == nil || err.Error() != want || !errors.Is(err, issuesource.ErrInvalid) {
			t.Errorf("Parse(%q) = %v", in, err)
		}
	}
}

func TestGetReturnsIssueWithMarkdownBodyAndURL(t *testing.T) {
	srv, s := newStub(t)
	srv.Seed(jiratest.Issue{Key: "RAL-5", Summary: "Fix the login redirect", Body: "It loops.\n\n- one\n- two", Type: "Bug", Parent: "RAL-1"})
	got, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "RAL-5"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref.String() != "jira:RAL-5" || got.Title != "Fix the login redirect" || got.Type != "Bug" || got.Parent != "RAL-1" || got.Status == "" {
		t.Errorf("Get = %+v", got)
	}
	if got.URL != srv.URL()+"/browse/RAL-5" {
		t.Errorf("URL = %q", got.URL)
	}
	if !strings.Contains(got.Body, "It loops.") || !strings.Contains(got.Body, "- one") {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestGetNotFound(t *testing.T) {
	_, s := newStub(t)
	_, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "RAL-99"})
	if !errors.Is(err, issuesource.ErrNotFound) || !strings.Contains(err.Error(), "RAL-99 was not found in Jira or is not visible to this account") {
		t.Errorf("err = %v", err)
	}
}

func TestGetUnauthorizedNamesSettings(t *testing.T) {
	srv, s := newStub(t)
	srv.RejectCredentials()
	_, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "RAL-1"})
	setting, ok := taskmanager.CredentialSetting(err)
	if !errors.Is(err, issuesource.ErrUnauthorized) || !ok || setting != "jira.token" || !strings.Contains(err.Error(), "jira.user") {
		t.Errorf("err = %v (%q)", err, setting)
	}
}

func TestGetRateLimited(t *testing.T) {
	srv, s := newStub(t)
	srv.RateLimit(time.Minute)
	_, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "RAL-1"})
	if !errors.Is(err, issuesource.ErrRateLimited) {
		t.Errorf("err = %v", err)
	}
}

func TestGetUnavailable(t *testing.T) {
	srv, s := newStub(t)
	srv.FailWith(503, "down")
	_, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "RAL-1"})
	if !errors.Is(err, issuesource.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
}

func TestGetOtherSystemRefInvalid(t *testing.T) {
	srv, s := newStub(t)
	_, err := s.Get(ctx, issuesource.Ref{System: "github", Key: "RAL-1"})
	if !errors.Is(err, issuesource.ErrInvalid) || len(srv.Requests()) != 0 {
		t.Errorf("err = %v, requests = %d", err, len(srv.Requests()))
	}
}

func TestGetCrossProjectTicket(t *testing.T) {
	srv, s := newStub(t)
	srv.Seed(jiratest.Issue{Key: "OTHER-12", Summary: "Elsewhere"})
	got, err := s.Get(ctx, issuesource.Ref{System: "jira", Key: "OTHER-12"})
	if err != nil || got.Ref.Key != "OTHER-12" || got.Title != "Elsewhere" {
		t.Errorf("Get = %+v, %v", got, err)
	}
}

func TestConformanceAgainstStub(t *testing.T) {
	srv, s := newStub(t)
	host := strings.TrimPrefix(srv.URL(), "http://")
	issuetest.Run(t, issuetest.Subject{
		Source: s,
		Seed: func(i issuesource.Issue) error {
			srv.Seed(jiratest.Issue{Key: i.Ref.Key, Summary: i.Title, Body: i.Body, Type: i.Type})
			return nil
		},
		Controls: issuetest.Controls{
			RejectCredentials: srv.RejectCredentials,
			RateLimit:         func() { srv.RateLimit(time.Second) },
			Unreachable:       func() { srv.FailWith(503, "down") },
			Heal:              srv.Heal,
		},
		Accepted: map[string]string{"abc-1": "ABC-1", srv.URL() + "/browse/ABC-1": "ABC-1"},
		Rejected: []string{"", "abc", "ABC-0", "http://" + host + "/browse/"},
		Accepts:  "use a key such as ABC-123",
	})
}
