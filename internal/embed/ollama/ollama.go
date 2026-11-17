// Package ollama implements embed.Provider for an Ollama server.
package ollama

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
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/embed/core"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "ralph"
	batchSize        = 16
	maxResponseBytes = 64 << 20
	maxMessageChars  = 200
	providerName     = "ollama"
)

// Config configures a Provider.
type Config struct {
	URL        string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
	UserAgent  string
}

// Provider embeds text with one model of one Ollama server.
type Provider struct {
	base      string
	model     string
	timeout   time.Duration
	client    *http.Client
	userAgent string
}

// New validates cfg and returns a Provider; it makes no request.
func New(cfg Config) (*Provider, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("ollama: the URL must be an http or https address with a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("ollama: the URL must use http or https")
	}
	if u.User != nil {
		return nil, errors.New("ollama: the URL must not contain credentials")
	}
	if cfg.Model == "" {
		return nil, errors.New("ollama: the model must not be empty")
	}
	p := &Provider{
		base:      strings.TrimSuffix(cfg.URL, "/"),
		model:     cfg.Model,
		timeout:   cfg.Timeout,
		client:    cfg.HTTPClient,
		userAgent: cfg.UserAgent,
	}
	if p.timeout <= 0 {
		p.timeout = defaultTimeout
	}
	if p.client == nil {
		p.client = &http.Client{}
	}
	if p.userAgent == "" {
		p.userAgent = defaultUserAgent
	}
	return p, nil
}

type embedRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns one normalized vector per text, requesting at most batchSize texts at a time.
func (p *Provider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	dims := 0
	for start := 0; start < len(texts); start += batchSize {
		batch := texts[start:min(start+batchSize, len(texts))]
		var resp embedResponse
		if err := p.call(ctx, http.MethodPost, "/api/embed", embedRequest{Model: p.model, Input: batch}, &resp); err != nil {
			return nil, err
		}
		if len(resp.Embeddings) != len(batch) {
			return nil, fmt.Errorf("%w: the service returned %d vectors for %d texts", core.ErrInvalid, len(resp.Embeddings), len(batch))
		}
		for _, v := range resp.Embeddings {
			if dims == 0 {
				dims = len(v)
			}
			if len(v) == 0 || len(v) != dims {
				return nil, fmt.Errorf("%w: the service returned vectors of different or zero lengths", core.ErrInvalid)
			}
			n, err := core.Normalize(v)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
	}
	return out, nil
}

type tagsResponse struct {
	Models []struct {
		Name   string `json:"name"`
		Model  string `json:"model"`
		Digest string `json:"digest"`
	} `json:"models"`
}

type showResponse struct {
	ModelInfo map[string]json.RawMessage `json:"model_info"`
}

// Describe reports the model's digest, vector length and context size.
func (p *Provider) Describe(ctx context.Context) (core.Info, error) {
	var tags tagsResponse
	if err := p.call(ctx, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		return core.Info{}, err
	}
	want := p.model
	if !strings.Contains(want, ":") {
		want += ":latest"
	}
	found := false
	info := core.Info{Provider: providerName, Model: p.model}
	for _, m := range tags.Models {
		if m.Name == want || m.Model == want {
			info.Digest = m.Digest
			found = true
			break
		}
	}
	if !found {
		return core.Info{}, p.notInstalled()
	}
	var show showResponse
	if err := p.call(ctx, http.MethodPost, "/api/show", map[string]string{"model": p.model}, &show); err != nil {
		return core.Info{}, err
	}
	for k, raw := range show.ModelInfo {
		var n float64
		if json.Unmarshal(raw, &n) != nil || n < 0 || n != float64(int(n)) {
			continue
		}
		switch {
		case strings.HasSuffix(k, ".embedding_length"):
			info.Dimensions = int(n)
		case strings.HasSuffix(k, ".context_length"):
			info.ContextTokens = int(n)
		}
	}
	return info, nil
}

func (p *Provider) notInstalled() error {
	return fmt.Errorf("%w: model %s is not installed in Ollama: run ollama pull %s", core.ErrModelNotFound, p.model, p.model)
}

// call sends one request and decodes a successful JSON response into out.
func (p *Provider) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: cannot encode the request", core.ErrInvalid)
		}
		rd = bytes.NewReader(b)
	}
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, p.base+path, rd)
	if err != nil {
		return fmt.Errorf("%w: cannot build the request", core.ErrUnavailable)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return p.transportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return p.transportError(ctx, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response larger than 64 MiB", core.ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return p.statusError(resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: unreadable response", core.ErrUnavailable)
	}
	return nil
}

func (p *Provider) transportError(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return fmt.Errorf("%w: timed out after %s", core.ErrUnavailable, p.timeout)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w: connection refused", core.ErrUnavailable)
	case errors.Is(err, syscall.ECONNRESET):
		return fmt.Errorf("%w: connection reset", core.ErrUnavailable)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: connection closed unexpectedly", core.ErrUnavailable)
	}
	return fmt.Errorf("%w: request failed", core.ErrUnavailable)
}

func (p *Provider) statusError(status int, body []byte) error {
	msg := serviceMessage(body)
	switch {
	case status == http.StatusNotFound && strings.Contains(strings.ToLower(msg), "model"):
		return p.notInstalled()
	case status == http.StatusBadRequest:
		if msg == "" {
			msg = "the service refused the request"
		}
		return fmt.Errorf("%w: %s", core.ErrInvalid, msg)
	}
	if msg != "" {
		return fmt.Errorf("%w: HTTP %d: %s", core.ErrUnavailable, status, msg)
	}
	return fmt.Errorf("%w: HTTP %d", core.ErrUnavailable, status)
}

// serviceMessage extracts the error text of a response, limited in length.
func serviceMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	msg := ""
	if json.Unmarshal(body, &e) == nil {
		msg = e.Error
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if utf8.RuneCountInString(msg) > maxMessageChars {
		msg = string([]rune(msg)[:maxMessageChars-1]) + "…"
	}
	return msg
}
