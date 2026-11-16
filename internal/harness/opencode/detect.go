package opencode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/harness"
)

const (
	commandTimeout = 5 * time.Second
	rawLimit       = 80
	minMajor       = 2

	otherProject = `the opencode command on the path does not support "run --format"; it looks like a different project with the same name`
)

var versionPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]*)?)`)

// runCommand is replaceable in tests. Standard input is empty; merged also
// captures standard error.
var runCommand = func(ctx context.Context, path string, merged bool, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if merged {
		cmd.Stderr = &out
	}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return out.String(), err
}

func (adapter) Detect(ctx context.Context, root string) harness.Detection {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return detectFiles(root)
	}

	versionCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	out, err := runCommand(versionCtx, path, false, "--version")
	version, detail := "", "opencode command at "+path
	switch {
	case err != nil:
		detail = "could not read the version: " + runFailure(versionCtx, err)
	default:
		if m := versionPattern.FindStringSubmatch(out); m != nil {
			version = m[1]
		} else {
			detail = "could not read the version: unrecognized output"
			if raw := firstLine(out); raw != "" {
				detail += fmt.Sprintf(" %q", raw)
			}
		}
	}
	cancel()

	d := harness.Detection{Found: true, Version: version, Detail: detail}

	helpCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	help, helpErr := runCommand(helpCtx, path, true, "run", "--help")
	switch {
	case helpErr != nil:
		d.Detail += fmt.Sprintf("; could not check %q: %s", "run --format", runFailure(helpCtx, helpErr))
	case !strings.Contains(help, "--format"):
		d.Unsupported = otherProject
		return d
	}
	if version != "" {
		if major, convErr := strconv.Atoi(strings.SplitN(version, ".", 2)[0]); convErr == nil && major < minMajor {
			d.Unsupported = fmt.Sprintf("ralph supports OpenCode %d and later (found %s)", minMajor, version)
		}
	}
	return d
}

func detectFiles(root string) harness.Detection {
	for _, n := range []string{".opencode", "opencode.json", "opencode.jsonc"} {
		info, err := os.Stat(filepath.Join(root, n))
		if err == nil && info.IsDir() == (n == ".opencode") {
			return harness.Detection{Found: true, Detail: "no opencode command on the path; found " + n}
		}
	}
	return harness.Detection{Detail: "no opencode command on the path and no .opencode directory or opencode.json file"}
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
