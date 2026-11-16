package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const manifestVersion = 1

// Entry records one artifact ralph wrote.
type Entry struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	KeyPath []string `json:"keyPath,omitempty"`
	SHA256  string   `json:"sha256"`
	Adapter string   `json:"adapter"`
	Ralph   string   `json:"ralph"`
}

// Manifest is what ralph wrote and the artifacts the user has taken over.
type Manifest struct {
	Version    int      `json:"version"`
	Customized []string `json:"customized"`
	Entries    []Entry  `json:"entries"`
}

// LoadManifest reads the manifest at path; a missing file is an empty manifest.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{Version: manifestVersion}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("integration: read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("integration: %s is not a valid manifest: %w", path, err)
	}
	if m.Version == 0 {
		m.Version = manifestVersion
	}
	if m.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("integration: %s has unsupported version %d", path, m.Version)
	}
	return m, nil
}

// Save writes the manifest atomically with entries sorted by ID.
func (m Manifest) Save(path string) error {
	m.Version = manifestVersion
	if m.Customized == nil {
		m.Customized = []string{}
	}
	entries := append([]Entry{}, m.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	m.Entries = entries

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("integration: encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("integration: create %s: %w", filepath.Dir(path), err)
	}
	return writeAtomic(path, buf.Bytes(), 0o644)
}

func (m Manifest) find(id string) (Entry, bool) {
	for _, e := range m.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func (m *Manifest) record(e Entry) {
	for i := range m.Entries {
		if m.Entries[i].ID == e.ID {
			m.Entries[i] = e
			return
		}
	}
	m.Entries = append(m.Entries, e)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ralph-tmp-*")
	if err != nil {
		return fmt.Errorf("integration: create temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("integration: write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("integration: write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("integration: write %s: %w", path, err)
	}
	return nil
}
