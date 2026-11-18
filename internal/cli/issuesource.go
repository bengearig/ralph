package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/issuesource/jira"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/version"
	"github.com/spf13/cobra"
)

// openIssueSource builds the issue source the configuration selects.
func openIssueSource(_ context.Context, a *app) (issuesource.Source, error) {
	if a.root == "" {
		return nil, errNotInRepository
	}
	settings, err := issuesource.FromConfig(a.cfg)
	if err != nil {
		return nil, err
	}
	if settings.Provider != "jira" {
		return nil, &mcpserver.NotConfiguredError{Message: "no issue source is configured: set issues.provider to jira"}
	}
	for _, path := range []string{"jira.site", "jira.user"} {
		if a.setting(path) == "" {
			return nil, &mcpserver.NotConfiguredError{Message: fmt.Sprintf("%s is not set: set it to use Jira", path)}
		}
	}
	timeout, err := time.ParseDuration(a.setting("jira.timeout"))
	if err != nil {
		return nil, fmt.Errorf("jira.timeout: %w", err)
	}
	return jira.New(jira.Config{
		Site:      strings.TrimSuffix(a.setting("jira.site"), "/"),
		User:      a.setting("jira.user"),
		Token:     func() (string, error) { return a.cfg.Secret("jira.token") },
		Timeout:   timeout,
		UserAgent: "ralph/" + version.String(),
	})
}

func newIssueCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Read tickets from the issue source",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newIssueShowCommand(a))
	return cmd
}

func newIssueShowCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <ref>",
		Short: "Show one ticket from the issue source",
		Long: "Show one ticket from the issue source: its key, type, status, title, address, parent and body.\n" +
			"The ref is a ticket key such as ABC-123 or the address of the ticket. Nothing is written, in ralph or in the issue source.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if a.root == "" {
				return errNotInRepository
			}
			if _, err := os.Stat(project.StorePath(a.root)); errors.Is(err, fs.ErrNotExist) {
				return errors.New(errNoDatabase)
			} else if err != nil {
				return err
			}
			src, err := openIssueSource(ctx, a)
			if err != nil {
				return err
			}
			ref, err := src.Parse(args[0])
			if err != nil {
				return err
			}
			issue, err := src.Get(ctx, ref)
			if err != nil {
				return err
			}
			out := mcpserver.NewIssueResult(issue)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			printIssue(cmd.OutOrStdout(), issue)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the ticket as JSON")
	return cmd
}

func printIssue(out io.Writer, i issuesource.Issue) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s  %s\n%s\n", i.Ref.Key, i.Type, i.Status, i.Title, i.URL)
	if i.Parent != "" {
		fmt.Fprintf(&b, "parent: %s\n", i.Parent)
	}
	if strings.TrimSpace(i.Body) != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(i.Body, "\n"))
	}
	_, _ = out.Write([]byte(b.String()))
}
