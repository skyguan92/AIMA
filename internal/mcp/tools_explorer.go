package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

func registerExplorerTools(s *Server, deps *ToolDeps) {
	// explorer.status
	s.RegisterTool(&Tool{
		Name:        "explorer.status",
		Description: "Show the autonomous Explorer's current state: tier, active plan, schedule config, and last run time.",
		InputSchema: schema(""),
		Handler: func(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
			if deps.ExplorerStatus == nil {
				return ErrorResult("explorer.status not implemented"), nil
			}
			data, err := deps.ExplorerStatus(ctx)
			if err != nil {
				return nil, fmt.Errorf("explorer status: %w", err)
			}
			return TextResult(string(data)), nil
		},
	})

	// explorer.config
	s.RegisterTool(&Tool{
		Name:        "explorer.config",
		Description: "Get or update the Explorer's schedule configuration (gap_scan_interval, sync_interval, quiet hours, etc).",
		InputSchema: schema(
			`"action":{"type":"string","description":"get or set","enum":["get","set"]},`+
				`"key":{"type":"string","description":"Config key: gap_scan_interval, sync_interval, full_audit_interval, quiet_start, quiet_end, max_concurrent_runs, enabled"},`+
				`"value":{"type":"string","description":"New value for set action"}`,
			"action"),
		Handler: func(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
			if deps.ExplorerConfig == nil {
				return ErrorResult("explorer.config not implemented"), nil
			}
			data, err := deps.ExplorerConfig(ctx, params)
			if err != nil {
				return nil, fmt.Errorf("explorer config: %w", err)
			}
			return TextResult(string(data)), nil
		},
	})

	// explorer.trigger
	s.RegisterTool(&Tool{
		Name:        "explorer.trigger",
		Description: "Manually trigger an Explorer gap scan cycle. Useful for testing or when you want immediate exploration.",
		InputSchema: schema(""),
		Handler: func(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
			if deps.ExplorerTrigger == nil {
				return ErrorResult("explorer.trigger not implemented"), nil
			}
			data, err := deps.ExplorerTrigger(ctx)
			if err != nil {
				return nil, fmt.Errorf("explorer trigger: %w", err)
			}
			return TextResult(string(data)), nil
		},
	})
}
