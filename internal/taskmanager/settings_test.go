package taskmanager

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
)

func load(t *testing.T, project string, env map[string]string) *config.Config {
	t.Helper()
	root := t.TempDir()
	if project != "" {
		dir := filepath.Join(root, ".ralph")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(config.Options{
		ProjectRoot: root,
		Getenv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFromConfigDefaults(t *testing.T) {
	s, err := FromConfig(load(t, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "none" || s.Site != "" || s.User != "" || s.Project != "" || s.Sprint != "" || s.Board != 0 {
		t.Errorf("settings = %+v", s)
	}
	if s.Types["brief"] != "Feature" || s.Types["slice"] != "Task" || s.Types["task"] != "" {
		t.Errorf("Types = %v", s.Types)
	}
	if !s.AutoSync || s.PollInterval != 5*time.Minute || s.SyncTimeout != 15*time.Second || s.Timeout != 30*time.Second {
		t.Errorf("settings = %+v", s)
	}
	if len(s.States) != 0 || len(s.Flow) != 0 {
		t.Errorf("States = %v, Flow = %v", s.States, s.Flow)
	}
}

func TestFromConfigSprintLevelsDefault(t *testing.T) {
	s, _ := FromConfig(load(t, "", nil))
	if !slices.Equal(s.SprintLevels, []string{"slice", "task"}) {
		t.Errorf("SprintLevels = %v", s.SprintLevels)
	}
	s, _ = FromConfig(load(t, "jira:\n  sprint_levels: []\n", nil))
	if !slices.Equal(s.SprintLevels, []string{"slice", "task"}) {
		t.Errorf("empty SprintLevels = %v", s.SprintLevels)
	}
	s, _ = FromConfig(load(t, "jira:\n  sprint_levels: [brief]\n", nil))
	if !slices.Equal(s.SprintLevels, []string{"brief"}) {
		t.Errorf("set SprintLevels = %v", s.SprintLevels)
	}
}

func TestFromConfigMapped(t *testing.T) {
	s, _ := FromConfig(load(t, "", nil))
	for level, want := range map[string]bool{"brief": true, "slice": true, "task": false, "other": false} {
		if got := s.Mapped(level); got != want {
			t.Errorf("Mapped(%q) = %v, want %v", level, got, want)
		}
	}
	s, _ = FromConfig(load(t, "jira:\n  type:\n    task: Sub-task\n    brief: ''\n", nil))
	if !s.Mapped("task") {
		t.Error("task is not mapped after setting its type")
	}
}

func TestFromConfigStatesMap(t *testing.T) {
	s, err := FromConfig(load(t, "jira:\n  states:\n    - TODO=Backlog\n    - DONE=Closed\n    - TODO=Selected\n", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.States) != 2 || s.States["TODO"] != "Selected" || s.States["DONE"] != "Closed" {
		t.Errorf("States = %v", s.States)
	}
}

func TestFromConfigNoNetwork(t *testing.T) {
	cfg := load(t, "jira:\n  project: ABC\n  board: 7\n  flow: [A, B]\n  sprint: S1\n", map[string]string{
		"RALPH_JIRA_SITE":            "https://example.invalid/",
		"RALPH_JIRA_USER":            "me@example.com",
		"RALPH_TASKMANAGER_PROVIDER": "jira",
	})
	s, err := FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "jira" || s.Site != "https://example.invalid" || s.User != "me@example.com" || s.Project != "ABC" ||
		s.Board != 7 || s.Sprint != "S1" || !slices.Equal(s.Flow, []string{"A", "B"}) {
		t.Errorf("settings = %+v", s)
	}
}
