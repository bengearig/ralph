// Package jira implements taskmanager.System for tickets in Jira Cloud over
// its REST API v3.
//
// The requests are written from Atlassian's published Jira Cloud REST API v3
// description. No Jira site was available when this package was written, so
// the stub server in jiratest is what the default tests check them against;
// the live test, which needs a real site, is the first check against Jira
// itself.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "ralph"
	maxErrorChars    = 300
	maxResponseBytes = 10 << 20
	tokenSetting     = "jira.token"
	userSetting      = "jira.user"
)

// Config configures a Client.
type Config struct {
	// Site is the address of the Jira site, such as https://example.atlassian.net.
	Site    string
	User    string
	Project string
	// Board is the id of the board whose sprints Schedule uses; zero means the
	// project's only scrum board.
	Board int
	// Token is called for every request so a changed secret is picked up.
	Token      func() (string, error)
	Timeout    time.Duration
	HTTPClient *http.Client
	UserAgent  string
}

// Client talks to one Jira project.
type Client struct {
	site      string
	user      string
	project   string
	token     func() (string, error)
	client    *http.Client
	userAgent string
	board     int

	mu         sync.Mutex
	foundBoard int
	sprints    map[string]int
}

var _ taskmanager.System = (*Client)(nil)

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	site := strings.TrimSuffix(cfg.Site, "/")
	if err := checkSite(site); err != nil {
		return nil, err
	}
	if cfg.Token == nil {
		return nil, errors.New("jira: a token function is required")
	}
	if cfg.Board < 0 {
		return nil, errors.New("jira: the board id must not be negative")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	return &Client{site: site, user: cfg.User, project: cfg.Project, token: cfg.Token, client: client, userAgent: ua, board: cfg.Board, sprints: map[string]int{}}, nil
}

func checkSite(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("jira: the site is not a valid URL")
	}
	if u.User != nil {
		return errors.New("jira: the site must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return errors.New("jira: the site must not have a query or a fragment")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
	}
	return errors.New("jira: the site must use https unless its host is a loopback address")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var errNoProject = &invalidError{"jira.project is not set"}

func (c *Client) needProject() error {
	if c.project == "" {
		return errNoProject
	}
	return nil
}

// Name returns jira.
func (c *Client) Name() string { return "jira" }

// do sends one request and decodes a 2xx JSON response into out when it is not nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	token, err := c.token()
	if err != nil {
		return taskmanager.NewCredentialError(tokenSetting, fmt.Sprintf("cannot read the token from setting %s: %v", tokenSetting, err))
	}
	if token == "" {
		return taskmanager.NewCredentialError(tokenSetting, fmt.Sprintf("setting %s resolved to an empty token", tokenSetting))
	}
	if c.user == "" {
		return taskmanager.NewCredentialError(userSetting, fmt.Sprintf("setting %s is empty", userSetting))
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(c.user+":"+token))
	secrets := []string{token, auth, strings.TrimPrefix(auth, "Basic ")}

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("jira: encoding request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	target := c.site + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("jira: building request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		cause := errors.Unwrap(err)
		if cause == nil {
			cause = err
		}
		return fmt.Errorf("%w: %s %s: %s", taskmanager.ErrUnavailable, method, path, redact(cause.Error(), secrets))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: reading response: %s", taskmanager.ErrUnavailable, redact(err.Error(), secrets))
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("%w: the response from Jira is larger than %d bytes", taskmanager.ErrUnavailable, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp, raw, secrets)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: unreadable response from Jira", taskmanager.ErrUnavailable)
	}
	return nil
}

func redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

// apiError is the body Jira answers an error with.
type apiError struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
	Message       string            `json:"message"`
}

// message returns Jira's own explanation if the body has one, else the raw text.
func message(raw []byte) string {
	var ae apiError
	if err := json.Unmarshal(raw, &ae); err == nil {
		var parts []string
		for _, m := range ae.ErrorMessages {
			if m != "" {
				parts = append(parts, m)
			}
		}
		if ae.Message != "" {
			parts = append(parts, ae.Message)
		}
		fields := make([]string, 0, len(ae.Errors))
		for f := range ae.Errors {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, f := range fields {
			parts = append(parts, f+": "+ae.Errors[f])
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	return strings.TrimSpace(string(raw))
}

func statusError(resp *http.Response, raw []byte, secrets []string) error {
	msg := truncate(redact(message(raw), secrets), maxErrorChars)
	status := resp.StatusCode
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return taskmanager.NewCredentialError(tokenSetting, fmt.Sprintf("Jira answered %d: %s; check the settings %s and %s", status, msg, userSetting, tokenSetting))
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: Jira answered 404: %s", taskmanager.ErrNotFound, msg)
	case status == http.StatusTooManyRequests:
		return &taskmanager.RateLimitError{ResetAt: retryAt(resp.Header.Get("Retry-After"))}
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %s", taskmanager.ErrInvalid, msg)
	case status >= 500:
		return fmt.Errorf("%w: Jira answered %d: %s", taskmanager.ErrUnavailable, status, msg)
	case status >= 400:
		return fmt.Errorf("%w: Jira answered %d: %s", taskmanager.ErrInvalid, status, msg)
	}
	return fmt.Errorf("%w: unexpected Jira status %d: %s", taskmanager.ErrUnavailable, status, msg)
}

func retryAt(h string) time.Time {
	if secs, err := strconv.ParseInt(strings.TrimSpace(h), 10, 64); err == nil && secs >= 0 {
		return time.Now().Add(time.Duration(secs) * time.Second)
	}
	if t, err := http.ParseTime(h); err == nil {
		return t
	}
	return time.Time{}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
