package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/bengearig/ralph/internal/project"
)

const recordFileName = "record.json"

var sliceIDPattern = regexp.MustCompile(`^S-[0-9]+$`)

// RecordDir is one run record directory under .ralph/runs.
type RecordDir struct {
	// Path is relative to the project root, with forward slashes.
	Path    string
	Slice   string
	Task    string
	Started time.Time
	// Bytes is the total size of the files in the directory.
	Bytes int64
}

// PurgeSelection chooses which records PurgeRecords takes; both set means both must match.
type PurgeSelection struct {
	// OlderThan matches records started longer ago than this; zero matches every age.
	OlderThan time.Duration
	// Slice is a slice ID in upper case; empty matches every slice.
	Slice string
}

// PurgeResult is what PurgeRecords selected and removed.
type PurgeResult struct {
	Records []RecordDir
	Removed int
	// Bytes is the size of the records removed, or of those selected on a dry run.
	Bytes int64
}

// ListRecords returns the record directories that hold a readable record.json
// with a valid started_at, oldest first. Anything else under .ralph/runs is ignored.
func ListRecords(root string) ([]RecordDir, error) {
	runs := filepath.Join(project.RalphDir(root), runsDirName)
	if info, err := os.Lstat(runs); err != nil || !info.IsDir() {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the run records: %w", err)
	}
	slices, err := os.ReadDir(runs)
	if err != nil {
		return nil, fmt.Errorf("read the run records: %w", err)
	}
	var out []RecordDir
	for _, s := range slices {
		if !s.IsDir() {
			continue
		}
		dirs, err := os.ReadDir(filepath.Join(runs, s.Name()))
		if err != nil {
			return nil, fmt.Errorf("read the run records: %w", err)
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			full := filepath.Join(runs, s.Name(), d.Name())
			rec, ok := readRecordDir(full)
			if !ok {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return nil, fmt.Errorf("read the run records: %w", err)
			}
			rec.Path = filepath.ToSlash(rel)
			if rec.Slice == "" {
				rec.Slice = s.Name()
			}
			rec.Bytes = dirBytes(full)
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Started.Equal(out[j].Started) {
			return out[i].Started.Before(out[j].Started)
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func readRecordDir(dir string) (RecordDir, bool) {
	file := filepath.Join(dir, recordFileName)
	if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
		return RecordDir{}, false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return RecordDir{}, false
	}
	var f struct {
		Slice     string `json:"slice"`
		Task      string `json:"task"`
		StartedAt string `json:"started_at"`
	}
	if json.Unmarshal(data, &f) != nil {
		return RecordDir{}, false
	}
	started, err := time.Parse(time.RFC3339, f.StartedAt)
	if err != nil {
		return RecordDir{}, false
	}
	return RecordDir{Slice: f.Slice, Task: f.Task, Started: started}, true
}

func dirBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// PurgeRecords removes the records sel selects, or only lists them when dry,
// then removes a slice directory left empty. It tries every record before
// reporting a removal failure.
func PurgeRecords(root string, sel PurgeSelection, dry bool, now time.Time) (PurgeResult, error) {
	var res PurgeResult
	if sel.Slice != "" && !sliceIDPattern.MatchString(sel.Slice) {
		return res, fmt.Errorf("%q is not a slice ID such as S-1", sel.Slice)
	}
	all, err := ListRecords(root)
	if err != nil {
		return res, err
	}
	for _, r := range all {
		if sel.Slice != "" && r.Slice != sel.Slice {
			continue
		}
		if sel.OlderThan > 0 && now.Sub(r.Started) <= sel.OlderThan {
			continue
		}
		res.Records = append(res.Records, r)
	}
	if dry {
		for _, r := range res.Records {
			res.Bytes += r.Bytes
		}
		return res, nil
	}
	var errs []error
	parents := map[string]bool{}
	for _, r := range res.Records {
		full := filepath.Join(root, filepath.FromSlash(r.Path))
		if err := os.RemoveAll(full); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", r.Path, err))
			continue
		}
		res.Removed++
		res.Bytes += r.Bytes
		parents[filepath.Dir(full)] = true
	}
	for dir := range parents {
		_ = os.Remove(dir)
	}
	return res, errors.Join(errs...)
}
