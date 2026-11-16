package naming

import (
	"strings"
	"testing"
)

func TestSlugifyTable(t *testing.T) {
	for in, want := range map[string]string{
		"Add dark mode toggle":  "add-dark-mode-toggle",
		"a -- b!!c":             "a-b-c",
		"  --Hello, World!--  ": "hello-world",
		"Ünïcode ok":            "n-code-ok",
		"":                      "work",
		"!!! ???":               "work",
		"日本語":                   "work",
		"v1.2 release":          "v1-2-release",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugifyLengthCut(t *testing.T) {
	for _, in := range []string{
		"this is a very long title that keeps going well past the limit",
		strings.Repeat("a", 100),
		strings.Repeat("abcd ", 20),
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbb",
	} {
		got := Slugify(in)
		if len(got) > 40 || len(got) == 0 || strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") {
			t.Errorf("Slugify(%q) = %q", in, got)
		}
	}
	if got := Slugify("this is a very long title that keeps going well past the limit"); got != "this-is-a-very-long-title-that-keeps" {
		t.Errorf("hyphen cut = %q", got)
	}
	if got := Slugify(strings.Repeat("a", 100)); got != strings.Repeat("a", 40) {
		t.Errorf("hard cut = %q", got)
	}
}

func TestRenderBranchDefault(t *testing.T) {
	const tmpl = "ralph/{{lower .ID}}-{{.Slug}}"
	for _, c := range []struct{ id, title, want string }{
		{"S-3", "Settings toggle", "ralph/s-3-settings-toggle"},
		{"T-12", "Fix typo", "ralph/t-12-fix-typo"},
	} {
		got, err := RenderBranch(tmpl, Data{ID: c.id, Title: c.title, Slug: Slugify(c.title)})
		if err != nil || got != c.want {
			t.Errorf("RenderBranch = %q, %v; want %q", got, err, c.want)
		}
	}
}

func TestRenderBranchCustom(t *testing.T) {
	got, err := RenderBranch("{{.Level}}/{{upper .ID}}-{{trunc 4 .Slug}}", Data{ID: "s-1", Level: "slice", Slug: "settings"})
	if err != nil || got != "slice/S-1-sett" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRenderBranchInvalidResult(t *testing.T) {
	for tmpl, value := range map[string]string{"{{.Title}}": "Fix typo", "a{{.ParentID}}..b": "aB-1..b"} {
		_, err := RenderBranch(tmpl, Data{Title: "Fix typo", ParentID: "B-1"})
		if err == nil || !strings.Contains(err.Error(), value) {
			t.Errorf("%q: err = %v, want it to name %q", tmpl, err, value)
		}
	}
}

func TestRenderMissingFieldErrors(t *testing.T) {
	if _, err := RenderSubject("{{.Missing}}", Data{}); err == nil {
		t.Error("missing field accepted")
	}
}

func TestRenderUnknownFunctionRejected(t *testing.T) {
	if _, err := RenderSubject("{{shout .ID}}", Data{}); err == nil {
		t.Error("unknown function accepted")
	}
}

func TestRenderSubjectDefault(t *testing.T) {
	got, err := RenderSubject("{{.ID}}: {{.Title}}", Data{ID: "T-1", Title: "Fix typo"})
	if err != nil || got != "T-1: Fix typo" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRenderSubjectCollapsesNewlines(t *testing.T) {
	got, err := RenderSubject("{{.Title}}\n", Data{Title: "one\r\ntwo\nthree"})
	if err != nil || got != "one  two three" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestValidBranchNameTable(t *testing.T) {
	for _, ok := range []string{"ralph/s-1-x", "feature/a.b"} {
		if err := ValidBranchName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a..b", "a@{b", "@", "-a", "/a", "a/", "a//b", "a.lock", "a/.b",
		"a.", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a\x01b", "a\tb", "a/b.lock/c"} {
		if err := ValidBranchName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFormatCommitMessageWithTrailer(t *testing.T) {
	if got, want := FormatCommitMessage("T-1: Fix", "T-1", true), "T-1: Fix\n\nRalph-Task: T-1\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatCommitMessageWithoutTrailer(t *testing.T) {
	if got, want := FormatCommitMessage("T-1: Fix", "T-1", false), "T-1: Fix\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseTaskTrailer(t *testing.T) {
	for _, c := range []struct {
		name, msg, want string
		ok              bool
	}{
		{"present", "T-1: x\n\nRalph-Task: T-1\n", "T-1", true},
		{"absent", "T-1: x\n\nbody\n", "", false},
		{"empty", "", "", false},
		{"crlf", "T-1: x\r\n\r\nRalph-Task: T-1\r\n", "T-1", true},
		{"trailing spaces", "x\n\n  Ralph-Task:   T-2  \n\n  \n", "T-2", true},
		{"middle paragraph", "x\n\nRalph-Task: T-1\n\nfinal paragraph\n", "", false},
		{"last of several", "x\n\nRalph-Task: T-1\nSigned-off-by: a\nRalph-Task: T-2\n", "T-2", true},
		{"empty value", "x\n\nRalph-Task:\n", "", false},
	} {
		got, ok := ParseTaskTrailer(c.msg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	if got, ok := ParseTaskTrailer(FormatCommitMessage("T-9: x", "T-9", true)); !ok || got != "T-9" {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestValidateTemplates(t *testing.T) {
	good := []string{"ralph/{{lower .ID}}-{{.Slug}}", "{{.Level}}/{{.ParentID}}/{{.Number}}"}
	for _, g := range good {
		if err := ValidateBranchTemplate(g); err != nil {
			t.Errorf("branch %q: %v", g, err)
		}
	}
	for _, b := range []string{"{{.ID", "{{shout .ID}}", "{{.Title}}", "{{.Nope}}", ""} {
		if err := ValidateBranchTemplate(b); err == nil {
			t.Errorf("branch %q accepted", b)
		}
	}
	if err := ValidateSubjectTemplate("{{.ID}}: {{.Title}}"); err != nil {
		t.Error(err)
	}
	for _, b := range []string{"{{.ID", "{{shout .ID}}", "{{.Nope}}"} {
		if err := ValidateSubjectTemplate(b); err == nil {
			t.Errorf("subject %q accepted", b)
		}
	}
}
