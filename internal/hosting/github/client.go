// Package github implements hosting.Provider for GitHub over its REST API.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const (
	defaultAPIURL    = "https://api.github.com"
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "ralph"
	apiVersion       = "2022-11-28"
	maxErrorChars    = 300
	maxResponseBytes = 10 << 20
	tokenSetting     = "github.token"
)

// Config configures a Provider.
type Config struct {
	APIURL string
	Owner  string
	Repo   string
	// Token is called for every request so a changed secret is picked up.
	Token      func() (string, error)
	Timeout    time.Duration
	HTTPClient *http.Client
	UserAgent  string
}

// Provider talks to one GitHub repository.
type Provider struct {
	apiURL    string
	owner     string
	repo      string
	token     func() (string, error)
	client    *http.Client
	userAgent string
}

var _ hosting.Provider = (*Provider)(nil)

// New validates cfg and returns a Provider.
func New(cfg Config) (*Provider, error) {
	apiURL := strings.TrimSuffix(cfg.APIURL, "/")
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	if err := checkAPIURL(apiURL); err != nil {
		return nil, err
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		return nil, errors.New("github: owner and repository must be set")
	}
	if cfg.Token == nil {
		return nil, errors.New("github: a token function is required")
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
	return &Provider{apiURL: apiURL, owner: cfg.Owner, repo: cfg.Repo, token: cfg.Token, client: client, userAgent: ua}, nil
}

func checkAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("github: API URL is not a valid URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
	}
	return errors.New("github: API URL must use https unless its host is a loopback address")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *Provider) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo) + suffix
}

// do sends one request and decodes a 2xx JSON response into out.
func (p *Provider) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	_, err := p.send(ctx, method, path, query, in, out)
	return err
}

// send is do that also returns the response headers.
func (p *Provider) send(ctx context.Context, method, path string, query url.Values, in, out any) (http.Header, error) {
	token, err := p.token()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read the token from setting %s: %v", hosting.ErrUnauthorized, tokenSetting, err)
	}
	if token == "" {
		return nil, fmt.Errorf("%w: setting %s resolved to an empty token", hosting.ErrUnauthorized, tokenSetting)
	}

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("github: encoding request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	target := p.apiURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("github: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", p.userAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: %s %s: %s", hosting.ErrUnavailable, method, path, redact(errors.Unwrap(err), err, token))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: reading response: %s", hosting.ErrUnavailable, redact(nil, err, token))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp, raw, token)
	}
	if out == nil {
		return resp.Header, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: unreadable response from GitHub", hosting.ErrUnavailable)
	}
	return resp.Header, nil
}

// redact renders err without the token, preferring the unwrapped cause, which omits the URL.
func redact(cause, err error, token string) string {
	if cause == nil {
		cause = err
	}
	return strings.ReplaceAll(cause.Error(), token, "[redacted]")
}

type apiError struct {
	Message string `json:"message"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// message returns GitHub's own explanation if the body has one, else the raw body.
func (e apiError) message(raw []byte) string {
	parts := []string{}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	for _, d := range e.Errors {
		if d.Message != "" {
			parts = append(parts, d.Message)
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(string(raw))
	}
	return strings.Join(parts, "; ")
}

func statusError(resp *http.Response, raw []byte, token string) error {
	var ae apiError
	_ = json.Unmarshal(raw, &ae)
	msg := truncate(strings.ReplaceAll(ae.message(raw), token, "[redacted]"), maxErrorChars)
	status := resp.StatusCode

	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		if rl, ok := rateLimit(resp, status); ok {
			return rl
		}
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: GitHub answered %d: %s; check the token in setting %s", hosting.ErrUnauthorized, status, msg, tokenSetting)
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: GitHub answered 404: %s (GitHub also reports a repository the token cannot see as not found)", hosting.ErrNotFound, msg)
	case status == http.StatusUnprocessableEntity:
		if strings.Contains(strings.ToLower(ae.message(raw)), "already exists") {
			return fmt.Errorf("%w: %s", hosting.ErrConflict, msg)
		}
		return fmt.Errorf("%w: %s", hosting.ErrInvalid, msg)
	case status >= 500:
		return fmt.Errorf("%w: GitHub answered %d: %s", hosting.ErrUnavailable, status, msg)
	case status >= 400:
		return fmt.Errorf("%w: GitHub answered %d: %s", hosting.ErrInvalid, status, msg)
	}
	return fmt.Errorf("%w: unexpected GitHub status %d: %s", hosting.ErrUnavailable, status, msg)
}

func rateLimit(resp *http.Response, status int) (*hosting.RateLimitError, bool) {
	h := resp.Header
	retryAfter := h.Get("Retry-After")
	exhausted := h.Get("X-RateLimit-Remaining") == "0"
	if !exhausted && retryAfter == "" && status != http.StatusTooManyRequests {
		return nil, false
	}
	e := &hosting.RateLimitError{}
	if secs, err := strconv.ParseInt(retryAfter, 10, 64); err == nil && secs >= 0 {
		e.ResetAt = time.Now().Add(time.Duration(secs) * time.Second)
	} else if t, err := http.ParseTime(retryAfter); err == nil {
		e.ResetAt = t
	} else if secs, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && secs > 0 {
		e.ResetAt = time.Unix(secs, 0)
	}
	return e, true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
