# U12 on `dev-mac`

- Date: 2026-04-20
- Host: `dev-mac` (local)
- Binary: `./build/aima-darwin-arm64`
- Isolation: `AIMA_DATA_DIR=/tmp/aima-uat-u12`, proxy `127.0.0.1:6192`, MCP `127.0.0.1:9192`

## Verdict

`PASS`

The deprecated tool names tested here all returned explicit JSON-RPC `-32601` errors with `Tool not found: ...`. No silent drop, HTTP 500, or process crash was observed.

## What Was Verified

Against a local `aima serve --mcp` instance, sent raw MCP `tools/call` requests for:

- `app.register`
- `app.provision`
- `app.list`
- `power.mode`
- `power.history`

All five responses had the same shape:

- HTTP transport succeeded
- JSON-RPC `error.code = -32601`
- `error.message = "Tool not found: <deprecated-name>"`

## Evidence

- `01-app-register.json`
- `02-app-provision.json`
- `03-app-list.json`
- `04-power-mode.json`
- `05-power-history.json`
