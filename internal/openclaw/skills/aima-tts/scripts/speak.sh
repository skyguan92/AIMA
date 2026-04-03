#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  speak.sh <text> [--filename output.wav] [--voice default] [--api speech|tts]
           [--response-format wav] [--speed 1.0]
           [--reference-audio value] [--reference-text text]
EOF
  exit 2
}

if [[ "${1:-}" == "" || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
fi

text="${1:-}"
shift || true

filename=""
voice="default"
api="speech"
response_format="wav"
speed=""
reference_audio=""
reference_text=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --filename) filename="${2:-}"; shift 2 ;;
    --voice) voice="${2:-}"; shift 2 ;;
    --api) api="${2:-}"; shift 2 ;;
    --response-format) response_format="${2:-}"; shift 2 ;;
    --speed) speed="${2:-}"; shift 2 ;;
    --reference-audio) reference_audio="${2:-}"; shift 2 ;;
    --reference-text) reference_text="${2:-}"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; usage ;;
  esac
done

AIMA_BASE_URL="${AIMA_BASE_URL:-http://127.0.0.1:6188/v1}"
OPENCLAW_CONFIG_PATH="${OPENCLAW_CONFIG_PATH:-$HOME/.openclaw/openclaw.json}"

case "$api" in
  speech|tts) ;;
  *) echo "Unsupported --api value: $api" >&2; usage ;;
esac

if [[ -z "$filename" ]]; then
  filename="$(date +%Y-%m-%d-%H-%M-%S)-speech.${response_format}"
fi

resolve_tts_model() {
  python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ.get("OPENCLAW_CONFIG_PATH", Path.home() / ".openclaw" / "openclaw.json"))
fallback = "qwen3-tts-0.6b"
try:
    data = json.loads(path.read_text())
except Exception:
    print(fallback)
    raise SystemExit(0)

tts = data.get("messages", {}).get("tts", {})
providers = tts.get("providers", {})
model = providers.get("openai", {}).get("model") or tts.get("openai", {}).get("model")
if isinstance(model, str) and model:
    print(model)
else:
    print(fallback)
PY
}

resolve_tts_api_key() {
  python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ.get("OPENCLAW_CONFIG_PATH", Path.home() / ".openclaw" / "openclaw.json"))
fallback = "local"
try:
    data = json.loads(path.read_text())
except Exception:
    print(fallback)
    raise SystemExit(0)

tts = data.get("messages", {}).get("tts", {})
providers = tts.get("providers", {})
api_key = providers.get("openai", {}).get("apiKey") or tts.get("openai", {}).get("apiKey") or fallback
if isinstance(api_key, str) and api_key:
    print(api_key)
else:
    print(fallback)
PY
}

TTS_MODEL="${AIMA_TTS_MODEL:-$(resolve_tts_model)}"
AIMA_API_KEY="${AIMA_API_KEY:-$(resolve_tts_api_key)}"

base_v1="${AIMA_BASE_URL%/}"
if [[ "$base_v1" == */v1 ]]; then
  base_root="${base_v1%/v1}"
else
  base_root="${base_v1}"
  base_v1="${base_root}/v1"
fi

build_payload() {
  TTS_MODEL="$TTS_MODEL" \
  TTS_TEXT="$text" \
  TTS_VOICE="$voice" \
  TTS_API="$api" \
  TTS_RESPONSE_FORMAT="$response_format" \
  TTS_SPEED="$speed" \
  TTS_REFERENCE_AUDIO="$reference_audio" \
  TTS_REFERENCE_TEXT="$reference_text" \
  python3 - <<'PY'
import json
import os

payload = {
    "model": os.environ["TTS_MODEL"],
    "voice": os.environ["TTS_VOICE"],
}

text_key = "text" if os.environ["TTS_API"] == "tts" else "input"
payload[text_key] = os.environ["TTS_TEXT"]

response_format = os.environ.get("TTS_RESPONSE_FORMAT", "").strip()
if response_format:
    payload["response_format"] = response_format

speed = os.environ.get("TTS_SPEED", "").strip()
if speed:
    try:
        payload["speed"] = float(speed)
    except ValueError:
        payload["speed"] = speed

reference_audio = os.environ.get("TTS_REFERENCE_AUDIO", "").strip()
if reference_audio:
    payload["reference_audio"] = reference_audio

reference_text = os.environ.get("TTS_REFERENCE_TEXT", "").strip()
if reference_text:
    payload["reference_text"] = reference_text

print(json.dumps(payload, ensure_ascii=False))
PY
}

outdir="${HOME}/.openclaw/workspace/audio"
mkdir -p "$outdir"
outpath="${outdir}/${filename}"
payload="$(build_payload)"
curl_args=(-sS -X POST -H "Content-Type: application/json")
if [[ -n "${AIMA_API_KEY}" ]]; then
  curl_args+=(-H "Authorization: Bearer ${AIMA_API_KEY}")
fi

case "$api" in
  speech)
    curl "${curl_args[@]}" "${base_v1}/audio/speech" -d "$payload" -o "$outpath"
    ;;
  tts)
    tmp_json="$(mktemp)"
    trap 'rm -f "$tmp_json"' EXIT
    curl "${curl_args[@]}" "${base_root}/v1/tts" -d "$payload" -o "$tmp_json"
    if ! python3 - "$tmp_json" "$outpath" <<'PY'
import base64
import json
import sys

src, dst = sys.argv[1], sys.argv[2]
with open(src, "r", encoding="utf-8") as fh:
    data = json.load(fh)

audio = data.get("audio_base64")
if not isinstance(audio, str) or not audio:
    raise SystemExit("missing audio_base64 in /v1/tts response")

with open(dst, "wb") as fh:
    fh.write(base64.b64decode(audio))
PY
    then
      echo "Error: TTS returned invalid JSON response" >&2
      cat "$tmp_json" >&2
      exit 1
    fi
    rm -f "$tmp_json"
    trap - EXIT
    ;;
esac

size=$(stat -c%s "$outpath" 2>/dev/null || stat -f%z "$outpath" 2>/dev/null || echo 0)
if [[ "$size" -lt 100 ]]; then
  echo "Error: TTS returned empty or error response" >&2
  cat "$outpath" >&2
  exit 1
fi

echo "Audio saved: ${outpath} (${size} bytes)"
echo "MEDIA: ${outpath}"
