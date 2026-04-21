# U8 on `dev-mac`

- Date: 2026-04-20
- Host: local `dev-mac`
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `/tmp/aima-uat-u8`

## Verdict

`PASS`

This validated the 600s edge HTTP timeout used by `RequestAdvise` / `RequestScenario` in `cmd/aima/tooldeps_integration.go`.

## What Happened

1. Configured an isolated edge data dir with:
   - `central.endpoint = http://127.0.0.1:18081`
   - fake registered device identity (`device.id`, `device.token`, `device.registration_state`)

2. Started a local mock Central server that:
   - accepts `POST /api/v1/advise`
   - intentionally sleeps `130s`
   - then returns a valid `recommendation + advisory` JSON payload

3. Ran:
   - `aima knowledge advise qwen3-8b --engine vllm --intent low-latency`

4. The command completed successfully after `130.192s` with `returncode=0`.
   - stdout contained a normalized edge-facing advisory payload
   - stderr contained only normal startup logs
   - no `context deadline exceeded` or timeout error appeared

## Why This Passes U8

- The response time was greater than 120 seconds
- The edge client still returned success
- This directly exercises the `syncHTTPClient := &http.Client{Timeout: 600 * time.Second}` path used by `RequestAdvise`

Inference:
An older ~120s client timeout would have failed this synthetic `130s` response. This is an inference from the measured duration, not a replay of the old binary.

## Evidence

- `mock_central.py`: synthetic slow Central endpoint
- `00-mock-central.log`: request received at `09:06:19Z`, response sent at `09:08:29Z`
- `01-knowledge-advise.json`: CLI timing + stdout/stderr capture (`elapsed_seconds=130.192`, `returncode=0`)
