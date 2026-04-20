# U2 on `gb10-4T`

- Date: 2026-04-20
- Host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `~/aima-uat/u2/data`, separate proxy/MCP ports `6286/9186`

## Verdict

`KNOWN ISSUE`

The "partial preserve" branch is still not healthy once a tune run has one successful cell and then loses the next cell:

- the first cell completed normally and wrote a real benchmark/configuration row;
- after the second cell lost its backend, the run remained stuck in `running`;
- `summary_json` under-counted the run as `total_cells=1, success_cells=1` even though the same `tuning_session` still reported `progress=1, total=2`.

So the implementation does preserve the first successful benchmark row in DB, but it does not surface the partial run correctly at the exploration summary level, and it can fail to converge after the failed cell.

## What Was Verified

1. A real 2-cell tune run was started through isolated MCP `explore.start`.
   - run id: `ca3b9f262f1d3583`
   - tuning session: `42aa70e762d9015f`
   - model / engine: `gemma-4-26b-a4b-it` + `vllm-gemma4-blackwell`
   - search space: `gpu_memory_utilization=[0.74, 1.2]`

2. The first cell completed successfully.
   - `tuning: testing config progress=1/2 config=map[gpu_memory_utilization:0.74]`
   - isolated DB kept one real benchmark/configuration row
   - run summary already promoted:
     - `benchmark_id=d984dc10a425a85f`
     - `config_id=d067bf14f2abc765`
     - `throughput_tps=18.3714409353017`

3. The second cell did not fail naturally from `1.2`.
   - deploy fitness clamped `gpu_memory_utilization 1.20 -> 0.93`
   - to force the intended partial-success branch on an isolated host, the second container was explicitly removed after it started

4. After the second backend disappeared, the run state became inconsistent.
   - `serve.log` recorded `sync: removing stale backend model=gemma-4-26b-a4b-it`
   - `docker ps` became empty
   - by `2026-04-20T11:51:23Z`, the run still remained `running`

5. The partial summary was wrong.
   - `summary_json.total_cells=1`
   - `summary_json.success_cells=1`
   - but the embedded `tuning_session` still reported:
     - `status=running`
     - `progress=1`
     - `total=2`
     - `results_len=1`

6. The code path matches the observed under-count.
   - `internal/agent/exploration.go` currently sets:
     - `payload["total_cells"] = len(session.Results)`
     - `payload["success_cells"] = len(session.Results)`
   - this explains why the exploration-level summary only reflects successful results, not attempted cells

## Evidence

- `00-version.txt`: isolated binary version
- `01-serve-start.txt`: isolated serve bootstrap
- `02-explorer-status-raw.json`: baseline explorer status
- `03-explore-start.json`: raw MCP start response with run id
- `status/020.json` to `status/022.json`: first successful cell appears while run is still in progress
- `05-run-status-after-injected-failure.json`: post-fault run status
- `06-serve-after-injected-failure.log`: second-cell stale backend removal in serve log
- `07-db-counts.txt`: isolated DB still contains only the successful benchmark/config row
- `08-docker-after-injected-failure.txt`: no backend container left
- `09-summary-mismatch.txt`: compact summary mismatch (`1/1` vs tuning `1/2`)
- `10-remote-cleanup.txt`: isolated serve, container, and data dir cleaned up
