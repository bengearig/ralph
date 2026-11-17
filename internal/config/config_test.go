package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/testutil"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(
		Setting{Path: "log.level", Default: "info", Flag: "log-level"},
		Setting{Path: "project.id"},
		Setting{Path: "a.b.c", Description: "deeply nested"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	reg                         *Registry
	userFile, projFile, localFi string
	root                        string
	env                         map[string]string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	dir := project.RalphDir(root)
	return fixture{
		reg:      testRegistry(t),
		userFile: filepath.Join(t.TempDir(), "user.yaml"),
		projFile: filepath.Join(dir, project.ProjectFile),
		localFi:  filepath.Join(dir, project.LocalFile),
		root:     root,
	}
}

func (f fixture) opts(env map[string]string, flags map[string]string) Options {
	return Options{
		Registry:    f.reg,
		UserFile:    f.userFile,
		ProjectRoot: f.root,
		Getenv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Flags:       flags,
	}
}

func TestEnvName(t *testing.T) {
	if got := EnvName("log.level"); got != "RALPH_LOG_LEVEL" {
		t.Fatalf("got %q", got)
	}
}

func TestLayerString(t *testing.T) {
	want := []string{"default", "user", "project", "project-local", "env", "flag"}
	for i, w := range want {
		if got := Layer(i).String(); got != w {
			t.Errorf("Layer(%d) = %q, want %q", i, got, w)
		}
	}
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	s, ok := r.Lookup("log.level")
	if !ok || s.Default != "info" || s.Flag != "log-level" || s.Description == "" {
		t.Fatalf("log.level = %+v, %v", s, ok)
	}
	p, ok := r.Lookup("project.id")
	if !ok || p.Default != "" || p.Flag != "" || p.Description == "" {
		t.Fatalf("project.id = %+v, %v", p, ok)
	}
	m, ok := r.Lookup("storage.migrate")
	if !ok || m.Default != "auto" || len(m.Allowed) != 2 || m.Allowed[0] != "auto" || m.Allowed[1] != "manual" || m.Description == "" {
		t.Fatalf("storage.migrate = %+v, %v", m, ok)
	}
	k, ok := r.Lookup("storage.backups.keep")
	if !ok || k.Default != "5" || k.Description == "" {
		t.Fatalf("storage.backups.keep = %+v, %v", k, ok)
	}
	got := r.Settings()
	var paths []string
	for _, s := range got {
		paths = append(paths, s.Path)
	}
	want := []string{"bitbucket.api_url", "bitbucket.repo", "bitbucket.token", "bitbucket.user", "bitbucket.workspace", "documents.export.dir", "documents.versions.keep", "git.branch_template", "git.commit_template", "git.commit_trailer", "github.api_url", "github.owner", "github.repo", "github.token", "headless.allow", "log.level", "plan.confirm_outline", "pr.base_branch", "pr.draft", "pr.provider", "pr.remote", "pr.timeout", "project.id", "storage.backups.keep", "storage.migrate", "verify.commands", "verify.timeout"}
	if !slices.Equal(paths, want) {
		t.Fatalf("settings = %v, want %v", paths, want)
	}
}

func TestNewRegistryRejectsDuplicate(t *testing.T) {
	if _, err := NewRegistry(Setting{Path: "a.b"}, Setting{Path: "a.b"}); err == nil {
		t.Fatal("expected error for duplicate path")
	}
}

func TestNewRegistryRejectsBadPaths(t *testing.T) {
	for _, path := range []string{"", ".a", "a.", "a..b"} {
		if _, err := NewRegistry(Setting{Path: path}); err == nil {
			t.Errorf("path %q: expected error", path)
		}
	}
	if _, err := NewRegistry(Setting{Path: "a"}, Setting{Path: "a.b"}); err == nil {
		t.Error("expected error for a setting that is also a prefix")
	}
	if _, err := NewRegistry(Setting{Path: "a", Flag: "x"}, Setting{Path: "b", Flag: "x"}); err == nil {
		t.Error("expected error for duplicate flag")
	}
}

func TestPrecedenceAcrossLayers(t *testing.T) {
	f := newFixture(t)
	write(t, f.userFile, "log:\n  level: user\n")
	write(t, f.projFile, "log:\n  level: project\n")
	write(t, f.localFi, "log:\n  level: local\n")
	env := map[string]string{"RALPH_LOG_LEVEL": "env"}
	flags := map[string]string{"log-level": "flag"}

	steps := []struct {
		drop  func()
		value string
		layer Layer
	}{
		{func() {}, "flag", LayerFlag},
		{func() { flags = nil }, "env", LayerEnv},
		{func() { env = nil }, "local", LayerProjectLocal},
		{func() { _ = os.Remove(f.localFi) }, "project", LayerProject},
		{func() { _ = os.Remove(f.projFile) }, "user", LayerUser},
		{func() { _ = os.Remove(f.userFile) }, "info", LayerDefault},
	}
	for _, s := range steps {
		s.drop()
		cfg, err := Load(f.opts(env, flags))
		if err != nil {
			t.Fatal(err)
		}
		v, ok := cfg.Get("log.level")
		if !ok || v.Value != s.value || v.Layer != s.layer {
			t.Fatalf("want %s from %s, got %+v", s.value, s.layer, v)
		}
	}
}

func TestProvenanceFile(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "# comment\n\nlog:\n  level: debug\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := cfg.Get("log.level")
	if v.Layer != LayerProject || v.Source.File != f.projFile || v.Source.Line != 4 || v.Value != "debug" {
		t.Fatalf("got %+v", v)
	}
}

func TestProvenanceEnvAndFlag(t *testing.T) {
	f := newFixture(t)
	cfg, err := Load(f.opts(map[string]string{"RALPH_PROJECT_ID": "p"}, map[string]string{"log-level": "warn"}))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("project.id"); v.Source.Name != "RALPH_PROJECT_ID" {
		t.Errorf("env source = %+v", v)
	}
	if v, _ := cfg.Get("log.level"); v.Source.Name != "--log-level" {
		t.Errorf("flag source = %+v", v)
	}
}

func TestNestedSetting(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "a:\n  b:\n    c: deep\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.Get("a.b.c"); !ok || v.Value != "deep" || v.Source.Line != 3 {
		t.Fatalf("got %+v", v)
	}
}

func TestMissingFilesAreSkipped(t *testing.T) {
	f := newFixture(t)
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	vals := cfg.Values()
	if len(vals) != 1 || vals[0].Path != "log.level" || vals[0].Layer != LayerDefault {
		t.Fatalf("values = %+v", vals)
	}
}

func TestEmptyFilesSetNothing(t *testing.T) {
	f := newFixture(t)
	write(t, f.userFile, "")
	write(t, f.projFile, "# only a comment\n")
	write(t, f.localFi, "---\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("log.level"); v.Layer != LayerDefault {
		t.Fatalf("got %+v", v)
	}
}

func TestUnreadableFileIsError(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile+"/nested", "")
	_, err := Load(f.opts(nil, nil))
	if err == nil || !strings.Contains(err.Error(), f.projFile) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyEnvIsUnset(t *testing.T) {
	f := newFixture(t)
	cfg, err := Load(f.opts(map[string]string{"RALPH_LOG_LEVEL": ""}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("log.level"); v.Layer != LayerDefault {
		t.Fatalf("got %+v", v)
	}
}

func TestFlagsOnlyWhenSet(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "log:\n  level: debug\n")
	cfg, err := Load(f.opts(nil, map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("log.level"); v.Layer != LayerProject {
		t.Fatalf("got %+v", v)
	}
}

func TestUnknownFlagIsError(t *testing.T) {
	f := newFixture(t)
	_, err := Load(f.opts(nil, map[string]string{"nope": "x"}))
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownSettingIsError(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "log:\n  level: info\n  colour: red\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{f.projFile + ":3", `unknown setting "log.colour"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	write(t, f.projFile, "bogus: 1\n")
	if _, err = Load(f.opts(nil, nil)); err == nil || !strings.Contains(err.Error(), `:1: unknown setting "bogus"`) {
		t.Fatalf("err = %v", err)
	}
	write(t, f.projFile, "log.level: debug\n")
	if _, err = Load(f.opts(nil, nil)); err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("dotted key err = %v", err)
	}
}

func TestNestedKeyMustBeMapping(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "log: debug\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil || !strings.Contains(err.Error(), f.projFile+":1") || !strings.Contains(err.Error(), `"log"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestNonScalarIsError(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{"log:\n  level: [a, b]\n", "log:\n  level:\n    x: y\n"} {
		write(t, f.projFile, body)
		_, err := Load(f.opts(nil, nil))
		if err == nil || !strings.Contains(err.Error(), f.projFile+":2") || !strings.Contains(err.Error(), `"log.level"`) {
			t.Fatalf("body %q: err = %v", body, err)
		}
	}
}

func TestInvalidYAMLNamesFile(t *testing.T) {
	f := newFixture(t)
	write(t, f.userFile, "log: [unclosed\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil || !strings.Contains(err.Error(), f.userFile) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyProjectRootSkipsProjectLayers(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "log:\n  level: debug\n")
	opts := f.opts(nil, nil)
	opts.ProjectRoot = ""
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("log.level"); v.Layer != LayerDefault {
		t.Fatalf("got %+v", v)
	}
}

func TestEmptyUserFileSkipsUserLayer(t *testing.T) {
	f := newFixture(t)
	write(t, f.userFile, "log:\n  level: debug\n")
	opts := f.opts(nil, nil)
	opts.UserFile = ""
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("log.level"); v.Layer != LayerDefault {
		t.Fatalf("got %+v", v)
	}
}

func TestDefaultUserFileHonorsXDG(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME applies on Linux")
	}
	testutil.IsolateEnv(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got, want := DefaultUserFile(), filepath.Join(dir, "ralph", "config.yaml"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
