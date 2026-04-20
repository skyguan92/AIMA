# U9 on `gb10-4T`

- Date: 2026-04-20
- Host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Binary: `aima v0.4-dev` from repo `HEAD=44bc4c7e362d`
- Isolation: `AIMA_DATA_DIR=~/aima-uat/u9/data`

## Verdict

`KNOWN ISSUE`

The intended timeout/warmup behavior works when the deployment is labeled with a concrete engine asset name, but the common `--engine vllm-nightly` type path still reports a false `ready=true` before health/warmup are actually satisfied.

## What Was Verified

1. Type-alias path: `aima deploy qwen3.5-9b --engine vllm-nightly --config port=18080`
   - `deploy status` reported `ready=true` at `2026-04-20T09:32:45Z`, one second after container start.
   - Direct requests right after that still failed with `Recv failure: Connection reset by peer`.
   - Container logs showed it was still in repair-init `pip install`, not serving yet.
   - Inference from code + behavior: Docker status lookup only resolves engine assets by `metadata.name`, but this path labels the deployment as `vllm-nightly` (type), so `health_check` and `warmup` are skipped.

2. Exact-asset path: `aima deploy qwen3.5-9b --engine vllm-nightly-blackwell --config port=18081`
   - Progress moved as expected: `initializing` -> `model_init` -> `loading_shards`.
   - Deployment start time was `2026-04-20T09:36:07Z`; `deploy status` became `ready=true` at `2026-04-20T09:38:06Z`.
   - Direct `/health` returned `200`.
   - Docker logs show `/health` probes followed by `POST /v1/chat/completions` `200`, so warmup gating was active before the final ready transition.

3. Forced timeout path via overlay asset `vllm-nightly-blackwell-u9-fail`
   - Overlay changed `startup.health_check.path` to `/definitely-not-here` and `timeout_s` to `20`.
   - `aima run qwen3.5-9b --engine vllm-nightly-blackwell-u9-fail --config port=18082 --no-pull`
     started at `2026-04-20T09:40:51Z`.
   - Docker deploy happened at `2026-04-20T09:41:28Z`.
   - The command returned at `2026-04-20T09:41:49Z` with `Timed out waiting for deployment to be ready.`
   - After the server eventually came up, `/health` returned `200` while `/definitely-not-here` returned `404`.
   - This confirms the timeout path did not hang and was driven by the configured health-check path.

## Evidence

- `02-normal-deploy.stdout` / `02-normal-deploy.stderr`: type-alias deploy kickoff
- `04-normal-health.txt` / `05-normal-chat.txt`: false-ready path still resetting connections
- `07-type-engine-false-ready.txt`: container state from the false-ready path
- `08-normal-exact-deploy.stdout` / `08-normal-exact-deploy.stderr`: exact-asset deploy kickoff
- `status/normal-exact/029.txt` -> `038.txt`: exact-asset startup progress through ready
- `10-normal-exact-docker-logs-2.txt` / `11-normal-exact-docker-logs-3.txt`: exact-asset logs, including `/health` and warmup chat requests
- `12-normal-exact-health.txt`: direct `200 OK` on the exact-asset path
- `vllm-nightly-blackwell-u9-fail.yaml`: overlay asset used for the forced-timeout case
- `14-fail-run.stdout` / `14-fail-run.stderr`: `aima run` timeout behavior
- `status/fail/017.txt` -> `020.txt`: failing deployment remains `ready=false`
- `16-fail-correct-health.txt` / `17-fail-bad-health.txt`: correct-vs-bad health path contrast
- `18-fail-docker-logs.txt`: server eventually starts and repeatedly returns `404` on the configured bad health path
