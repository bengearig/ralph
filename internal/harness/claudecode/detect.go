package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/harness"
)

const (
	versionTimeout = 5 * time.Second
	rawLimit       = 80
)

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]*)?`)

// runVersion is replaceable in tests.
var runVersion = func(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, path, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return out.String(), err
}

func (adapter) Detect(ctx context.Context, root string) harness.Detection {
	path, err := exec.LookPath("claude")
	if err != nil {
		if info, statErr := os.Stat(filepath.Join(root, ".claude")); statErr == nil && info.IsDir() {
			return harness.Detection{Found: true, Detail: "no claude command on the path; found a .claude directory"}
		}
		return harness.Detection{Detail: "no claude command on the path and no .claude directory"}
	}

	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := runVersion(ctx, path)
	if err != nil {
		return harness.Detection{Found: true, Detail: "could not read the version: " + runFailure(ctx, err)}
	}
	if version := versionPattern.FindString(out); version != "" {
		return harness.Detection{Found: true, Version: version, Detail: "claude command at " + path}
	}
	detail := "could not read the version: unrecognized output"
	if raw := firstLine(out); raw != "" {
		detail += fmt.Sprintf(" %q", raw)
	}
	return harness.Detection{Found: true, Detail: detail}
}

func runFailure(ctx context.Context, err error) string {
	var exit *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	case errors.As(err, &exit):
		return fmt.Sprintf("exited with status %d", exit.ExitCode())
	default:
		return strings.SplitN(err.Error(), "\n", 2)[0]
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > rawLimit {
		line = string(r[:rawLimit])
	}
	return line
}
