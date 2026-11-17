// Package filelock is an advisory exclusive lock on a file that the operating system drops when the process ends.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrHeld means another process holds the lock.
var ErrHeld = errors.New("lock held")

// Lock is a held lock.
type Lock struct {
	f       *os.File
	path    string
	created bool
}

// Acquire takes the lock on path without waiting, creating the file and its directory; it returns ErrHeld when another holds it.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrHeld) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{f: f, path: path, created: errors.Is(statErr, os.ErrNotExist)}, nil
}

// Release unlocks and closes the file; it is safe to call more than once.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// Discard releases the lock and removes the file when this process made it.
func (l *Lock) Discard() {
	if l == nil {
		return
	}
	l.Release()
	if l.created {
		_ = os.Remove(l.path)
	}
}
