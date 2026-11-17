package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bengearig/ralph/internal/harness"
)

func (adapter) Capabilities() harness.Capabilities {
	return harness.Capabilities{Auto: true}
}

type mcpEntry struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
}

type permissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

type configContent struct {
	MCP struct {
		Servers map[string]mcpEntry `json:"servers"`
	} `json:"mcp"`
	Permissions []permissionRule `json:"permissions"`
}

func (adapter) Headless(req harness.HeadlessRequest) (harness.Invocation, error) {
	if err := req.Validate(); err != nil {
		return harness.Invocation{}, err
	}
	if req.Mode == harness.PermissionAuto {
		return harness.Invocation{}, fmt.Errorf("auto mode is not implemented yet")
	}
	var cfg configContent
	cfg.MCP.Servers = map[string]mcpEntry{
		req.MCP.Name: {Type: "local", Command: append([]string{req.MCP.Command}, req.MCP.Args...)},
	}
	cfg.Permissions = []permissionRule{{Action: "*", Resource: "*", Effect: "deny"}}
	content, err := json.Marshal(cfg)
	if err != nil {
		return harness.Invocation{}, fmt.Errorf("config content: %w", err)
	}
	args := []string{"run", "--standalone", "--format", "json", "--archived"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	return harness.Invocation{
		Command: "opencode",
		Args:    args,
		Env:     []string{"OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_CONFIG_CONTENT=" + string(content)},
		Dir:     req.Dir,
		Stdin:   req.Prompt,
	}, nil
}

func (adapter) NewParser() harness.Parser { return &parser{} }

type parser struct{ out harness.Outcome }

type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Text   string `json:"text"`
		Reason string `json:"reason"`
	} `json:"part"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
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
	if e.SessionID != "" {
		p.out.SessionID = e.SessionID
	}
	switch e.Type {
	case "text":
		p.out.Text = e.Part.Text
	case "step_finish":
		if e.Part.Reason == "stop" {
			p.out.Finished = true
		}
	case "error":
		if e.Error.Type != "" {
			p.out.Error = e.Error.Type + ": " + e.Error.Message
		} else {
			p.out.Error = e.Error.Message
		}
	}
	p.out.Success = p.out.Finished && p.out.Error == ""
}

func (p *parser) Usage() harness.Usage { return p.out.Usage }

func (p *parser) Outcome() harness.Outcome { return p.out }
