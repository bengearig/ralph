package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/harness"
)

var minHeadlessVersion = [3]int{2, 1, 259}

const minHeadlessVersionText = "2.1.259"

var leadingVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func (adapter) Capabilities() harness.Capabilities {
	return harness.Capabilities{Auto: true, NativeBudget: true}
}

// tooOld reports whether v is a parsable version below the headless minimum.
func tooOld(v string) bool {
	m := leadingVersion.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return false
	}
	for i := range minHeadlessVersion {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return false
		}
		if n != minHeadlessVersion[i] {
			return n < minHeadlessVersion[i]
		}
	}
	return false
}

func (adapter) Headless(req harness.HeadlessRequest) (harness.Invocation, error) {
	if err := req.Validate(); err != nil {
		return harness.Invocation{}, err
	}
	if req.Mode == harness.PermissionAuto {
		return harness.Invocation{}, fmt.Errorf("auto mode is not implemented yet")
	}
	if tooOld(req.Version) {
		return harness.Invocation{}, fmt.Errorf("claude %s is too old for headless runs; version %s or later is needed", req.Version, minHeadlessVersionText)
	}
	entry, err := mcpContent(req.MCP)
	if err != nil {
		return harness.Invocation{}, err
	}
	cfg, err := json.Marshal(struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}{map[string]json.RawMessage{req.MCP.Name: entry}})
	if err != nil {
		return harness.Invocation{}, fmt.Errorf("MCP config: %w", err)
	}
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "user,local", "--strict-mcp-config", "--mcp-config", string(cfg),
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, "--permission-mode", "dontAsk", "--permission-prompts", "none")
	settings, err := restrictedSettings(req)
	if err != nil {
		return harness.Invocation{}, err
	}
	args = append(args, "--settings", settings)
	return harness.Invocation{Command: "claude", Args: args, Dir: req.Dir, Stdin: req.Prompt}, nil
}

var ownProtected = []string{".claude/", ".mcp.json"}

type permissionSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

// restrictedSettings is the --settings JSON of a restricted run. There is
// deliberately no bare Edit or Read rule: a bare allow also covers paths
// outside the project.
func restrictedSettings(req harness.HeadlessRequest) (string, error) {
	var s permissionSettings
	p := &s.Permissions
	p.Allow = []string{"Edit(./**)"}
	for _, c := range req.Allow.Commands {
		p.Allow = append(p.Allow, "Bash("+c+")")
	}
	for _, t := range req.Allow.Tools {
		p.Allow = append(p.Allow, "mcp__"+req.MCP.Name+"__"+t)
	}
	p.Deny = []string{}
	for _, path := range append(append([]string(nil), req.Allow.Protected...), ownProtected...) {
		if dir, ok := strings.CutSuffix(path, "/"); ok {
			p.Deny = append(p.Deny, "Edit("+dir+"/**)")
		} else {
			p.Deny = append(p.Deny, "Edit("+path+")")
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("permission settings: %w", err)
	}
	return string(b), nil
}

func (adapter) NewParser() harness.Parser { return &parser{} }

type parser struct{ out harness.Outcome }

type event struct {
	Type      string   `json:"type"`
	Subtype   string   `json:"subtype"`
	SessionID string   `json:"session_id"`
	IsError   bool     `json:"is_error"`
	Result    string   `json:"result"`
	Errors    []string `json:"errors"`
}

func (p *parser) Line(b []byte) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return
	}
	var e event
	if json.Unmarshal(b, &e) != nil {
		return
	}
	switch {
	case e.Type == "system" && e.Subtype == "init":
		if e.SessionID != "" {
			p.out.SessionID = e.SessionID
		}
	case e.Type == "result":
		p.out.Finished = true
		if e.SessionID != "" {
			p.out.SessionID = e.SessionID
		}
		p.out.Text = e.Result
		p.out.Success = e.Subtype == "success" && !e.IsError
		p.out.Error = ""
		if !p.out.Success {
			if len(e.Errors) > 0 {
				p.out.Error = strings.Join(e.Errors, "; ")
			} else {
				p.out.Error = e.Subtype
			}
		}
	}
}

func (p *parser) Usage() harness.Usage { return p.out.Usage }

func (p *parser) Outcome() harness.Outcome { return p.out }
