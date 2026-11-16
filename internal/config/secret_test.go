package config

import (
	"strings"
	"testing"
)

const literal = "ghp_abc123SECRETvalue"

func secretFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	r, err := NewRegistry(Setting{Path: "gh.token", Kind: KindSecret, Flag: "gh-token"}, Setting{Path: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	f.reg = r
	return f
}

func TestParseSecretRefValid(t *testing.T) {
	ref, err := ParseSecretRef("env:GITHUB_TOKEN")
	if err != nil || ref != (SecretRef{Scheme: "env", Name: "GITHUB_TOKEN"}) {
		t.Fatalf("ref = %+v, err = %v", ref, err)
	}
}

func TestParseSecretRefRejects(t *testing.T) {
	for _, in := range []string{"", "GITHUB_TOKEN", "env:", "env:1BAD", "file:/x", "env:A B"} {
		if _, err := ParseSecretRef(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestSecretRefErrorDoesNotEchoInput(t *testing.T) {
	_, err := ParseSecretRef(literal)
	if err == nil || strings.Contains(err.Error(), literal) {
		t.Fatalf("err = %v", err)
	}
}

func TestLiteralSecretRejectedWithoutEcho(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: "+literal+"\n")
	_, err := Load(f.opts(nil, nil))
	if err == nil {
		t.Fatal("literal accepted")
	}
	msg := err.Error()
	if strings.Contains(msg, literal) || !strings.Contains(msg, f.projFile+":2") || !strings.Contains(msg, `"gh.token"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretResolvesFromEnv(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:MY_TOK\n")
	c := mustLoad(t, f.opts(map[string]string{"MY_TOK": "s3cret"}, nil))
	got, err := c.Secret("gh.token")
	if err != nil || got != "s3cret" || !c.SecretResolves("gh.token") {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestSecretUnsetVariable(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:MY_TOK\n")
	c := mustLoad(t, f.opts(nil, nil))
	_, err := c.Secret("gh.token")
	if err == nil || !strings.Contains(err.Error(), "MY_TOK") || c.SecretResolves("gh.token") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretEmptyVariableIsUnset(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:MY_TOK\n")
	c := mustLoad(t, f.opts(map[string]string{"MY_TOK": ""}, nil))
	if _, err := c.Secret("gh.token"); err == nil || c.SecretResolves("gh.token") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretOnNonSecretSetting(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "plain: env:MY_TOK\n")
	c := mustLoad(t, f.opts(map[string]string{"MY_TOK": "x"}, nil))
	if _, err := c.Secret("plain"); err == nil || c.SecretResolves("plain") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Secret("nope"); err == nil {
		t.Fatal("unknown setting resolved")
	}
}

func TestSecretUnsetSetting(t *testing.T) {
	c := mustLoad(t, secretFixture(t).opts(nil, nil))
	if _, err := c.Secret("gh.token"); err == nil {
		t.Fatal("unset setting resolved")
	}
}

func TestSecretReferenceFromEnvLayer(t *testing.T) {
	f := secretFixture(t)
	c := mustLoad(t, f.opts(map[string]string{"RALPH_GH_TOKEN": "env:MY_TOK", "MY_TOK": "v"}, nil))
	if got, err := c.Secret("gh.token"); err != nil || got != "v" {
		t.Fatalf("got %q, err %v", got, err)
	}
	_, err := Load(f.opts(map[string]string{"RALPH_GH_TOKEN": literal}, nil))
	if err == nil || strings.Contains(err.Error(), literal) || !strings.Contains(err.Error(), "RALPH_GH_TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretFlagLiteralRejectedWithoutEcho(t *testing.T) {
	f := secretFixture(t)
	_, err := Load(f.opts(nil, map[string]string{"gh-token": literal}))
	if err == nil || strings.Contains(err.Error(), literal) || !strings.Contains(err.Error(), "--gh-token") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDoesNotResolveSecrets(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:UNSET_VAR\n")
	mustLoad(t, f.opts(nil, nil))
}
