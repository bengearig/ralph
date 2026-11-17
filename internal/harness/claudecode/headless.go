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
	if err := harness.CheckMode(adapter{}, req); err != nil {
		return harness.Invocation{}, err
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
	permissionMode := "dontAsk"
	if req.Mode == harness.PermissionAuto {
		permissionMode = "auto"
	}
	args = append(args, "--permission-mode", permissionMode, "--permission-prompts", "none")
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

// restrictedSettings is the --settings JSON of a run: in auto mode only the
// protected-path denies remain. There is
// deliberately no bare Edit or Read rule: a bare allow also covers paths
// outside the project.
func restrictedSettings(req harness.HeadlessRequest) (string, error) {
	var s permissionSettings
	p := &s.Permissions
	p.Allow = []string{}
	if req.Mode != harness.PermissionAuto {
		p.Allow = append(p.Allow, "Edit(./**)")
		for _, c := range req.Allow.Commands {
			p.Allow = append(p.Allow, "Bash("+c+")")
		}
		for _, t := range req.Allow.Tools {
			p.Allow = append(p.Allow, "mcp__"+req.MCP.Name+"__"+t)
		}
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

func (adapter) NewParser() harness.Parser {
	return &parser{uses: map[string]toolUse{}, denied: map[string]bool{}}
}

type parser struct {
	out    harness.Outcome
	uses   map[string]toolUse
	denied map[string]bool
}

type toolUse struct {
	name  string
	input json.RawMessage
}

type event struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	IsError   bool            `json:"is_error"`
	Result    string          `json:"result"`
	Errors    []string        `json:"errors"`
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	Message   json.RawMessage `json:"message"`
	Denials   []struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		Input     json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
}

type assistantMessage struct {
	Content []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

// target is the command or path a tool input names.
func target(tool string, raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return harness.CompactTarget(raw)
	}
	key := ""
	switch tool {
	case "Bash":
		key = "command"
	case "Edit", "Write", "MultiEdit", "Read":
		key = "file_path"
	case "NotebookEdit":
		key = "notebook_path"
	case "WebFetch":
		key = "url"
	}
	if s, ok := m[key].(string); ok && key != "" {
		return s
	}
	return harness.CompactTarget(raw)
}

func (p *parser) addDenial(id string, d harness.Denial) {
	if id != "" {
		p.denied[id] = true
	}
	d.Shell = d.Tool == "Bash"
	p.out.Denials = append(p.out.Denials, d)
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
	case e.Type == "assistant":
		var m assistantMessage
		if json.Unmarshal(e.Message, &m) != nil {
			return
		}
		for _, c := range m.Content {
			if c.Type == "tool_use" && c.ID != "" {
				p.uses[c.ID] = toolUse{c.Name, c.Input}
			}
		}
	case e.Type == "system" && e.Subtype == "permission_denied":
		var reason string
		_ = json.Unmarshal(e.Message, &reason)
		p.addDenial(e.ToolUseID, harness.Denial{
			Tool:   e.ToolName,
			Target: target(e.ToolName, p.uses[e.ToolUseID].input),
			Reason: reason,
		})
	case e.Type == "system" && e.Subtype == "init":
		if e.SessionID != "" {
			p.out.SessionID = e.SessionID
		}
	case e.Type == "result":
		p.out.Finished = true
		if e.SessionID != "" {
			p.out.SessionID = e.SessionID
		}
		for _, d := range e.Denials {
			if d.ToolUseID == "" || !p.denied[d.ToolUseID] {
				p.addDenial(d.ToolUseID, harness.Denial{
					Tool:   d.ToolName,
					Target: target(d.ToolName, d.Input),
					Reason: "denied by the permission mode",
				})
			}
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

func (p *parser) Outcome() harness.Outcome {
	o := p.out
	o.Denials = append([]harness.Denial{}, p.out.Denials...)
	return o
}
