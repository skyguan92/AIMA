package openclaw

import (
	"context"
	"log/slog"
	"time"
)

// StartSyncLoop keeps openclaw.json converged with the current ready local AIMA
// backends. The sync decision lives in the OpenClaw package instead of the CLI.
func StartSyncLoop(ctx context.Context, deps *Deps, interval time.Duration) {
	syncOnce := func() {
		status, err := Inspect(ctx, deps)
		if err != nil {
			slog.Warn("openclaw auto-sync: inspect failed", "error", err)
			return
		}
		if status == nil || status.SyncReady {
			return
		}
		if summaryCount(status.Expected) == 0 && !status.AIMAConfigured {
			return
		}
		if _, err := Sync(ctx, deps, false); err != nil {
			slog.Warn("openclaw auto-sync: sync failed", "error", err)
		}
	}

	syncOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}
