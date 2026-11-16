package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ignoreFile = ".gitignore"

const ignoreHeader = "# Local ralph files. Written by ralph init."

// LocalIgnoreEntries returns the ignore rules, relative to the ralph
// directory, for files that must not be committed.
func LocalIgnoreEntries() []string {
	return []string{
		"/" + StoreFile + "*",
		"/" + BackupDirName + "/",
		"/" + LocalFile,
	}
}

// EnsureIgnore adds the entries missing from <root>/.ralph/.gitignore,
// creating the file if needed, and returns the entries it added.
func EnsureIgnore(root string, entries []string) (added []string, err error) {
	path := filepath.Join(RalphDir(root), ignoreFile)
	existing, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return nil, fmt.Errorf("project: read %s: %w", path, err)
	}

	present := make(map[string]bool)
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimRight(line, " \r")] = true
	}
	for _, e := range entries {
		if !present[e] {
			present[e] = true
			added = append(added, e)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}

	var b strings.Builder
	b.Write(existing)
	if missing {
		b.WriteString(ignoreHeader + "\n")
	} else if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		b.WriteString("\n")
	}
	for _, e := range added {
		b.WriteString(e + "\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("project: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return nil, fmt.Errorf("project: write %s: %w", path, err)
	}
	return added, nil
}
