// Package workflow holds the harness-neutral workflow instruction texts.
package workflow

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"

	"go.yaml.in/yaml/v3"
)

//go:embed instructions/*.md
var instructionFiles embed.FS

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Instruction is one workflow instruction an adapter presents to its harness.
type Instruction struct {
	Name         string
	Description  string
	ArgumentHint string
	// Body is a template; use Render to get text for a harness.
	Body string
}

// RenderContext supplies the harness-specific parts of an instruction.
type RenderContext struct {
	// Tool returns the harness's name for a ralph tool.
	Tool func(name string) string
	// Arguments is the harness's placeholder for what the user typed after the command.
	Arguments string
	// Text holds the harness's wording for keys that differ between harnesses;
	// a key it lacks gets the neutral default.
	Text map[string]string
}

// defaultText is the neutral wording for each key of RenderContext.Text.
var defaultText = map[string]string{
	"approval": "Show the outline to the user in this conversation and wait. Create or apply nothing until the user approves it. If they reject it with feedback, revise the outline and show it again. Create or apply it only once they approve.",
}

type frontMatter struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

// All returns every instruction, sorted by name.
func All() ([]Instruction, error) {
	return load(instructionFiles, "instructions")
}

// Get returns the named instruction.
func Get(name string) (Instruction, bool, error) {
	all, err := All()
	if err != nil {
		return Instruction{}, false, err
	}
	for _, in := range all {
		if in.Name == name {
			return in, true, nil
		}
	}
	return Instruction{}, false, nil
}

func load(fsys fs.FS, dir string) ([]Instruction, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading instructions: %w", err)
	}
	var out []Instruction
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("instruction %s: %w", e.Name(), err)
		}
		in, err := parseInstruction(e.Name(), data)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[in.Name]; dup {
			return nil, fmt.Errorf("instruction %s: duplicates %s", e.Name(), prev)
		}
		seen[in.Name] = e.Name()
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseInstruction(file string, data []byte) (Instruction, error) {
	name := strings.TrimSuffix(file, ".md")
	if !namePattern.MatchString(name) {
		return Instruction{}, fmt.Errorf("instruction %s: name %q must match %s", file, name, namePattern)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Instruction{}, fmt.Errorf("instruction %s: missing front matter", file)
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		if head, ok = strings.CutSuffix(rest, "\n---"); !ok {
			return Instruction{}, fmt.Errorf("instruction %s: front matter is not closed by ---", file)
		}
	}
	var fm frontMatter
	dec := yaml.NewDecoder(strings.NewReader(head))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return Instruction{}, fmt.Errorf("instruction %s: front matter: %w", file, err)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return Instruction{}, fmt.Errorf("instruction %s: front matter has no description", file)
	}
	in := Instruction{Name: name, Description: fm.Description, ArgumentHint: fm.ArgumentHint, Body: body}
	if _, err := in.Render(RenderContext{Tool: func(n string) string { return n }, Arguments: ""}); err != nil {
		return Instruction{}, fmt.Errorf("instruction %s: %w", file, err)
	}
	return in, nil
}

// Render expands the body for one harness.
func (in Instruction) Render(rc RenderContext) (string, error) {
	if rc.Tool == nil {
		return "", fmt.Errorf("instruction %s: render context has no Tool function", in.Name)
	}
	funcs := template.FuncMap{
		"tool":      func(name string) string { return rc.Tool(name) },
		"arguments": func() string { return rc.Arguments },
		"harnessText": func(key string) (string, error) {
			if text, ok := rc.Text[key]; ok {
				return text, nil
			}
			if text, ok := defaultText[key]; ok {
				return text, nil
			}
			return "", fmt.Errorf("unknown harness text %q", key)
		},
	}
	tmpl, err := template.New(in.Name).Option("missingkey=error").Funcs(funcs).Parse(in.Body)
	if err != nil {
		return "", fmt.Errorf("instruction %s: %w", in.Name, err)
	}
	if err := checkTree(tmpl.Root); err != nil {
		return "", fmt.Errorf("instruction %s: %w", in.Name, err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		return "", fmt.Errorf("instruction %s: %w", in.Name, err)
	}
	return sb.String(), nil
}

// Tools lists the tool names the body references, sorted and unique.
func (in Instruction) Tools() []string {
	set := map[string]struct{}{}
	_, err := in.Render(RenderContext{Tool: func(name string) string {
		set[name] = struct{}{}
		return name
	}})
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// checkTree allows only text and actions made of the tool, arguments and
// harnessText functions with literal arguments; text/template builtins cannot be removed
// from a template, so they are rejected here.
func checkTree(root *parse.ListNode) error {
	for _, n := range root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
				return fmt.Errorf("only a single call to tool, arguments or harnessText is allowed in %s", n)
			}
			args := n.Pipe.Cmds[0].Args
			id, ok := args[0].(*parse.IdentifierNode)
			if !ok {
				return fmt.Errorf("unsupported action %s", n)
			}
			switch id.Ident {
			case "tool":
				if len(args) != 2 {
					return fmt.Errorf("tool takes one string argument in %s", n)
				}
				if _, ok := args[1].(*parse.StringNode); !ok {
					return fmt.Errorf("tool takes a string literal in %s", n)
				}
			case "harnessText":
				if len(args) != 2 {
					return fmt.Errorf("harnessText takes one string argument in %s", n)
				}
				if _, ok := args[1].(*parse.StringNode); !ok {
					return fmt.Errorf("harnessText takes a string literal in %s", n)
				}
			case "arguments":
				if len(args) != 1 {
					return fmt.Errorf("arguments takes no argument in %s", n)
				}
			default:
				return fmt.Errorf("function %q is not allowed", id.Ident)
			}
		default:
			return fmt.Errorf("unsupported template construct %s", n)
		}
	}
	return nil
}
