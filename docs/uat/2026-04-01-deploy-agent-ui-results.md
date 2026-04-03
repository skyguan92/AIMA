# Deploy / Agent / UI UAT Results

Date: 2026-04-01
Branch: `feat/uat-deploy-agent-hardening`
Scope: `deploy` / `undeploy`, model -> direct agent, UI deployment + agent status panels
Round 1 defer: `gb10`

## Summary

This round found one real cross-process regression during live UAT, then closed the remaining confirmed follow-up bugs in code + regression tests:

- immediate same-name redeploy after `undeploy` could be hidden from `deploy.list` / UI for up to 15 seconds
- root cause: deleted-deployment tombstone suppression compared only `started_at_unix` (second precision)
- fix: prefer precise `start_time` timestamp when deciding whether a replacement deployment started after delete

Post-UAT hardening also fixed:

- `undeploy` now waits for the native port to actually close before returning, so immediate same-port redeploy does not race stale socket state
- local `agent.ask` now preflights the current model context window from live AIMA proxy status and fails early with an actionable error instead of blindly forwarding into a backend `400`
- loopback / local AIMA LLM requests no longer inherit the old fixed 5-minute client timeout; local direct-agent requests now use a longer timeout policy suitable for CPU-only inference paths

After the fix:

- `deploy.run` preserved explicit config overrides in CLI, MCP, and dry-run output
- `undeploy` removed runtime state, proxy backends, and wrote tombstones / rollback snapshots
- UI deployment panel showed real startup stage fields from backend status:
  - `phase=starting`
  - `startup_phase=loading_model`
  - `startup_progress=35`
  - `startup_message=Loading model...`
- UI deployment panel then transitioned to ready and showed the real backend address
- UI deployment panel removed the card after `undeploy`
- immediate same-port native redeploy is covered by runtime regression tests
- local direct-agent context-window mismatch is covered by agent regression tests before the request ever reaches the backend

## Local Dev-Mac UAT

Environment:

- isolated temp data dir copied from local `~/.aima/aima.db`
- reused local `models/` and `dist/` via symlink
- local `serve` on `127.0.0.1:16188`

### Deploy / UI / Undeploy

Validated with real CLI + HTTP UI bridge + browser:

- dry-run:
  - `deploy Qwen3-4B-Q5_K_M --engine llamacpp --dry-run --config port=18080 --config ctx_size=2048 --max-cold-start 30`
  - output preserved `port=18080`, `ctx_size=2048`, and `max_cold_start_s`
- run:
  - `run Qwen3-4B-Q5_K_M --engine llamacpp --config port=18080 --config ctx_size=2048 --max-cold-start 30 --no-pull`
  - CLI showed real startup progress before ready
- undeploy:
  - `undeploy Qwen3-4B-Q5_K_M`
  - deployment disappeared from UI and did not resurrect on refresh

Evidence captured:

- initial UI: `output/playwright/local-uat-initial.png`
- ready deployment card: `output/playwright/local-uat-starting.png`
- post-undeploy UI: `output/playwright/local-uat-after-undeploy.png`
- fixed startup-stage UI:
  - `output/playwright/local-uat-starting-fixed-3.png`
  - UI snapshot showed:
    - deployment name `Qwen3-4B-Q5_K_M`
    - detail `native starting`
    - stage text `Loading model...`
    - progress `35%`

Backend startup-stage evidence after the fix:

- `POST /api/v1/tools/deploy.list` returned `phase=starting`, `ready=false`, `startup_phase=loading_model`, `startup_progress=35`
- same deployment transitioned to `phase=running`, `ready=true` at ~6.2s

### Model -> Direct Agent

Validated with real local proxy + UI config panel + agent status:

- local proxy model list:
  - `GET /v1/models` returned `Qwen3-4B-Q5_K_M`
  - later returned `Qwen3-0.6B-Q8_0`
- configured through CLI:
  - `llm.endpoint = http://127.0.0.1:16188/v1`
  - `llm.model = Qwen3-0.6B-Q8_0`
  - `llm.user_agent = AIMA-UAT/1.0`
  - `llm.extra_params = {"temperature":0}`
- `agent status` returned `agent_available: true`
- UI after reload + 2s wait showed:
  - mode `L3a Agent`
  - `LLM Model = Qwen3-0.6B-Q8_0`
  - `LLM Endpoint = http://127.0.0.1:16188/v1`
  - screenshot: `output/playwright/local-uat-agent-configured.png`

Direct `ask` findings:

- `Qwen3-4B-Q5_K_M` deployed with `ctx_size=2048` failed immediately:
  - backend error: request `13677 tokens` exceeded context `2048`
  - this proved `agent.ask` really routed into the local model
- redeploying a 16K-context local model removed the context-size failure
- `Qwen3-0.6B-Q8_0` with `ctx_size=16384` accepted the agent request and backend logs showed prompt processing progress for a `13677` token prompt
- proxy audit confirmed the request routed to the local backend:
  - `model=Qwen3-0.6B-Q8_0`
  - `backend=127.0.0.1:18083`
- the end-to-end `ask` still exceeded the HTTP client timeout and failed after ~5 minutes with:
  - `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`
- conclusion from live UAT: direct-agent routing is functionally correct, but a CPU-only local 0.6B/16K deployment is not operationally fast enough for practical `agent.ask`

Post-UAT code hardening changed the operational behavior:

- the small-context case now fails locally during client preflight with a clear `ctx_size` / prompt-budget error
- the local-loopback case no longer hard-fails at exactly 5 minutes due only to the client timeout policy

## Remote Matrix

Round-1 collection followed the "ALL COLLECT, THEN ANALYZE" pattern before local fixes were applied.

| Host | Reachability | Agent | Deploy / UI Relevant State |
|------|--------------|-------|----------------------------|
| `test-win` | reachable | true | 3 native deployments ready; useful Windows baseline |
| `linux-1` | reachable | false | 1 failed native deployment: `process exited before readiness` |
| `amd395` | reachable | false | 1 failed native deployment: stale metadata / port in use |
| `hygon` | reachable | false | no deployments |
| `m1000` | reachable | false | 1 ready native deployment on `127.0.0.1:8000` |
| `w7900d` | reachable | false | no models, no deployments |
| `qjq2` | unreachable | — | recorded unreachable |
| `metax-n260` | unreachable | — | recorded unreachable |
| `aibook` | unreachable in recollect | — | earlier baseline existed, but round-1 recollect could not reconnect |

Notes:

- `test-win` and `w7900d` needed explicit IP / port instead of SSH alias
- `gb10` was intentionally deferred in this round

## Residual Risks

- local direct-agent success still depends heavily on deployment context size and inference latency
- very slow local CPU deployments may still be operationally impractical even after removing the old 5-minute client ceiling
- a follow-up round should repeat the direct-agent success case on a faster local backend path:
  - Metal-enabled local deployment
  - or a reachable non-GB10 edge box with a ready local LLM and UI access
