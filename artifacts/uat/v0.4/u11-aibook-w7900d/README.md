# U11 on `w7900d` and `aibook`

- Date: 2026-04-20
- Hosts checked:
  - `root@36.151.243.68:21985` (`wx-ms-w7900d-0003`, AMD W7900D x8)
  - `aibook@100.106.164.54` (planned TTS/ASR host)
  - `qujing@100.91.39.109` (`gb10-4T`) as fallback asset scan only
- Binary baseline: current repo `HEAD=44bc4c7`

## Verdict

`KNOWN ISSUE`

U11 could not be completed with the current lab state.

- The planned TTS/ASR host `aibook` was still unreachable.
- `gb10-4T` did not have locally discoverable TTS/ASR model assets to use as a safe substitute.
- `w7900d` did have live T2I/T2V services, but they did not expose a benchmark-compatible surface that could be exercised end to end without changing shared long-running services.

So U11 does not meet its acceptance bar yet.

## What Was Verified

1. The original TTS/ASR target host was not reachable.
   - `ssh aibook@100.106.164.54` timed out
   - this blocks the documented TTS/ASR path directly

2. `w7900d` does have shared multimodal services running.
   - `ComfyUI` is listening on `:8188`
   - `t2v-server.py` is listening on `:8006`
   - both are existing shared services; this run did not restart or reconfigure them

3. The live T2I endpoint is not compatible with the current `image_gen` benchmark requester contract.
   - `GET /` on `:8188` returns the ComfyUI HTML UI
   - `GET /system_stats` works and confirms the service is alive
   - `POST /v1/images/generations` returns `405`
   - `POST /prompt` with the request shape used by the fallback image requester returns `500`
   - so the current benchmark adapter cannot talk to the existing ComfyUI service as-is

4. The checked T2V service shape is nominally close, but the live service was not responsive enough to benchmark safely.
   - the deployed script at `/disk/ssd1/t2v-server.py` serves:
     - `GET /health`
     - `POST /`
   - that is compatible in principle with the current `video_gen` requester
   - but the live service timed out on both:
     - `GET http://127.0.0.1:8006/health`
     - `POST http://127.0.0.1:8006`
   - because this is a shared service on a busy host, this run did not recycle it just to force UAT progress

5. No safe fallback TTS/ASR assets were found on `gb10-4T`.
   - the asset scan under likely model roots returned empty for:
     - `tts`
     - `asr`
     - `funasr`
     - `mooer`
     - `qwen3-tts`
     - `qwen3-asr`

## Interpretation

This is not a single crash or a single CLI bug. U11 is blocked by a combination of current lab conditions:

- missing reachability for the intended speech host;
- no local speech-model fallback on the free GB10 test box;
- existing W7900D services exposing interfaces that the current benchmark adapters cannot safely consume as a drop-in target.

With those constraints, this run could not produce the required five-modality `benchmark_results` evidence without modifying shared services or inventing test wrappers that would not reflect the deployed surface.

## Evidence

- `01-aibook-ssh.txt`: direct SSH timeout for the planned TTS/ASR host
- `02-w7900d-services.txt`: listening ports and shared-service process list on `w7900d`
- `03-w7900d-http-probe.txt`: live HTTP probe showing ComfyUI incompatibility and T2V timeouts
- `04-w7900d-t2v-server.py.txt`: deployed T2V server implementation and route shape
- `05-gb10-4t-tts-asr-assets.txt`: empty fallback TTS/ASR asset scan on `gb10-4T`
