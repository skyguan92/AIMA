# U6 on `light-salt` (`test-win`)

- Date: 2026-04-20
- Host: `jguan@100.114.25.35` (`Light-Salt`, Windows 11, RTX 4060 8GB)
- Binary: `aima v0.4-dev` (`44bc4c7`)
- Isolation: used a separate `AIMA_DATA_DIR` under `C:\Users\jguan\aima-uat\u6\data`, then deleted that remote directory after capture

## Verdict

`KNOWN ISSUE`

U6 did not pass on Windows. Two acceptance branches diverged from the doc:

1. The unregistered cloud-rejection path works before `serve`.
   - `device status` was `unregistered`
   - `support.invite_code` was absent
   - `knowledge sync --push` failed cleanly with `device not registered with aima-service`

2. `aima serve` without any explicit invite code did **not** stay `unregistered` / `pending`.
   - Within the first second it auto-registered successfully
   - `device status` stayed `registered` across later samples
   - This matches the current code path that falls back to the default invite code when no explicit invite is present

3. The "local inference still works without identity" acceptance could not be satisfied on this Windows path.
   - `deploy Qwen3-0.6B-Q8_0 --engine llamacpp` generated a native `llama-server.exe` command with `--gpu-memory-utilization`
   - The Windows `llama-server.exe` rejected that flag with `error: invalid argument: --gpu-memory-utilization`
   - `deploy list` then showed the deployment in `failed` / `process exited before readiness`

## Evidence

- `03-device-status-before.txt`: pre-serve state was `unregistered`
- `04-sync-push-before-serve.txt`: unregistered Central push was gracefully rejected
- `05-deploy.txt`: native deploy command emitted on Windows
- `06-deploy-list-after-fail.txt`: failed deployment with `invalid argument: --gpu-memory-utilization`
- `09-device-status-after-serve.txt`: post-serve state was `registered`
- `08-identity-mirror-check.txt`: canonical/private identity keys were mirrored after registration

## Notes

- The `*.txt` captures are PowerShell redirections, so they are encoded as UTF-16LE.
- I did not keep local copies of raw `device.token` / `recovery_code`; only the mirror-check result was preserved.
