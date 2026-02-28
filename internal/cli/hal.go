package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newHalCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hal",
		Short: "Hardware abstraction layer — detect capabilities and collect metrics",
	}

	cmd.AddCommand(
		newHalDetectCmd(app),
		newHalMetricsCmd(app),
	)

	return cmd
}

func newHalDetectCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Detect hardware capabilities (GPU, CPU, RAM, NPU)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.ToolDeps.DetectHardware == nil {
				return fmt.Errorf("hardware.detect not available")
			}
			data, err := app.ToolDeps.DetectHardware(cmd.Context())
			if err != nil {
				return err
			}
			var pretty json.RawMessage = data
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}

func newHalMetricsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Collect current hardware metrics (GPU utilization, memory, temperature)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.ToolDeps.CollectMetrics == nil {
				return fmt.Errorf("hardware.metrics not available")
			}
			data, err := app.ToolDeps.CollectMetrics(cmd.Context())
			if err != nil {
				return err
			}
			var pretty json.RawMessage = data
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}
