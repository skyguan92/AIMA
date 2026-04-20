# U5 on `gb10-4T` via local mock Central

- Date: 2026-04-20
- Edge host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Central host: `dev-mac` via local mock Central on `127.0.0.1:18086`, reverse-tunneled into `gb10-4T`
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `~/aima-uat/u5/data`, separate proxy/MCP ports `6285/9185`

## Verdict

`KNOWN ISSUE`

The advisory rejection path is only partially healthy.

- Attempt 1 proved the simple rejection loop works when Explorer rejects an advisory before execution: Central moved to `rejected` and stored the rejection feedback.
- Attempt 2 proved the deeper execution path is still broken: once an advisory becomes a real `central.advisory` validation plan, the plan can stay stuck `active` after the backend disappears, and Central never receives rejection feedback.

So U5 does not meet its acceptance bar yet.

## What Was Verified

1. Isolated edge `serve --mcp` was started on `gb10-4T`.
   - proxy / MCP ports: `6285/9185`
   - `central.endpoint` pointed to `http://127.0.0.1:18086`
   - a local mock Central was exposed to the remote host through SSH reverse forwarding

2. Attempt 1: Central rejection from local execution facts works.
   - advisory id: `adv-u5-reject-1`
   - target: `gemma-4-26b-a4b-it + vllm-gemma4-blackwell`
   - Explorer received the advisory and rejected it immediately with:
     - `reason="not in ready combos"`
   - mock Central stored:
     - `status=rejected`
     - `feedback="not in ready combos"`

3. Attempt 2: a real advisory validation plan was created.
   - advisory id: `adv-u5-reject-2`
   - target: `GLM-4.1V-9B-Thinking + vllm-nightly`
   - bad config: `dtype=definitely-not-real`
   - Explorer persisted a real plan row:
     - `id=advisory-adv-u5-reject-2`
     - `trigger=central.advisory`
     - `status=active`
     - `progress=0/1`

4. Attempt 2 then failed to converge.
   - `serve.log` shows Docker deploy with the invalid dtype
   - the backend later disappeared:
     - `sync: removing stale backend model=glm-4.1v-9b-thinking`
   - `docker ps` became empty
   - but the plan still stayed `active`
   - mock Central still remained at:
     - `status=delivered`
     - no rejection feedback event

## Interpretation

This UAT item exposed two different behaviors:

- pre-execution advisory rejection can close the loop correctly;
- post-plan execution failure can leave the advisory loop hanging instead of transitioning to `rejected`.

That means the release-gate scenario is not robust yet for the case where validation actually starts and then fails.

## Evidence

- `00a-mock-central-attempt1.log`: first advisory delivered then rejected by feedback
- `00-mock-central.log`: second advisory mock-Central log
- `01-version.txt`: isolated remote binary version
- `02-serve-start.txt`: isolated edge `serve --mcp` startup
- `03-explorer-baseline.json`: baseline explorer status before pull
- `03a-config-seeded.txt`: seeded `device.id` plus central endpoint/api key
- `04-central-sync-pull.json`: first `central.sync pull` result
- `04a-attempt1-summary.txt`: compact first-attempt rejection summary
- `05-central-sync-pull-attempt2.json`: second `central.sync pull` result
- `05a-attempt2-summary.txt`: compact second-attempt stuck-plan summary
- `06-serve-after-attempt2.log`: second-attempt serve log tail
- `07-plans-after-attempt2.txt`: `exploration_plans` still `active`
- `08-docker-after-attempt2.txt`: no backend container left
- `09-central-state-after-attempt2.json`: advisory still `delivered`
- `10-remote-cleanup.txt`: isolated edge cleanup
- `11-local-cleanup.txt`: local mock Central + tunnel cleanup
