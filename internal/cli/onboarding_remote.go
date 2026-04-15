package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// RemoteMCPClient is the global state captured by the root --remote and
// --api-key flags. When Endpoint is empty CLI subcommands run against their
// in-process ToolDeps closures (offline-first); when it is set, subcommands
// that opt into remote dispatch issue a JSON-RPC tools/call against
// {endpoint}/mcp so a single CLI on a laptop can drive any number of
// `aima serve` instances across the fleet.
type RemoteMCPClient struct {
	Endpoint string
	APIKey   string
}

// Configured reports whether --remote was supplied (either via flag or the
// AIMA_REMOTE env var).
func (c *RemoteMCPClient) Configured() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.Endpoint) != ""
}

// CallTool posts a JSON-RPC tools/call request and returns the inner result
// text as raw JSON (matching the shape ToolDeps closures produce, so the
// per-subcommand printers work unchanged whether running local or remote).
//
// The endpoint MUST point at the MCP HTTP listener (default :9090, enabled by
// `aima serve --mcp`) — NOT the proxy/UI port (default :6188). On 404 the
// returned error includes a hint to that effect, which is by far the most
// common configuration mistake in UAT.
func (c *RemoteMCPClient) CallTool(ctx context.Context, toolName string, args map[string]any) (json.RawMessage, error) {
	if args == nil {
		args = map[string]any{}
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode mcp request: %w", err)
	}

	endpoint, err := normalizeRemoteEndpoint(c.Endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build mcp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	httpClient := &http.Client{Timeout: 30 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Port-mismatch is the #1 UAT confusion: users point --remote at :6188
		// (UI/proxy) instead of the dedicated MCP port :9090 enabled by
		// `aima serve --mcp`. Both "connection refused" (port closed) and 404
		// (wrong path on :6188) land here or in the StatusCode branch below —
		// give the same hint at both layers so the diagnosis is self-service.
		if strings.Contains(err.Error(), "connection refused") {
			return nil, fmt.Errorf("call %s: %w — is --remote pointing at the MCP HTTP port? (default :9090, enabled by `aima serve --mcp`; the UI/proxy port :6188 does NOT serve /mcp)", endpoint, err)
		}
		return nil, fmt.Errorf("call %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read mcp response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("mcp %s: %s returned 404 — is --remote pointing at the MCP HTTP port? (default :9090, enabled by `aima serve --mcp`; the UI/proxy port :6188 will NOT serve /mcp)", toolName, endpoint)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp %s returned %d: %s", toolName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var rpc struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpc); err != nil {
		return nil, fmt.Errorf("decode mcp response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil || len(rpc.Result.Content) == 0 {
		return nil, fmt.Errorf("mcp %s: empty result", toolName)
	}
	text := rpc.Result.Content[0].Text
	if rpc.Result.IsError {
		msg := strings.TrimSpace(text)
		if msg == "" {
			msg = "(no message)"
		}
		return nil, fmt.Errorf("mcp %s failed: %s", toolName, msg)
	}
	return json.RawMessage(text), nil
}

// normalizeRemoteEndpoint validates the URL and prepends a default scheme when
// the user supplied a bare host. Without this, http.NewRequest produces an
// inscrutable "first path segment in URL cannot contain colon" error that
// nobody wants to debug at 2am.
func normalizeRemoteEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("remote endpoint is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return strings.TrimRight(raw, "/") + "/mcp", nil
}

// envOrFlag returns the env-var value if the flag was left at its zero value.
func envOrFlag(flag, envKey string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	return strings.TrimSpace(os.Getenv(envKey))
}
