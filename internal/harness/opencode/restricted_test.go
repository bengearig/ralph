package opencode

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func restrictedReq() harness.HeadlessRequest {
	req := headlessReq()
	req.Allow = harness.Allowlist{
		Tools:     []string{"status", "unit_get"},
		Commands:  []string{"git status *", `go test \*`, `a\\b`},
		Protected: []string{".git/", ".ralph/", "secrets.txt"},
	}
	return req
}

func contentOf(t *testing.T, req harness.HeadlessRequest) string {
	t.Helper()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := strings.CutPrefix(inv.Env[1], "OPENCODE_CONFIG_CONTENT=")
	if !ok {
		t.Fatalf("Env = %q", inv.Env)
	}
	return got
}

func rulesOf(t *testing.T, req harness.HeadlessRequest) []permissionRule {
	t.Helper()
	var c configContent
	if err := json.Unmarshal([]byte(contentOf(t, req)), &c); err != nil {
		t.Fatal(err)
	}
	return c.Permissions
}

func indexOf(rules []permissionRule, r permissionRule) int {
	for i, x := range rules {
		if x == r {
			return i
		}
	}
	return -1
}

func TestOpenCodeRestrictedRulesGolden(t *testing.T) {
	got := contentOf(t, restrictedReq())
	golden, err := os.ReadFile("testdata/restricted_config_content.golden")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(string(golden), "\n"); got != want {
		t.Errorf("config content =\n%s\nwant\n%s", got, want)
	}
}

func TestOpenCodeRestrictedProtectedDenyAfterEditAllow(t *testing.T) {
	rules := rulesOf(t, restrictedReq())
	allow := indexOf(rules, permissionRule{"edit", "*", "allow"})
	for _, r := range rules {
		if r.Action == "edit" && r.Effect == "deny" {
			if i := indexOf(rules, r); i < allow {
				t.Errorf("deny %+v at %d before edit allow at %d", r, i, allow)
			}
		}
	}
}

func TestOpenCodeRestrictedProtectedPatternsCoverRelativeAndAbsolute(t *testing.T) {
	rules := rulesOf(t, restrictedReq())
	for _, res := range []string{".git/*", "*/.git/*", "secrets.txt", "*/secrets.txt", ".opencode/*", "*/opencode.json", "opencode.jsonc"} {
		if indexOf(rules, permissionRule{"edit", res, "deny"}) < 0 {
			t.Errorf("deny %q missing", res)
		}
	}
}

func TestOpenCodeRestrictedToolRulesUseServerPrefix(t *testing.T) {
	rules := rulesOf(t, restrictedReq())
	if indexOf(rules, permissionRule{"ralph_unit_get", "*", "allow"}) < 0 {
		t.Errorf("rules = %+v", rules)
	}
}

func TestOpenCodeRestrictedStarEscapesReduced(t *testing.T) {
	rules := rulesOf(t, restrictedReq())
	for _, res := range []string{"git status *", "go test *", `a\b`} {
		if indexOf(rules, permissionRule{"shell", res, "allow"}) < 0 {
			t.Errorf("shell %q missing from %+v", res, rules)
		}
	}
}

func TestOpenCodeRestrictedEmptyAllow(t *testing.T) {
	rules := rulesOf(t, headlessReq())
	want := []permissionRule{
		{"*", "*", "deny"}, {"read", "*", "allow"}, {"glob", "*", "allow"}, {"grep", "*", "allow"}, {"edit", "*", "allow"},
		{"edit", ".opencode/*", "deny"}, {"edit", "*/.opencode/*", "deny"},
		{"edit", "opencode.json", "deny"}, {"edit", "*/opencode.json", "deny"},
		{"edit", "opencode.jsonc", "deny"}, {"edit", "*/opencode.jsonc", "deny"},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("rules = %+v", rules)
	}
}

func autoInvocation(t *testing.T) harness.Invocation {
	t.Helper()
	req := headlessReq()
	req.Mode = harness.PermissionAuto
	req.Allow = harness.Allowlist{Protected: []string{".git/", ".ralph/"}}
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestOpenCodeAutoArgsEndWithAuto(t *testing.T) {
	inv := autoInvocation(t)
	want := []string{"run", "--standalone", "--format", "json", "--archived", "--auto"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("Args = %q, want %q", inv.Args, want)
	}
}

func TestOpenCodeAutoRulesOnlyProtectedDenies(t *testing.T) {
	inv := autoInvocation(t)
	content := strings.TrimPrefix(inv.Env[1], "OPENCODE_CONFIG_CONTENT=")
	var cfg configContent
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP.Servers) != 1 || len(cfg.Permissions) == 0 {
		t.Fatalf("config = %s", content)
	}
	for _, r := range cfg.Permissions {
		if r.Effect != "deny" || r.Action != "edit" {
			t.Errorf("rule %+v is not a protected edit deny", r)
		}
	}
}

func TestOpenCodeAutoKeepsProjectConfigDisabled(t *testing.T) {
	inv := autoInvocation(t)
	if len(inv.Env) != 2 || inv.Env[0] != "OPENCODE_DISABLE_PROJECT_CONFIG=1" {
		t.Errorf("Env = %q", inv.Env)
	}
}
