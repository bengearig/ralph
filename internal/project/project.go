// Package project locates a ralph project on disk.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DirName     = ".ralph"
	ProjectFile = "project.yaml"
	LocalFile   = "project.local.yaml"

	// IntegrationFile and IntegrationLocalFile record what ralph installed for
	// harnesses; the local one is not committed.
	IntegrationFile      = "integration.json"
	IntegrationLocalFile = "integration.local.json"

	StoreFile     = "store.db"
	BackupDirName = "backups"

	// RunsDirName holds the loop's run records; LoopLockFile is its lock.
	RunsDirName  = "runs"
	LoopLockFile = "loop.lock"

	// IndexFile is the derived vector index for semantic search.
	IndexFile = "index.db"
	// IndexRebuildFile is the new index a rebuild builds beside the old one; IndexLockFile is the lock of an index run.
	IndexRebuildFile = "index.rebuild.db"
	IndexLockFile    = "index.lock"

	// TicketsLockFile is the lock of a ticket sync pass.
	TicketsLockFile = "tickets.lock"
)

var (
	ErrNotRepository  = errors.New("not a git repository")
	ErrBareRepository = errors.New("bare git repository has no working tree")
)

// FindRoot returns the primary worktree of the repository containing dir, so
// every linked worktree resolves to the same project root.
func FindRoot(dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("project: directory %q: %w", dir, err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("project: git is required but was not found on PATH: %w", err)
	}

	cmd := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "not a git repository") {
			return "", fmt.Errorf("project: %q: %w", dir, ErrNotRepository)
		}
		return "", fmt.Errorf("project: git worktree list in %q: %w: %s", dir, err, msg)
	}

	var root string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			if strings.HasPrefix(path, `"`) {
				unquoted, err := strconv.Unquote(path)
				if err != nil {
					return "", fmt.Errorf("project: parse worktree path %s: %w", path, err)
				}
				path = unquoted
			}
			root = path
		} else if line == "bare" {
			return "", fmt.Errorf("project: %q: %w", root, ErrBareRepository)
		}
	}
	if root == "" {
		return "", fmt.Errorf("project: git worktree list in %q printed no worktree", dir)
	}
	return root, nil
}

// RalphDir returns the ralph directory of the project at root; it need not exist.
func RalphDir(root string) string {
	return filepath.Join(root, DirName)
}

// StorePath returns the database file of the project at root.
func StorePath(root string) string {
	return filepath.Join(RalphDir(root), StoreFile)
}

// IndexPath returns the vector index file of the project at root.
func IndexPath(root string) string {
	return filepath.Join(RalphDir(root), IndexFile)
}

// BackupsDir returns the directory holding database backups.
func BackupsDir(root string) string {
	return filepath.Join(RalphDir(root), BackupDirName)
}
