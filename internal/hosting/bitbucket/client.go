// Package bitbucket implements hosting.Provider for Bitbucket Cloud over its REST API.
package bitbucket

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
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const (
	defaultAPIURL    = "https://api.bitbucket.org/2.0"
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "ralph"
	maxErrorChars    = 300
	maxResponseBytes = 10 << 20
	maxPages         = 50
	tokenSetting     = "bitbucket.token"
	userSetting      = "bitbucket.user"
)

// Config configures a Provider.
type Config struct {
	APIURL    string
	Workspace string
	Repo      string
	// User is the account email; when set the token is sent with Basic authentication, else as a Bearer token.
	User string
	// Token is called for every request so a changed secret is picked up.
	Token      func() (string, error)
	Timeout    time.Duration
	HTTPClient *http.Client
	UserAgent  string
}

// Provider talks to one Bitbucket Cloud repository.
type Provider struct {
	apiURL    string
	apiBase   *url.URL
	workspace string
	repo      string
	user      string
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
	base, err := checkAPIURL(apiURL)
	if err != nil {
		return nil, err
	}
	if cfg.Workspace == "" || cfg.Repo == "" {
		return nil, errors.New("bitbucket: workspace and repository must be set")
	}
	if cfg.Token == nil {
		return nil, errors.New("bitbucket: a token function is required")
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
	return &Provider{
		apiURL: apiURL, apiBase: base, workspace: cfg.Workspace, repo: cfg.Repo, user: cfg.User,
		token: cfg.Token, client: client, userAgent: ua,
	}, nil
}

func checkAPIURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("bitbucket: API URL is not a valid URL")
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		if isLoopback(u.Hostname()) {
			return u, nil
		}
	}
	return nil, errors.New("bitbucket: API URL must use https unless its host is a loopback address")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *Provider) repoPath(suffix string) string {
	return "/repositories/" + url.PathEscape(p.workspace) + "/" + url.PathEscape(p.repo) + suffix
}

func (p *Provider) target(path string, query url.Values) string {
	t := p.apiURL + path
	if len(query) > 0 {
		t += "?" + query.Encode()
	}
	return t
}

// do sends one request to an API path and decodes a 2xx JSON response into out.
func (p *Provider) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	return p.send(ctx, method, p.target(path, query), in, out)
}

// send sends one request to a full URL on the configured API host.
func (p *Provider) send(ctx context.Context, method, target string, in, out any) error {
	token, err := p.token()
	if err != nil {
		return hosting.NewCredentialError(tokenSetting, fmt.Sprintf("cannot read the token from setting %s: %v", tokenSetting, err))
	}
	if token == "" {
		return hosting.NewCredentialError(tokenSetting, fmt.Sprintf("setting %s resolved to an empty token", tokenSetting))
	}

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("bitbucket: encoding request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("bitbucket: building request: %w", err)
	}
	if p.user != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.user+":"+token)))
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", p.userAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %s %s: %s", hosting.ErrUnavailable, method, req.URL.Path, p.redact(errors.Unwrap(err), err, token))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: reading response: %s", hosting.ErrUnavailable, p.redact(nil, err, token))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return p.statusError(resp, raw, token)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: unreadable response from Bitbucket", hosting.ErrUnavailable)
	}
	return nil
}

func (p *Provider) scrub(s, token string) string {
	s = strings.ReplaceAll(s, token, "[redacted]")
	if p.user != "" {
		s = strings.ReplaceAll(s, p.user, "[redacted]")
	}
	return s
}

// redact renders err without the token or user, preferring the unwrapped cause, which omits the URL.
func (p *Provider) redact(cause, err error, token string) string {
	if cause == nil {
		cause = err
	}
	return p.scrub(cause.Error(), token)
}

type apiError struct {
	Error struct {
		Message string         `json:"message"`
		Detail  string         `json:"detail"`
		Fields  map[string]any `json:"fields"`
	} `json:"error"`
}

// message returns Bitbucket's own explanation if the body has one, else the raw body.
func (e apiError) message(raw []byte) string {
	msg := e.Error.Message
	if msg == "" {
		return strings.TrimSpace(string(raw))
	}
	return msg
}

// fieldErrors flattens field errors as "field: message", in field order.
func (e apiError) fieldErrors() []string {
	keys := make([]string, 0, len(e.Error.Fields))
	for k := range e.Error.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		switch v := e.Error.Fields[k].(type) {
		case string:
			out = append(out, k+": "+v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, k+": "+s)
				} else if b, err := json.Marshal(item); err == nil {
					out = append(out, k+": "+string(b))
				}
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				out = append(out, k+": "+string(b))
			}
		}
	}
	return out
}

func (p *Provider) statusError(resp *http.Response, raw []byte, token string) error {
	var ae apiError
	_ = json.Unmarshal(raw, &ae)
	full := ae.message(raw)
	msg := truncate(p.scrub(full, token), maxErrorChars)
	status := resp.StatusCode

	switch {
	case status == http.StatusUnauthorized:
		hint := fmt.Sprintf("check setting %s", tokenSetting)
		if p.user != "" {
			hint += " and setting " + userSetting
		} else {
			hint += "; " + userSetting + " is not set, so the token was sent as a Bearer access token (set it to the account email for an API token)"
		}
		return hosting.NewCredentialError(tokenSetting, fmt.Sprintf("Bitbucket answered 401: %s; %s", msg, hint))
	case status == http.StatusForbidden:
		return hosting.NewCredentialError(tokenSetting, fmt.Sprintf("Bitbucket answered 403: %s; the token lacks permission: it needs pull requests read and write (setting %s)", msg, tokenSetting))
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: Bitbucket answered 404: %s (Bitbucket also reports a repository the token cannot see as not found)", hosting.ErrNotFound, msg)
	case status == http.StatusBadRequest || status == http.StatusConflict:
		lower := strings.ToLower(full)
		if strings.Contains(lower, "already") && strings.Contains(lower, "pull request") {
			return fmt.Errorf("%w: %s", hosting.ErrConflict, msg)
		}
		if status == http.StatusBadRequest {
			if fields := ae.fieldErrors(); len(fields) > 0 {
				msg += " (" + truncate(p.scrub(strings.Join(fields, "; "), token), maxErrorChars) + ")"
			}
			return fmt.Errorf("%w: %s", hosting.ErrInvalid, msg)
		}
		return fmt.Errorf("%w: Bitbucket answered %d: %s", hosting.ErrInvalid, status, msg)
	case status == http.StatusTooManyRequests:
		e := &hosting.RateLimitError{}
		if secs, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil && secs >= 0 {
			e.ResetAt = time.Now().Add(time.Duration(secs) * time.Second)
		}
		return e
	case status >= 500:
		return fmt.Errorf("%w: Bitbucket answered %d: %s", hosting.ErrUnavailable, status, msg)
	case status >= 400:
		return fmt.Errorf("%w: Bitbucket answered %d: %s", hosting.ErrInvalid, status, msg)
	}
	return fmt.Errorf("%w: unexpected Bitbucket status %d: %s", hosting.ErrUnavailable, status, msg)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// collect reads a paginated result from path, following next links.
func collect[T any](ctx context.Context, p *Provider, path string, query url.Values) ([]T, error) {
	var all []T
	target := p.target(path, query)
	for n := 0; n < maxPages; n++ {
		var pg page[T]
		if err := p.send(ctx, http.MethodGet, target, nil, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg.Values...)
		if pg.Next == "" {
			return all, nil
		}
		if err := p.checkNext(pg.Next); err != nil {
			return nil, err
		}
		target = pg.Next
	}
	return nil, fmt.Errorf("%w: Bitbucket result has more than %d pages", hosting.ErrUnavailable, maxPages)
}

func (p *Provider) checkNext(next string) error {
	u, err := url.Parse(next)
	if err != nil || !strings.EqualFold(u.Scheme, p.apiBase.Scheme) || !strings.EqualFold(u.Host, p.apiBase.Host) {
		return fmt.Errorf("%w: refusing to follow a next page link that leaves the configured API address", hosting.ErrUnavailable)
	}
	return nil
}
