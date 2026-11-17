// Package jira reads tickets from Jira Cloud as an issuesource.Source.
package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/issuesource"
	tmjira "github.com/bengearig/ralph/internal/taskmanager/jira"
)

const system = "jira"

var keyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[1-9][0-9]*$`)

// Config configures a Source.
type Config struct {
	// Site is the address of the Jira site, such as https://example.atlassian.net.
	Site  string
	User  string
	Token func() (string, error)
	// Timeout bounds each request; zero takes the client's default.
	Timeout    time.Duration
	HTTPClient *http.Client
	UserAgent  string
}

// Source reads tickets from one Jira site.
type Source struct {
	client *tmjira.Client
	site   string
	host   string
}

var _ issuesource.Source = (*Source)(nil)

// New validates cfg and returns a Source.
func New(cfg Config) (*Source, error) {
	client, err := tmjira.New(tmjira.Config{
		Site:       cfg.Site,
		User:       cfg.User,
		Token:      cfg.Token,
		Timeout:    cfg.Timeout,
		HTTPClient: cfg.HTTPClient,
		UserAgent:  cfg.UserAgent,
	})
	if err != nil {
		return nil, err
	}
	site := strings.TrimSuffix(cfg.Site, "/")
	u, err := url.Parse(site)
	if err != nil {
		return nil, fmt.Errorf("jira: the site is not a valid URL")
	}
	return &Source{client: client, site: site, host: u.Host}, nil
}

// Name returns jira.
func (s *Source) Name() string { return system }

type invalidError struct{ msg string }

func (e *invalidError) Error() string        { return e.msg }
func (e *invalidError) Is(target error) bool { return target == issuesource.ErrInvalid }

func (s *Source) notATicket() error {
	return &invalidError{fmt.Sprintf("not a Jira ticket: use a key such as ABC-123 or a URL such as https://%s/browse/ABC-123", s.host)}
}

// Parse accepts a key, the address of a ticket on the site, or an address on
// the site whose query has selectedIssue.
func (s *Source) Parse(ref string) (issuesource.Ref, error) {
	ref = strings.TrimSpace(ref)
	if keyPattern.MatchString(ref) {
		return s.ref(ref), nil
	}
	lower := strings.ToLower(ref)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return issuesource.Ref{}, s.notATicket()
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return issuesource.Ref{}, s.notATicket()
	}
	if !strings.EqualFold(u.Host, s.host) {
		return issuesource.Ref{}, &invalidError{fmt.Sprintf("%s is not the Jira site %s", u.Host, s.host)}
	}
	if key, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/browse/"); ok && keyPattern.MatchString(key) {
		return s.ref(key), nil
	}
	if key := u.Query().Get("selectedIssue"); keyPattern.MatchString(key) {
		return s.ref(key), nil
	}
	return issuesource.Ref{}, s.notATicket()
}

func (s *Source) ref(key string) issuesource.Ref {
	return issuesource.Ref{System: system, Key: strings.ToUpper(key)}
}

// Get returns the ticket ref names, under the key Jira reports for it.
func (s *Source) Get(ctx context.Context, ref issuesource.Ref) (issuesource.Issue, error) {
	if ref.System != system {
		return issuesource.Issue{}, &invalidError{fmt.Sprintf("%s is not a Jira ticket", ref)}
	}
	t, err := s.client.Issue(ctx, ref.Key)
	if err != nil {
		return issuesource.Issue{}, err
	}
	key := t.Key
	if key == "" {
		key = ref.Key
	}
	return issuesource.Issue{
		Ref:    issuesource.Ref{System: system, Key: key},
		URL:    s.site + "/browse/" + url.PathEscape(key),
		Title:  t.Title,
		Body:   t.Body,
		Type:   t.Type,
		Status: t.Status,
		Parent: t.Parent,
	}, nil
}
