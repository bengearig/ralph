package claudecode

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func restrictedReq() harness.HeadlessRequest {
	req := headlessReq()
	req.Allow = harness.Allowlist{
		Tools:     []string{"status", "unit_get"},
		Commands:  []string{"git status *", `go test \*`, "echo (hi)"},
		Protected: []string{".git/", ".ralph/", "secrets.txt"},
	}
	return req
}

func settingsArg(t *testing.T, req harness.HeadlessRequest) string {
	t.Helper()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(inv.Args, "--settings")
	if i < 0 || i != len(inv.Args)-2 || inv.Args[i-1] != "none" {
		t.Fatalf("--settings not last after --permission-prompts none: %q", inv.Args)
	}
	return inv.Args[i+1]
}

type parsedSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

func parseSettings(t *testing.T, s string) parsedSettings {
	t.Helper()
	var p parsedSettings
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("not JSON: %v: %s", err, s)
	}
	return p
}

func TestClaudeRestrictedSettingsJSONGolden(t *testing.T) {
	got := settingsArg(t, restrictedReq())
	golden, err := os.ReadFile("testdata/restricted_settings.golden")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(string(golden), "\n"); got != want {
		t.Errorf("settings =\n%s\nwant\n%s", got, want)
	}
}

func TestClaudeRestrictedNoBareEditOrRead(t *testing.T) {
	p := parseSettings(t, settingsArg(t, restrictedReq()))
	for _, r := range append(slices.Clone(p.Permissions.Allow), p.Permissions.Deny...) {
		if r == "Edit" || r == "Read" || strings.HasPrefix(r, "Read(") {
			t.Errorf("rule %q", r)
		}
	}
	if !slices.Contains(p.Permissions.Allow, "Edit(./**)") {
		t.Errorf("allow = %q", p.Permissions.Allow)
	}
}

func TestClaudeRestrictedEmptyAllowIsMinimal(t *testing.T) {
	p := parseSettings(t, settingsArg(t, headlessReq()))
	if want := []string{"Edit(./**)"}; !reflect.DeepEqual(p.Permissions.Allow, want) {
		t.Errorf("allow = %q, want %q", p.Permissions.Allow, want)
	}
	if want := []string{"Edit(.claude/**)", "Edit(.mcp.json)"}; !reflect.DeepEqual(p.Permissions.Deny, want) {
		t.Errorf("deny = %q, want %q", p.Permissions.Deny, want)
	}
}

func TestClaudeRestrictedDeniesOwnConfig(t *testing.T) {
	p := parseSettings(t, settingsArg(t, restrictedReq()))
	for _, want := range []string{"Edit(.claude/**)", "Edit(.mcp.json)", "Edit(.git/**)", "Edit(.ralph/**)", "Edit(secrets.txt)"} {
		if !slices.Contains(p.Permissions.Deny, want) {
			t.Errorf("deny %q missing from %q", want, p.Permissions.Deny)
		}
	}
}

func TestClaudeRestrictedParenthesesInPatternUnchanged(t *testing.T) {
	p := parseSettings(t, settingsArg(t, restrictedReq()))
	if !slices.Contains(p.Permissions.Allow, "Bash(echo (hi))") || !slices.Contains(p.Permissions.Allow, `Bash(go test \*)`) {
		t.Errorf("allow = %q", p.Permissions.Allow)
	}
}
