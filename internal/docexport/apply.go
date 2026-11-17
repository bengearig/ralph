package docexport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const tempPrefix = ".ralph-export-"

// Apply writes the created and updated files, then removes the stale ones.
// A failure on one file is recorded and the rest continue; the error joins them.
func (p Plan) Apply() (Result, error) {
	var res Result
	var errs []error
	var writes, removals []Change
	for _, c := range p.Changes {
		switch c.Action {
		case ActionCreated, ActionUpdated:
			writes = append(writes, c)
		case ActionRemoved:
			removals = append(removals, c)
		case ActionUnchanged:
			res.Unchanged++
		case ActionConflict:
			res.Conflicts = append(res.Conflicts, c)
		}
	}
	if len(writes) > 0 {
		if err := os.MkdirAll(p.Dir, 0o755); err != nil {
			errs = append(errs, fmt.Errorf("create export directory %s: %w", p.Dir, err))
			writes = nil
		}
	}
	for _, c := range writes {
		path := filepath.Join(p.Dir, c.Name)
		if err := writeAtomic(p.Dir, c.Name, p.contents[c.Name]); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", path, err))
			continue
		}
		if c.Action == ActionCreated {
			res.Created++
		} else {
			res.Updated++
		}
	}
	for _, c := range removals {
		path := filepath.Join(p.Dir, c.Name)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
			continue
		}
		res.Removed++
	}
	return res, errors.Join(errs...)
}

func writeAtomic(dir, name string, content []byte) (err error) {
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err = tmp.Write(content); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}
