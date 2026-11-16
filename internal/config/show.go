package config

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

const (
	unsetText = "(unset)"
	noneText  = "-"
)

// WriteText prints every registered setting as a table. File sources inside
// displayRoot are shown relative to it.
func WriteText(w io.Writer, cfg *Config, displayRoot string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SETTING\tVALUE\tLAYER\tSOURCE"); err != nil {
		return err
	}
	for _, s := range cfg.registry.Settings() {
		value, layer, source := unsetText, noneText, noneText
		if v, ok := cfg.values[s.Path]; ok {
			value, layer, source = v.Value, v.Layer.String(), sourceText(v, displayRoot)
			if s.Kind == KindSecret {
				value += " " + resolvesText(cfg.SecretResolves(s.Path))
			}
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Path, value, layer, source); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type showEntry struct {
	Setting  string  `json:"setting"`
	Value    *string `json:"value"`
	Layer    *string `json:"layer"`
	Source   string  `json:"source"`
	Resolves *bool   `json:"resolves,omitempty"`
}

// WriteJSON prints every registered setting as a JSON array.
func WriteJSON(w io.Writer, cfg *Config) error {
	settings := cfg.registry.Settings()
	entries := make([]showEntry, 0, len(settings))
	for _, s := range settings {
		e := showEntry{Setting: s.Path, Source: noneText}
		if v, ok := cfg.values[s.Path]; ok {
			value, layer := v.Value, v.Layer.String()
			e.Value, e.Layer, e.Source = &value, &layer, sourceText(v, "")
		}
		if s.Kind == KindSecret {
			resolves := false
			if e.Value != nil {
				resolves = cfg.SecretResolves(s.Path)
			}
			e.Resolves = &resolves
		}
		entries = append(entries, e)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

func resolvesText(ok bool) string {
	if ok {
		return "(resolves)"
	}
	return "(does not resolve)"
}

func sourceText(v Value, displayRoot string) string {
	switch {
	case v.Source.File != "":
		file := v.Source.File
		if displayRoot != "" {
			if rel, err := filepath.Rel(displayRoot, file); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				file = rel
			}
		}
		return fmt.Sprintf("%s:%d", file, v.Source.Line)
	case v.Source.Name != "":
		return v.Source.Name
	default:
		return noneText
	}
}
