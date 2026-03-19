package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jguan/aima/internal/support"
)

func newAskForHelpCmd(app *App) *cobra.Command {
	var (
		endpoint   string
		inviteCode string
		workerCode string
		noWait     bool
		wait       bool
	)

	cmd := &cobra.Command{
		Use:   "askforhelp [request]",
		Short: "Connect to the support service and optionally create a remote help task",
		Long:  "Register this AIMA instance as a support device for aima-service-new, then optionally create a help task from a natural-language request.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.ToolDeps == nil || app.ToolDeps.SupportAskForHelp == nil {
				return fmt.Errorf("askforhelp not wired")
			}

			description := strings.TrimSpace(strings.Join(args, " "))
			data, err := app.ToolDeps.SupportAskForHelp(cmd.Context(), description, endpoint, inviteCode, workerCode)
			if err != nil {
				return fmt.Errorf("askforhelp: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), formatJSON(data))

			shouldWait := wait || (description != "" && !noWait)
			if !shouldWait || app.Support == nil {
				return nil
			}

			runErr := app.Support.Run(cmd.Context(), support.RunOptions{
				StopWhenIdle: description != "" || !wait,
				Prompt:       supportPrompt(cmd),
				Notify:       supportNotify(cmd),
			})
			if errors.Is(runErr, context.Canceled) {
				return nil
			}
			if runErr != nil {
				return fmt.Errorf("askforhelp wait: %w", runErr)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Support service base URL (persisted as support.endpoint)")
	cmd.Flags().StringVar(&inviteCode, "invite-code", "", "Invite code for first-time device registration (persisted as support.invite_code)")
	cmd.Flags().StringVar(&workerCode, "worker-code", "", "Worker enrollment code for first-time device registration (persisted as support.worker_code)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Create the task and return immediately instead of waiting for completion")
	cmd.Flags().BoolVar(&wait, "wait", false, "Keep polling in the foreground even without a new request")

	return cmd
}

func supportPrompt(cmd *cobra.Command) support.PromptFunc {
	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()

	return func(ctx context.Context, prompt support.Prompt) (string, error) {
		_ = ctx
		if prompt.Question == "" {
			return "", nil
		}
		fmt.Fprintf(out, "\n[askforhelp] %s\n> ", prompt.Question)
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(answer), nil
	}
}

func supportNotify(cmd *cobra.Command) support.NotifyFunc {
	out := cmd.OutOrStdout()
	return func(ctx context.Context, notification support.Notification) {
		_ = ctx
		switch {
		case notification.TaskID != "":
			fmt.Fprintf(out, "\n[askforhelp] task %s finished: %s\n", notification.TaskID, notification.TaskStatus)
		case notification.Message != "":
			fmt.Fprintf(out, "\n[askforhelp] %s\n", notification.Message)
		}
	}
}
