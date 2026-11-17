package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/jira"
	"github.com/bengearig/ralph/internal/version"
)

// openTaskManager builds the task manager the configuration selects, with its settings.
func openTaskManager(_ context.Context, a *app) (taskmanager.System, taskmanager.Settings, error) {
	if a.root == "" {
		return nil, taskmanager.Settings{}, errNotInRepository
	}
	if a.setting("taskmanager.provider") != "jira" {
		return nil, taskmanager.Settings{}, errors.New("no task manager is configured: set taskmanager.provider to jira")
	}
	for _, path := range []string{"jira.site", "jira.user", "jira.project"} {
		if a.setting(path) == "" {
			return nil, taskmanager.Settings{}, fmt.Errorf("%s is not set: set it to use Jira", path)
		}
	}
	settings, err := taskmanager.FromConfig(a.cfg)
	if err != nil {
		return nil, taskmanager.Settings{}, err
	}
	client, err := jira.New(jira.Config{
		Site:      settings.Site,
		User:      settings.User,
		Project:   settings.Project,
		Board:     settings.Board,
		Token:     func() (string, error) { return a.cfg.Secret("jira.token") },
		Timeout:   settings.Timeout,
		UserAgent: "ralph/" + version.String(),
	})
	if err != nil {
		return nil, taskmanager.Settings{}, err
	}
	return client, settings, nil
}
