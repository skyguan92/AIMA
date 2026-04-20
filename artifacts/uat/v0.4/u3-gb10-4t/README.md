# U3 on `gb10-4T` via isolated local Central

- Date: 2026-04-20
- Edge host: `qujing@100.91.39.109` (`aitopatom-66c4`, GB10 / Blackwell ARM64)
- Central host: `dev-mac` running repo `cmd/central` on a reverse-tunneled local port
- Support host: local mock `aima-service`, also reverse-tunneled to the remote edge
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: separate `AIMA_DATA_DIR` under `~/aima-uat/u3/data`, separate local Central DB at `artifacts/uat/v0.4/u3-gb10-4t/central.db`

## Verdict

`KNOWN ISSUE`

The onboarding chain is only partially healthy.

- Fresh device registration worked end to end.
- First `knowledge sync --push` ingested the new edge into Central and triggered advisory generation.
- Advisory delivery plus rejection feedback also worked for the shallow path.
- But the deeper validation path did not converge: a known-ready GLM advisory reached `delivered`, the edge started acting on it, and Central never observed a final `validated` or `rejected` state for that advisory.

So U3 does not meet its acceptance bar yet.

## What Was Verified

1. Fresh device registration worked from a clean isolated edge identity.
   - mock support service returned:
     - `device_id=dev-u3-gb10`
     - `token=tok-u3`
     - `recovery_code=rec-u3`
   - the isolated edge moved from unregistered to registered without touching the host's real `~/.aima`

2. First knowledge push successfully seeded Central with real edge data.
   - `05-central-stats-after-push.json` shows:
     - `devices=1`
     - `configurations=68`
     - `benchmarks=73`
     - `knowledge_notes=11`

3. Central post-ingest analysis generated advisories for the new device.
   - analyzer produced a `gap_alert` for:
     - `gemma-4-26b-a4b-it + vllm-gemma4-blackwell`
   - the advisory lifecycle reached `delivered`, proving generation and delivery worked

4. The shallow advisory-feedback loop worked.
   - manually inserted advisory `adv_u3_manual_validate`
   - target:
     - `gemma-4-26b-a4b-it + vllm-gemma4-blackwell`
   - final Central state:
     - `status=rejected`
     - `feedback="not in ready combos"`

5. The deeper validation path still failed to converge.
   - manually inserted advisory `adv_u3_glm_validate`
   - target:
     - `GLM-4.1V-9B-Thinking + vllm-nightly`
   - this was chosen because it had already been observed entering validation on `gb10-4T` in earlier UAT runs
   - final Central state remained:
     - `status=delivered`
     - `validated_at=NULL`
     - no feedback recorded

6. Acceptance was not met.
   - advisory lifecycle for the deep validation path never left `delivered`
   - this run did not produce a new validation artifact attributable to onboarding itself
   - the only stable completion in this isolated U3 run was rejection before execution, not a completed validation benchmark/configuration

## Interpretation

U3 exposed a split behavior:

- registration, ingest, advisory creation, delivery, and early rejection feedback are working;
- once onboarding reaches the deeper validation path, the lifecycle can stall before Central receives a terminal result.

That means the release-gate scenario is not robust enough yet for a newly onboarded device that actually begins advisory validation.

## Evidence

- `04-mock-aima-service-check.json`: canonical registration response from mock support service
- `04-central-stats.json`: empty Central baseline before onboarding
- `05-central-stats-after-push.json`: Central counts after first edge push
- `06-central-advisories-pending.json`: initial post-ingest advisory generation
- `07-central-advisories-all.json`: early advisory list snapshot
- `12-central-advisories-manual-insert.json`: manually inserted validation advisory for Gemma
- `14-central-advisories-after-glm-insert.json`: manually inserted GLM validation advisory
- `15-central-advisories-after-glm-pull.json`: GLM advisory after delivery to edge
- `16-central-advisories-final-window.json`: final advisory window showing `adv_u3_glm_validate` still `delivered`
- `17-central-advisory-summary.txt`: compact SQLite dump of final advisory lifecycle states
- `central.db`: isolated Central SQLite database
