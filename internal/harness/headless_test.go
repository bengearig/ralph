package harness

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func validRequest(t *testing.T) HeadlessRequest {
	t.Helper()
	return HeadlessRequest{
		Dir:    t.TempDir(),
		Prompt: "do it",
		MCP:    MCPServer{Name: "ralph", Command: "ralph"},
	}
}

func TestHeadlessRequestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HeadlessRequest)
		want   string
	}{
		{"valid minimal", func(*HeadlessRequest) {}, ""},
		{"empty mode accepted", func(r *HeadlessRequest) { r.Mode = "" }, ""},
		{"auto mode accepted", func(r *HeadlessRequest) { r.Mode = PermissionAuto }, ""},
		{"empty dir", func(r *HeadlessRequest) { r.Dir = "" }, "Dir"},
		{"relative dir", func(r *HeadlessRequest) { r.Dir = filepath.Join("a", "b") }, "Dir"},
		{"empty prompt", func(r *HeadlessRequest) { r.Prompt = "" }, "Prompt"},
		{"blank prompt", func(r *HeadlessRequest) { r.Prompt = " \n\t " }, "Prompt"},
		{"invalid mode", func(r *HeadlessRequest) { r.Mode = "yolo" }, "Mode"},
		{"negative budget", func(r *HeadlessRequest) { r.BudgetUSD = -1 }, "BudgetUSD"},
		{"nan budget", func(r *HeadlessRequest) { r.BudgetUSD = math.NaN() }, "BudgetUSD"},
		{"infinite budget", func(r *HeadlessRequest) { r.BudgetUSD = math.Inf(1) }, "BudgetUSD"},
		{"model with space", func(r *HeadlessRequest) { r.Model = "a b" }, "Model"},
		{"model with leading dash", func(r *HeadlessRequest) { r.Model = "-x" }, "Model"},
		{"model with control character", func(r *HeadlessRequest) { r.Model = "a\x01b" }, "Model"},
		{"model set", func(r *HeadlessRequest) { r.Model = "anthropic/claude-x" }, ""},
		{"empty mcp name", func(r *HeadlessRequest) { r.MCP.Name = "" }, "MCP.Name"},
		{"empty mcp command", func(r *HeadlessRequest) { r.MCP.Command = "" }, "MCP.Command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest(t)
			tt.mutate(&req)
			err := req.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

func TestPermissionModeValid(t *testing.T) {
	for mode, want := range map[PermissionMode]bool{
		PermissionRestricted: true,
		PermissionAuto:       true,
		"":                   false,
		"Auto":               false,
		"yolo":               false,
	} {
		if got := mode.Valid(); got != want {
			t.Errorf("PermissionMode(%q).Valid() = %v, want %v", mode, got, want)
		}
	}
}
