package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newMCPCmd(app *App) *cobra.Command {
	var profile string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve MCP over stdio",
		Long:  "Serve the AIMA MCP server over stdin/stdout for local agent integrations such as OpenClaw.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.MCP == nil {
				return fmt.Errorf("mcp server not available")
			}
			p, err := parseMCPProfile(profile)
			if err != nil {
				return err
			}
			if p != "" {
				app.MCP.SetProfile(p)
			}
			return app.MCP.ServeStdio(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "MCP tool discovery profile: operator, patrol, explorer (default: all tools)")
	return cmd
}
