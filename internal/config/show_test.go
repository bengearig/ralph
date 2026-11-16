package config

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTextColumns(t *testing.T) {
	f := newFixture(t)
	write(t, f.projFile, "log:\n  level: debug\n")
	cfg, err := Load(f.opts(map[string]string{"RALPH_PROJECT_ID": "abc"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, cfg, f.root); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "SETTING VALUE LAYER SOURCE" {
		t.Fatalf("header = %q", lines[0])
	}
	rows := map[string][]string{}
	var paths []string
	for _, l := range lines[1:] {
		fields := strings.Fields(l)
		rows[fields[0]] = fields
		paths = append(paths, fields[0])
	}
	if len(rows) != len(f.reg.Settings()) {
		t.Fatalf("rows = %d, want %d:\n%s", len(rows), len(f.reg.Settings()), buf.String())
	}
	wantSource := filepath.Join(".ralph", "project.yaml") + ":2"
	if got := rows["log.level"]; len(got) != 4 || got[1] != "debug" || got[2] != "project" || got[3] != wantSource {
		t.Errorf("log.level row = %q", got)
	}
	if got := rows["project.id"]; len(got) != 4 || got[1] != "abc" || got[2] != "env" || got[3] != "RALPH_PROJECT_ID" {
		t.Errorf("project.id row = %q", got)
	}
	for i := 1; i < len(paths); i++ {
		if paths[i-1] >= paths[i] {
			t.Errorf("rows not sorted: %v", paths)
		}
	}
}

func TestWriteTextUnsetAndOutsideRoot(t *testing.T) {
	f := newFixture(t)
	write(t, f.userFile, "log:\n  level: warn\n")
	cfg, err := Load(f.opts(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, cfg, f.root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), f.userFile+":2") {
		t.Errorf("absolute user file source missing:\n%s", buf.String())
	}
	var unset []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "(unset)") {
			unset = strings.Fields(l)
		}
	}
	if len(unset) != 4 || unset[2] != "-" || unset[3] != "-" {
		t.Errorf("unset row = %q\n%s", unset, buf.String())
	}
}

func TestWriteTextSecretShowsReferenceAndStatus(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:GH_TOKEN\n")
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"GH_TOKEN": literal}, "env:GH_TOKEN (resolves)"},
		{nil, "env:GH_TOKEN (does not resolve)"},
	} {
		cfg, err := Load(f.opts(tc.env, nil))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := WriteText(&buf, cfg, f.root); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("output missing %q:\n%s", tc.want, buf.String())
		}
		if strings.Contains(buf.String(), literal) {
			t.Errorf("secret leaked:\n%s", buf.String())
		}
		buf.Reset()
		if err := WriteJSON(&buf, cfg); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), literal) {
			t.Errorf("secret leaked in JSON:\n%s", buf.String())
		}
	}
}

func TestWriteJSONShape(t *testing.T) {
	f := secretFixture(t)
	write(t, f.projFile, "gh:\n  token: env:GH_TOKEN\n")
	cfg, err := Load(f.opts(map[string]string{"GH_TOKEN": literal}, nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, cfg); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if len(got) != len(f.reg.Settings()) {
		t.Fatalf("entries = %d", len(got))
	}
	by := map[string]map[string]any{}
	for _, e := range got {
		by[e["setting"].(string)] = e
	}
	tok := by["gh.token"]
	if tok["value"] != "env:GH_TOKEN" || tok["layer"] != "project" || tok["resolves"] != true ||
		!strings.HasSuffix(tok["source"].(string), "project.yaml:2") {
		t.Errorf("gh.token = %v", tok)
	}
	plain := by["plain"]
	if v, ok := plain["value"]; !ok || v != nil {
		t.Errorf("plain value = %v, present %v", v, ok)
	}
	if v, ok := plain["layer"]; !ok || v != nil {
		t.Errorf("plain layer = %v, present %v", v, ok)
	}
	if _, ok := plain["resolves"]; ok {
		t.Errorf("non-secret has resolves: %v", plain)
	}
	if plain["source"] != "-" {
		t.Errorf("plain source = %v", plain["source"])
	}
}
