# U10 on `light-salt`

- Date: 2026-04-20
- Host: `jguan@100.114.25.35` (`Light-Salt`, Windows 11, RTX 4060 Laptop 8GB)
- Binary: `aima v0.4-dev` from repo `HEAD=44bc4c7e362d`
- Isolation: `AIMA_DATA_DIR=%USERPROFILE%\aima-uat\u10\data`

## Verdict

`KNOWN ISSUE`

The onboarding flow is partially working on Windows native, but the deploy step still fails on this machine:

- the recommended top model (`qwen3-30b-a3b`) starts a real `llama-server.exe` load, yet onboarding times out after 60s while the model is still loading;
- a smaller local GGUF fallback (`Qwen3-0.6B-Q8_0`) still fails in the Windows native launch path before readiness is reached.

## What Was Verified

1. Onboarding status / scan / recommend all returned successfully.
   - `status` detected the expected hardware profile: `nvidia-rtx4060-x86`, `Ada`, `8188 MiB`.
   - `scan` found local models and handled missing cloud registration gracefully (`device not registered with aima-service`).
   - `recommend` ranked LLMs first and preferred larger fittable models first:
     - top 1: `qwen3-30b-a3b`
     - then `qwen3.5-35b-a3b`, `qwen3-32b`, `qwen3-8b`, ...

2. Top recommended model path: `onboarding deploy --model qwen3-30b-a3b --engine llamacpp --yes --json`
   - The flow downloaded the Windows CUDA `llama.cpp` bundle successfully.
   - It resolved the local GGUF path and launched `llama-server.exe`.
   - After 60s, onboarding returned `deployment started but not ready within 1m0s`.
   - Follow-up checks showed the deployment still existed as `starting`, with:
     - `startup_phase=loading_model`
     - `startup_progress=35`
     - `/health` = `503`
     - `/v1/models` = `503`
   - `llama-server.exe` was still alive and using about `9.5 GiB` working set, so this was a real slow load, not a fake start.

3. Small local GGUF fallback: `onboarding deploy --model "Qwen3-0.6B-Q8_0" --engine llamacpp --yes --json`
   - This went through the auto-detect fallback path because the imported GGUF name is not in catalog.
   - The generated Windows command still included `--gpu-memory-utilization 0.5`.
   - The run failed in native Windows launch bookkeeping:
     - `start Qwen3-0.6B-Q8_0 via schtasks: discover PID after schtasks launch: binary=llama-server.exe port=8080`
   - Final `deploy list` was empty.

## Lab Notes

This U10 item is only partially covered so far:

- `light-salt`: tested, failed as above
- `amd395`: skipped for now because a shared `aima-serve` is already running
- `w7900d`: skipped for now because Ollama / T2I / T2V services are already running
- `aibook`: SSH timed out during this round
- `metax-n260`: SSH timed out during this round

## Evidence

- `01-status.json`
- `02-scan.json`
- `03-recommend.json`
- `04-import-qwen3-30b.json`
- `06-deploy.stderr`
- `08-deploy-status.txt`
- `09-endpoint-check.txt`
- `11-deploy-logs.txt`
- `12-status-after-30s.json`
- `13-endpoint-check-after-30s.txt`
- `16-import-qwen3-0.6b.json`
- `17-deploy-small.stderr`
- `18-deploy-list-final.json`
