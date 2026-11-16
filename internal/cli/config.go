package cli

import (
	"os"

	"github.com/bengearig/ralph/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand(app *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect ralph's configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigShowCommand(app))
	return cmd
}

func newConfigShowCommand(app *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration and where each value comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if asJSON {
				return config.WriteJSON(out, app.cfg)
			}
			displayRoot := app.root
			if displayRoot == "" {
				displayRoot, _ = os.Getwd()
			}
			return config.WriteText(out, app.cfg, displayRoot)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the configuration as JSON")
	return cmd
}
