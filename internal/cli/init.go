package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// isTTY returns true if stdin is a terminal (not piped or redirected).
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// newInitCmd is the legacy `aima init` command. As of v0.4.x it is a thin
// wrapper over ToolDeps.OnboardingInit so human users walk the same code path
// as the Web UI wizard and the MCP onboarding tool.
//
// Deprecated: prefer `aima onboarding init`. This command is kept for backward
// compatibility and will be removed in v0.5.x.
func newInitCmd(app *App) *cobra.Command {
	var (
		yesFlag bool
		k3sFlag bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install infrastructure stack (Docker tier by default, --k3s for full K3S+HAMi)",
		Long: `Initialize AIMA infrastructure on this device.

Tiers:
  aima init        Docker + nvidia-ctk + aima-serve (lightweight container inference)
  aima init --k3s  + K3S + HAMi (GPU partitioning, multi-model scheduling)

The K3S tier is a superset of the Docker tier. Missing files are auto-downloaded
when confirmed (or use --yes to skip the prompt).

Deprecated: prefer ` + "`aima onboarding init`" + `. This command is kept for backward
compatibility and will be removed in v0.5.x.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(),
				"[deprecated] `aima init` will be removed in v0.5.x. Please use `aima onboarding init` instead.")

			if app.ToolDeps.OnboardingInit == nil {
				return fmt.Errorf("onboarding.init not available")
			}

			ctx := cmd.Context()
			tier := "docker"
			if k3sFlag {
				tier = "k3s"
			}

			// --yes also implies allow-download, matching legacy semantics where
			// non-interactive invocations should proceed without prompting.
			allowDownload := yesFlag || !isTTY()

			tierLabel := "Docker"
			if k3sFlag {
				tierLabel = "K3S (full stack)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initializing AIMA infrastructure stack [%s tier]...\n", tierLabel)

			data, err := app.ToolDeps.OnboardingInit(ctx, tier, allowDownload)
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}

			var env struct {
				Result json.RawMessage `json:"result"`
				Events []struct {
					Type string         `json:"type"`
					Data map[string]any `json:"data"`
				} `json:"events"`
			}
			if err := json.Unmarshal(data, &env); err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			w := cmd.OutOrStdout()
			for _, ev := range env.Events {
				printEvent(w, ev.Type, ev.Data)
			}

			var res struct {
				AllReady    bool `json:"all_ready"`
				StackStatus struct {
					Docker string `json:"docker"`
					K3S    string `json:"k3s"`
				} `json:"stack_status"`
				Tier string `json:"tier,omitempty"`
			}
			if err := json.Unmarshal(env.Result, &res); err != nil {
				fmt.Fprintln(w, string(env.Result))
				return nil
			}

			fmt.Fprintf(w, "\nInit complete: tier=%s all_ready=%v (docker=%s k3s=%s)\n",
				res.Tier, res.AllReady, res.StackStatus.Docker, res.StackStatus.K3S)

			if res.AllReady {
				fmt.Fprintln(w, "All components ready. Run 'aima serve' to begin.")
			} else {
				fmt.Fprintln(w, "Some components are not ready. Check events above.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Skip download confirmation prompt")
	cmd.Flags().BoolVar(&k3sFlag, "k3s", false, "Install full K3S+HAMi stack (GPU partitioning, multi-model scheduling)")
	return cmd
}
