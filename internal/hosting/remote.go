package hosting

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Remote identifies a repository on a host.
type Remote struct {
	Host  string
	Owner string
	Repo  string
}

var errBadRemote = errors.New("cannot parse remote URL")

// ParseRemote parses a git remote URL. Credentials are dropped and never
// appear in the result or in an error.
func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Remote{}, fmt.Errorf("%w: empty", errBadRemote)
	}
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Remote{}, fmt.Errorf("%w: malformed", errBadRemote)
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return Remote{}, fmt.Errorf("%w: unsupported scheme %q", errBadRemote, u.Scheme)
		}
		host, path = u.Hostname(), u.Path
	} else {
		at := strings.LastIndex(raw, "@")
		rest := raw[at+1:]
		h, p, ok := strings.Cut(rest, ":")
		if !ok {
			return Remote{}, fmt.Errorf("%w: unrecognized form", errBadRemote)
		}
		host, path = h, p
	}
	host = strings.ToLower(host)
	if host == "" {
		return Remote{}, fmt.Errorf("%w: no host", errBadRemote)
	}
	var segments []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) < 2 {
		return Remote{}, fmt.Errorf("%w: path needs an owner and a repository", errBadRemote)
	}
	repo := strings.TrimSuffix(segments[len(segments)-1], ".git")
	owner := strings.Join(segments[:len(segments)-1], "/")
	if repo == "" {
		return Remote{}, fmt.Errorf("%w: path needs an owner and a repository", errBadRemote)
	}
	return Remote{Host: host, Owner: owner, Repo: repo}, nil
}

// DetectRemote reads the URL of the named remote in dir and parses it.
func DetectRemote(ctx context.Context, dir, name string) (Remote, error) {
	out, err := gitOutput(ctx, dir, "remote", "get-url", name)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return Remote{}, fmt.Errorf("remote %q: %w", name, ErrNoRemote)
		}
		return Remote{}, fmt.Errorf("reading remote %q: %w", name, err)
	}
	r, err := ParseRemote(out)
	if err != nil {
		return Remote{}, fmt.Errorf("remote %q: %w", name, err)
	}
	return r, nil
}

// BaseBranch returns the branch pull requests target: the configured value,
// else the remote's default branch, else main.
func BaseBranch(ctx context.Context, dir, remote, configured string) string {
	if configured != "" {
		return configured
	}
	out, err := gitOutput(ctx, dir, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if err == nil {
		if b := strings.TrimPrefix(out, remote+"/"); b != "" {
			return b
		}
	}
	return "main"
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
