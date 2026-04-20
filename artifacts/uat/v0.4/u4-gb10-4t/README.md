# U4 on `gb10-4T`

- Date: 2026-04-20
- Host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `~/aima-uat/u4/data`, separate proxy/MCP ports `6284/9184`

## Verdict

`PASS`

This validated the release-gate behavior behind "Tier 2 -> Tier 1 degrade" without restarting `aima serve`:

- the active Tier 2 planning cycle failed after the LLM endpoint was broken mid-plan;
- Explorer degraded to the rule-based Tier 1 planner in the same process;
- the degraded plan kept executing deterministic validation work;
- after restoring the endpoint, the same process stayed healthy and the degraded validation wrote a real benchmark/configuration row.

## What Was Verified

1. Baseline Tier 2 before fault injection:
   - `agent.status` reported `agent_available=true`
   - `explorer.status` was idle with top-level `tier=2`

2. Mid-plan LLM outage:
   - while the planner was still in `phase=plan`, `llm.endpoint` was hot-reloaded from `https://api.kimi.com/coding/v1` to `http://127.0.0.1:1/v1`
   - `agent.status` immediately flipped to `agent_available=false`

3. Same-process degrade to Tier 1:
   - `serve.log` recorded:
     - `explorer: tier 2 planner failed`
     - `explorer: degrading to Tier 1 planner`
     - `plan generated ... tier=1 reasoning="rule-based (degraded from Tier 2)"`
   - `explorer.status` stayed `running`, and `active_plan.Tier=1`
   - no `serve` restart was performed

4. Deterministic work continued after degrade:
   - the degraded plan deployed `GLM-4.1V-9B-Thinking / vllm-nightly`
   - once the model became healthy, Explorer entered a real benchmark matrix
   - the first cell completed successfully in `3m20.702s`
   - a real row was written to the isolated DB:
     - `benchmark_id=14807b1bdd291eef`
     - `config_id=ea87b025050e3bf5`
     - `throughput_tps=10.1059698391381`
     - `ttft_ms_p95=122.5408`

5. Recovery without restart:
   - `llm.endpoint` was hot-reloaded back to `https://api.kimi.com/coding/v1`
   - remote `agent.status` returned to `agent_available=true`
   - the same degraded run kept progressing and wrote the first successful benchmark row after recovery

## Important Note

The functional degrade/recover path works, but the status surface is slightly counterintuitive:

- top-level `explorer.status.tier` remained `2`
- the actual degrade is visible in `serve.log` and in `active_plan.Tier=1`

So the implementation exposes "degraded execution" at the active-plan level, not as a temporary top-level `tier=1`.

## Evidence

- `14-15-status-reset.txt`: pre-fault baseline (`agent_available=true`, `explorer tier=2`)
- `21-22-status-after-invalid-endpoint.txt`: endpoint invalidated, `agent_available=false`
- `26-log-after-long-wait.txt`: planner failure + `degrading to Tier 1 planner`
- `27-status-after-long-wait.json`: degraded plan visible as `active_plan.Tier=1`
- `29-30-status-after-restore.txt`: endpoint restored, remote status healthy again without restart
- `33-status-and-db-after-first-cell.txt`: first successful benchmark/config row present in DB
- `34-log-after-first-cell.txt`: benchmark matrix first cell completed successfully
- `36-remote-cleanup.txt`: remote isolated `serve`, container, and data dir cleaned up
