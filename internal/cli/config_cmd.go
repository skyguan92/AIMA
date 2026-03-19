package cli

import (
	"fmt"

	"github.com/jguan/aima/internal/mcp"
	"github.com/spf13/cobra"
)

func newConfigCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set persistent configuration",
	}

	cmd.AddCommand(
		newConfigGetCmd(app),
		newConfigSetCmd(app),
	)

	return cmd
}

func newConfigGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !mcp.IsValidConfigKey(args[0]) {
				return fmt.Errorf("unknown config key %q; supported keys: %s", args[0], mcp.SupportedConfigKeysString())
			}
			value, err := app.ToolDeps.GetConfig(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get config %s: %w", args[0], err)
			}
			if mcp.IsSensitiveConfigKey(args[0]) {
				value = "***"
			}
			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

func newConfigSetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !mcp.IsValidConfigKey(args[0]) {
				return fmt.Errorf("unknown config key %q; supported keys: %s", args[0], mcp.SupportedConfigKeysString())
			}
			if err := app.ToolDeps.SetConfig(cmd.Context(), args[0], args[1]); err != nil {
				return fmt.Errorf("set config %s: %w", args[0], err)
			}
			display := args[1]
			if mcp.IsSensitiveConfigKey(args[0]) {
				display = "***"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], display)
			return nil
		},
	}
}
