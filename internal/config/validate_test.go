package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validatedFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	f.reg = DefaultRegistry()
	return f
}

func asError(t *testing.T, err error) *Error {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *Error", err)
	}
	return ce
}

func TestValidationAllowedValues(t *testing.T) {
	f := validatedFixture(t)
	write(t, f.projFile, "log:\n  level: verbose\n")
	_, err := Load(f.opts(nil, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 1 {
		t.Fatalf("problems = %v", ce.Problems)
	}
	p := ce.Problems[0]
	if p.File != f.projFile || p.Line != 2 {
		t.Errorf("location = %s:%d", p.File, p.Line)
	}
	want := `invalid value for "log.level": must be one of debug, info, warn, error`
	if p.Message != want {
		t.Errorf("message = %q, want %q", p.Message, want)
	}
}

func TestValidationFromEnvAndFlag(t *testing.T) {
	f := validatedFixture(t)
	_, err := Load(f.opts(map[string]string{"RALPH_LOG_LEVEL": "loud"}, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 1 || ce.Problems[0].Source != "RALPH_LOG_LEVEL" ||
		!strings.HasPrefix(ce.Problems[0].String(), "environment variable RALPH_LOG_LEVEL: ") {
		t.Fatalf("env problems = %v", ce.Problems)
	}
	_, err = Load(f.opts(nil, map[string]string{"log-level": "loud"}))
	ce = asError(t, err)
	if len(ce.Problems) != 1 || !strings.HasPrefix(ce.Problems[0].String(), "flag --log-level: ") {
		t.Fatalf("flag problems = %v", ce.Problems)
	}
}

func TestValidationProjectIDEmpty(t *testing.T) {
	f := validatedFixture(t)
	for _, body := range []string{"project:\n  id: \"\"\n", "project:\n  id: \"   \"\n"} {
		write(t, f.projFile, body)
		_, err := Load(f.opts(nil, nil))
		ce := asError(t, err)
		if len(ce.Problems) != 1 || ce.Problems[0].Line != 2 ||
			ce.Problems[0].Message != `invalid value for "project.id": must not be empty` {
			t.Fatalf("body %q: problems = %v", body, ce.Problems)
		}
	}
	write(t, f.projFile, "project:\n  id: abc\n")
	if _, err := Load(f.opts(nil, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestValidationSecretSettingNeverEchoes(t *testing.T) {
	f := secretFixture(t)
	f.reg, _ = NewRegistry(Setting{
		Path: "gh.token", Kind: KindSecret, Flag: "gh-token",
		Validate: func(v string) error { return errors.New("bad " + v) },
	})
	write(t, f.projFile, "gh:\n  token: "+literal+"\n")
	_, err := Load(f.opts(map[string]string{"RALPH_GH_TOKEN": literal}, map[string]string{"gh-token": literal}))
	ce := asError(t, err)
	if len(ce.Problems) != 3 {
		t.Fatalf("problems = %v", ce.Problems)
	}
	if strings.Contains(ce.Error(), literal) {
		t.Fatalf("echoed: %v", ce)
	}
}

func TestValidatorRunsAfterSecretReferenceIsWellFormed(t *testing.T) {
	f := secretFixture(t)
	f.reg, _ = NewRegistry(Setting{
		Path: "gh.token", Kind: KindSecret,
		Validate: func(string) error { return errors.New("rejected") },
	})
	write(t, f.projFile, "gh:\n  token: env:TOK\n")
	_, err := Load(f.opts(nil, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 1 || !strings.Contains(ce.Problems[0].Message, "rejected") {
		t.Fatalf("problems = %v", ce.Problems)
	}
}

func TestAllProblemsReported(t *testing.T) {
	f := validatedFixture(t)
	write(t, f.projFile, "log:\n  level: verbose\nbogus: 1\n")
	_, err := Load(f.opts(map[string]string{"RALPH_LOG_LEVEL": "loud"}, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 3 {
		t.Fatalf("problems = %v", ce.Problems)
	}
	if ce.Problems[0].Line != 2 || ce.Problems[1].Line != 3 || ce.Problems[2].Source != "RALPH_LOG_LEVEL" {
		t.Fatalf("order = %v", ce.Problems)
	}
	if !strings.Contains(ce.Problems[1].Message, `unknown setting "bogus"`) {
		t.Fatalf("problem = %v", ce.Problems[1])
	}
}

func TestAllProblemsLayerOrder(t *testing.T) {
	f := validatedFixture(t)
	write(t, f.userFile, "bogus: 1\n")
	write(t, f.localFi, "log:\n  level: nope\n")
	write(t, f.projFile, "other: 1\n")
	_, err := Load(f.opts(nil, map[string]string{"nope": "x"}))
	ce := asError(t, err)
	var files []string
	for _, p := range ce.Problems {
		files = append(files, p.File+p.Source)
	}
	want := []string{f.userFile, f.projFile, f.localFi, "--nope"}
	if strings.Join(files, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %v", files)
	}
}

func TestInvalidYAMLDoesNotStopOtherLayers(t *testing.T) {
	f := validatedFixture(t)
	write(t, f.userFile, "log: [unclosed\n")
	write(t, f.projFile, "log:\n  level: verbose\n")
	_, err := Load(f.opts(nil, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 2 || ce.Problems[0].File != f.userFile ||
		!strings.Contains(ce.Problems[0].Message, "invalid YAML") || ce.Problems[1].File != f.projFile {
		t.Fatalf("problems = %v", ce.Problems)
	}
}

func TestProblemString(t *testing.T) {
	cases := []struct {
		p    Problem
		want string
	}{
		{Problem{File: "a.yaml", Line: 3, Message: "boom"}, "a.yaml:3: boom"},
		{Problem{Source: "RALPH_X", Message: "boom"}, "environment variable RALPH_X: boom"},
		{Problem{Source: "--x", Message: "boom"}, "flag --x: boom"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestErrorMessage(t *testing.T) {
	e := &Error{Problems: []Problem{
		{File: "a.yaml", Line: 1, Message: "one"},
		{Source: "--x", Message: "two"},
	}}
	want := "invalid configuration:\n  a.yaml:1: one\n  flag --x: two"
	if e.Error() != want {
		t.Fatalf("got %q, want %q", e.Error(), want)
	}
}

func TestErrorCarriesWarnings(t *testing.T) {
	f := newFixture(t)
	f.reg, _ = NewRegistry(Setting{Path: "u", Scope: ScopeUser}, Setting{Path: "n", Allowed: []string{"a"}})
	write(t, f.projFile, "u: x\nn: b\n")
	_, err := Load(f.opts(nil, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 1 || len(ce.Warnings) != 1 || ce.Warnings[0].Path != "u" {
		t.Fatalf("error = %+v", ce)
	}
}

func TestLoadReturnsNilConfigOnError(t *testing.T) {
	f := validatedFixture(t)
	write(t, f.projFile, "bogus: 1\n")
	cfg, err := Load(f.opts(nil, nil))
	if cfg != nil || err == nil {
		t.Fatalf("cfg = %v, err = %v", cfg, err)
	}
}

func TestDefaultsAreValidated(t *testing.T) {
	f := newFixture(t)
	f.reg, _ = NewRegistry(Setting{Path: "mode", Default: "fast", Allowed: []string{"slow"}})
	_, err := Load(f.opts(nil, nil))
	ce := asError(t, err)
	if len(ce.Problems) != 1 || !strings.Contains(ce.Problems[0].Message, `"mode"`) {
		t.Fatalf("problems = %v", ce.Problems)
	}
}

func TestStorageBackupsKeepValidation(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("storage.backups.keep")
	if !ok || s.Validate == nil {
		t.Fatal("storage.backups.keep is not registered with a validator")
	}
	for _, v := range []string{"0", "-1", "abc", "2.5", "", "+3", " 3"} {
		err := s.Validate(v)
		if err == nil || err.Error() != "must be a whole number of at least 1" {
			t.Errorf("Validate(%q) = %v", v, err)
		}
	}
	for _, v := range []string{"1", "5", "20"} {
		if err := s.Validate(v); err != nil {
			t.Errorf("Validate(%q) = %v", v, err)
		}
	}
}

func TestHeadlessBudgetSetting(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("headless.budget_usd")
	if !ok || s.Default != "0" || s.Scope != ScopeProject || s.Description == "" {
		t.Fatalf("headless.budget_usd = %+v, %v", s, ok)
	}
	for _, v := range []string{"0", "5", "0.25"} {
		if err := s.Validate(v); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"-1", "abc", "NaN", "Inf", "-Inf", ""} {
		if err := s.Validate(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ralph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ralph", "project.yaml"), []byte("headless:\n  budget_usd: 0.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{
		Registry:    DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.Get("headless.budget_usd"); !ok || v.Value != "0.25" || v.Layer != LayerProject || len(cfg.Warnings()) != 0 {
		t.Errorf("value = %+v, %v, warnings %v", v, ok, cfg.Warnings())
	}
}

func TestHeadlessTimeoutSetting(t *testing.T) {
	s, ok := DefaultRegistry().Lookup("headless.timeout")
	if !ok || s.Default != "30m" || s.Scope != ScopeProject || s.Description == "" {
		t.Fatalf("headless.timeout = %+v, %v", s, ok)
	}
	for _, v := range []string{"90s", "30m", "1ns"} {
		if err := s.Validate(v); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"0", "0s", "-5m", "soon", ""} {
		if err := s.Validate(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}
