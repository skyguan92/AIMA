# U13 Smoke Matrix

- Date: 2026-04-20
- AIMA repo: `HEAD=44bc4c7e362d`
- Build method: `make all`
- Injected build version: `v0.4-dev`
- Injected build time: `2026-04-20T10:58:58Z`

## Verdict

`KNOWN ISSUE`

The smoke command set itself is stable on all devices that were reachable in
this round, but the U13 acceptance bar is still unmet:

- only 7/10 target devices were reachable over SSH in this round;
- all reachable devices reported `aima v0.4-dev`, not the required `v0.4.0`.

## What Was Verified

1. A current cross-built binary from repo `HEAD=44bc4c7e362d` was distributed to
   the reachable target devices in isolated paths (`~/aima-smoke-u13/`).

2. The same smoke command set was executed per device:
   - `aima version`
   - `aima hal detect`
   - `aima engine list`
   - `aima model list`
   - `aima deploy list`
   - `aima device status`

3. Seven devices were reachable and all six commands completed with `exit=0`:
   - `dev-mac`
   - `test-win`
   - `gb10`
   - `gb10-4t`
   - `linux-1`
   - `amd395`
   - `w7900d`

4. Three devices were unreachable and timed out at SSH connect:
   - `aibook`
   - `m1000`
   - `metax-n260`

5. No reachable device crashed on the smoke command set.
   - `hal detect`, `engine list`, `model list`, `deploy list`, and `device status`
     all returned successfully on every reachable device.

6. The version acceptance condition is not satisfied yet.
   - All seven reachable devices reported `aima v0.4-dev`
   - U13 requires `v0.4.0`
   - This is consistent with current build metadata rules: non-tagged `HEAD`
     builds from the `v0.4` development line report `v0.4-dev`

## Evidence

- `90-matrix-summary.md`
- `dev-mac/`
- `test-win/`
- `gb10/`
- `gb10-4t/`
- `linux-1/`
- `amd395/`
- `w7900d/`
- `aibook/`
- `m1000/`
- `metax-n260/`
