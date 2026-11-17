package taskmanager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/config"
)

// Settings are the task manager settings, read without any network access.
type Settings struct {
	Provider string
	Site     string
	User     string
	Project  string
	// Types maps the levels brief, slice and task to a ticket type; an empty
	// type means the level has no ticket.
	Types map[string]string
	// States maps a ralph state to a status name.
	States       map[string]string
	Flow         []string
	Sprint       string
	Board        int
	SprintLevels []string
	AutoSync     bool
	PollInterval time.Duration
	SyncTimeout  time.Duration
	Timeout      time.Duration
}

// Mapped reports whether units of level have a ticket.
func (s Settings) Mapped(level string) bool { return s.Types[level] != "" }

// FromConfig reads the task manager settings from cfg.
func FromConfig(cfg *config.Config) (Settings, error) {
	str := func(path string) string {
		v, _ := cfg.Get(path)
		return v.Value
	}
	dur := func(path string) (time.Duration, error) {
		return time.ParseDuration(str(path))
	}
	s := Settings{
		Provider: str("taskmanager.provider"),
		Site:     strings.TrimSuffix(str("jira.site"), "/"),
		User:     str("jira.user"),
		Project:  str("jira.project"),
		Types: map[string]string{
			"brief": str("jira.type.brief"),
			"slice": str("jira.type.slice"),
			"task":  str("jira.type.task"),
		},
		States:   map[string]string{},
		Sprint:   str("jira.sprint"),
		AutoSync: str("jira.auto_sync") == "true",
	}
	for _, item := range list(cfg, "jira.states") {
		state, status, ok := strings.Cut(item, "=")
		if !ok {
			return Settings{}, fmt.Errorf("taskmanager: jira.states item %q is not STATE=Status", item)
		}
		s.States[state] = status
	}
	s.Flow = list(cfg, "jira.flow")
	s.SprintLevels = list(cfg, "jira.sprint_levels")
	if len(s.SprintLevels) == 0 {
		s.SprintLevels = []string{"slice", "task"}
	}
	if v := str("jira.board"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Settings{}, fmt.Errorf("taskmanager: jira.board: %w", err)
		}
		s.Board = n
	}
	var err error
	for _, d := range []struct {
		path string
		into *time.Duration
	}{
		{"jira.poll_interval", &s.PollInterval},
		{"jira.sync_timeout", &s.SyncTimeout},
		{"jira.timeout", &s.Timeout},
	} {
		if *d.into, err = dur(d.path); err != nil {
			return Settings{}, fmt.Errorf("taskmanager: %s: %w", d.path, err)
		}
	}
	return s, nil
}

func list(cfg *config.Config, path string) []string {
	items, _ := cfg.List(path)
	return items
}
