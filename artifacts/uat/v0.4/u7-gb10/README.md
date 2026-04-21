# U7 on `gb10-4T`

- Date: 2026-04-20
- Host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `~/aima-uat/u7/data`, separate proxy/MCP ports `6289/9191`

## Verdict

`PASS`

This validated the intended fix in `cmd/aima/tooldeps_agent.go`: MCP-initiated tuning is detached from the HTTP request context and keeps running after the initial HTTP response closes.

## What Happened

1. Sent a raw MCP HTTP `tuning.start` request for:
   - model: `gemma-4-26b-a4b-it`
   - engine: `vllm-gemma4-blackwell`
   - hardware: `nvidia-gb10-arm64`
   - search space: `gpu_memory_utilization=[0.74]`
   - benchmark profile: `concurrency=1`, `rounds=1`, `num_requests=1`, `input_tokens=128`, `max_tokens=64`

2. The initial HTTP response returned in about `30ms` with session id `87cce8429c91e4b7`, while the session status was already `running`.

3. On a later, separate MCP connection, the same session still reported `running progress=0/1` while the Gemma container was still in startup / health-check.

4. The session later completed successfully and `tuning.results` returned one real benchmark result:
   - `throughput_tps=20.2735`
   - `ttft_p50_ms=192.721`
   - `tpot_p50_ms=46.8930`
   - `benchmark_id=c55bc30e74786dd3`
   - `config_id=d067bf14f2abc765`

5. The server log shows the full lifecycle:
   - `tuning: testing config progress=1/1`
   - Docker deploy of `gemma-4-26b-a4b-it-vllm-gemma4-blackwell`
   - benchmark completion
   - `auto-promote: first golden config`
   - `tuning: best config already deployed`

## Evidence

- `01-tuning-start.json`: initial raw MCP HTTP response, returned in `30ms`
- `status/002-after-2min-mcp.json`: separate MCP connection still sees the session `running`
- `status/003.json` → `status/006.json`: periodic polling until the session becomes `completed`
- `03-docker-after-2min.txt`: container still `Up ... (health: starting)` at the detached-run checkpoint
- `04-tuning-results.json`: final MCP `tuning.results` payload
- `05-serve-final.log`: end-to-end server log with deploy, benchmark, and auto-promote

## Note

An unrelated wrapper discrepancy was observed: `aima --remote http://127.0.0.1:9191 tuning status/results` returned `{"status":"no session"}` while raw MCP HTTP on the same endpoint returned the correct session/result. U7 was judged on the direct MCP path, which is the scope of this test.
