package workflow

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Override kinds.
const (
	OverrideReplace = "replace"
	OverrideAppend  = "append"
)

// Override records one project file that changed a built-in instruction.
type Override struct {
	Name string
	// File is the file name inside the workflow directory.
	File string
	// Kind is OverrideReplace or OverrideAppend.
	Kind string
}

// Load returns the built-in instructions with the project's overrides applied
// and the overrides used, ordered by instruction name, replacements first.
// projectDir is the project's .ralph/workflow directory; a missing directory
// means no overrides.
func Load(projectDir string) ([]Instruction, []Override, error) {
	instructions, err := All()
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(projectDir)
	if errors.Is(err, fs.ErrNotExist) {
		return instructions, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading workflow overrides: %w", err)
	}

	index := map[string]int{}
	for i, in := range instructions {
		index[in.Name] = i
	}
	type files struct{ replace, appendFile string }
	found := map[string]*files{}
	for _, e := range entries {
		file := e.Name()
		if strings.HasPrefix(file, ".") || !strings.HasSuffix(file, ".md") {
			continue
		}
		if info, err := os.Stat(filepath.Join(projectDir, file)); err != nil || info.IsDir() {
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, nil, fmt.Errorf("%s: %w", file, err)
			}
			continue
		}
		name, kind := strings.TrimSuffix(file, ".md"), OverrideReplace
		if base, ok := strings.CutSuffix(name, ".append"); ok {
			name, kind = base, OverrideAppend
		}
		if _, ok := index[name]; !ok {
			return nil, nil, fmt.Errorf("%s: no built-in instruction named %s", file, name)
		}
		f := found[name]
		if f == nil {
			f = &files{}
			found[name] = f
		}
		if kind == OverrideReplace {
			f.replace = file
		} else {
			f.appendFile = file
		}
	}

	var used []Override
	for i, in := range instructions {
		f := found[in.Name]
		if f == nil {
			continue
		}
		if f.replace != "" {
			data, err := os.ReadFile(filepath.Join(projectDir, f.replace))
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f.replace, err)
			}
			in, err = replaceInstruction(in, f.replace, string(data))
			if err != nil {
				return nil, nil, err
			}
			used = append(used, Override{Name: in.Name, File: f.replace, Kind: OverrideReplace})
		}
		if f.appendFile != "" {
			data, err := os.ReadFile(filepath.Join(projectDir, f.appendFile))
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f.appendFile, err)
			}
			in, err = appendToInstruction(in, f.appendFile, string(data))
			if err != nil {
				return nil, nil, err
			}
			used = append(used, Override{Name: in.Name, File: f.appendFile, Kind: OverrideAppend})
		}
		instructions[i] = in
	}
	return instructions, used, nil
}

func replaceInstruction(in Instruction, file, data string) (Instruction, error) {
	text := strings.ReplaceAll(data, "\r\n", "\n")
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		head, after, ok := strings.Cut(rest, "\n---\n")
		if !ok {
			if head, ok = strings.CutSuffix(rest, "\n---"); !ok {
				return Instruction{}, fmt.Errorf("%s: front matter is not closed by ---", file)
			}
		}
		var fm frontMatter
		dec := yaml.NewDecoder(strings.NewReader(head))
		dec.KnownFields(true)
		if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
			return Instruction{}, fmt.Errorf("%s: front matter: %w", file, err)
		}
		if strings.TrimSpace(fm.Description) != "" {
			in.Description = fm.Description
		}
		if strings.TrimSpace(fm.ArgumentHint) != "" {
			in.ArgumentHint = fm.ArgumentHint
		}
		body = after
	}
	if strings.TrimSpace(body) == "" {
		return Instruction{}, fmt.Errorf("%s: the replacement has no body", file)
	}
	in.Body = body
	if err := checkOverride(in, file); err != nil {
		return Instruction{}, err
	}
	return in, nil
}

func appendToInstruction(in Instruction, file, data string) (Instruction, error) {
	text := strings.ReplaceAll(data, "\r\n", "\n")
	if strings.HasPrefix(text, "---\n") || text == "---" {
		return Instruction{}, fmt.Errorf("%s: an append file has no front matter", file)
	}
	if strings.TrimSpace(text) == "" {
		return Instruction{}, fmt.Errorf("%s: the append file is empty", file)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	in.Body = strings.TrimRight(in.Body, "\n") + "\n\n" + text
	if err := checkOverride(in, file); err != nil {
		return Instruction{}, err
	}
	return in, nil
}

func checkOverride(in Instruction, file string) error {
	if _, err := in.Render(RenderContext{Tool: func(n string) string { return n }}); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}
