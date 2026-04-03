#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  speak.sh <text> [--filename output.wav] [--voice default] [--api speech|tts]
           [--response-format wav] [--speed 1.0]
           [--reference-audio value] [--reference-text text] [--x-vector-only]
           [--mode auto|voice_clone|custom_voice|voice_design]
           [--speaker id] [--instruct text] [--language name]
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
x_vector_only="false"
mode=""
speaker=""
instruct=""
language=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --filename) filename="${2:-}"; shift 2 ;;
    --voice) voice="${2:-}"; shift 2 ;;
    --api) api="${2:-}"; shift 2 ;;
    --response-format) response_format="${2:-}"; shift 2 ;;
    --speed) speed="${2:-}"; shift 2 ;;
    --reference-audio) reference_audio="${2:-}"; shift 2 ;;
    --reference-text) reference_text="${2:-}"; shift 2 ;;
    --x-vector-only) x_vector_only="true"; shift 1 ;;
    --mode) mode="${2:-}"; shift 2 ;;
    --speaker) speaker="${2:-}"; shift 2 ;;
    --instruct) instruct="${2:-}"; shift 2 ;;
    --language) language="${2:-}"; shift 2 ;;
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

resolve_asr_model() {
  python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ.get("OPENCLAW_CONFIG_PATH", Path.home() / ".openclaw" / "openclaw.json"))
fallback = ""
try:
    data = json.loads(path.read_text())
except Exception:
    print(fallback)
    raise SystemExit(0)

provider = data.get("models", {}).get("providers", {}).get("aima-media", {})
models = provider.get("models", [])
for entry in models:
    if not isinstance(entry, dict):
        continue
    model_id = entry.get("id")
    inputs = entry.get("input")
    if isinstance(model_id, str) and model_id and inputs == ["text"]:
        print(model_id)
        raise SystemExit(0)

print(fallback)
PY
}

prepare_reference_audio() {
  REFERENCE_AUDIO_INPUT="$reference_audio" python3 - <<'PY'
import base64
import mimetypes
import os
from pathlib import Path

value = os.environ.get("REFERENCE_AUDIO_INPUT", "").strip()
if not value:
    print("")
    raise SystemExit(0)

expanded = Path(os.path.expanduser(value))
if expanded.is_file():
    mime = mimetypes.guess_type(str(expanded))[0] or "audio/wav"
    encoded = base64.b64encode(expanded.read_bytes()).decode("ascii")
    print(f"data:{mime};base64,{encoded}")
else:
    print(value)
PY
}

transcribe_reference_audio() {
  local file_path="$1"
  local asr_model="$2"
  [[ -n "$file_path" && -n "$asr_model" && -f "$file_path" ]] || return 1
  local tmp_json
  tmp_json="$(mktemp)"
  local auth_args=()
  if [[ -n "${AIMA_API_KEY}" ]]; then
    auth_args=(-H "Authorization: Bearer ${AIMA_API_KEY}")
  fi
  if ! curl -sS "${auth_args[@]}" \
    -F "model=${asr_model}" \
    -F "file=@${file_path}" \
    "${base_v1}/audio/transcriptions" \
    -o "$tmp_json"; then
    rm -f "$tmp_json"
    return 1
  fi
  python3 - "$tmp_json" <<'PY'
import json
import sys

try:
    data = json.load(open(sys.argv[1], "r", encoding="utf-8"))
except Exception:
    raise SystemExit(1)

text = data.get("text")
if isinstance(text, str) and text.strip():
    print(text.strip())
    raise SystemExit(0)
raise SystemExit(1)
PY
  local status=$?
  rm -f "$tmp_json"
  return $status
}

TTS_MODEL="${AIMA_TTS_MODEL:-$(resolve_tts_model)}"
AIMA_API_KEY="${AIMA_API_KEY:-$(resolve_tts_api_key)}"
ASR_MODEL="${AIMA_ASR_MODEL:-$(resolve_asr_model)}"

base_v1="${AIMA_BASE_URL%/}"
if [[ "$base_v1" == */v1 ]]; then
  base_root="${base_v1%/v1}"
else
  base_root="${base_v1}"
  base_v1="${base_root}/v1"
fi

build_payload() {
  local prepared_reference_audio="$1"
  local prepared_reference_text="$2"
  local prepared_x_vector_only="$3"
  TTS_MODEL="$TTS_MODEL" \
  TTS_TEXT="$text" \
  TTS_VOICE="$voice" \
  TTS_API="$api" \
  TTS_RESPONSE_FORMAT="$response_format" \
  TTS_SPEED="$speed" \
  TTS_REFERENCE_AUDIO="$prepared_reference_audio" \
  TTS_REFERENCE_TEXT="$prepared_reference_text" \
  TTS_X_VECTOR_ONLY="$prepared_x_vector_only" \
  TTS_MODE="$mode" \
  TTS_SPEAKER="$speaker" \
  TTS_INSTRUCT="$instruct" \
  TTS_LANGUAGE="$language" \
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

if os.environ.get("TTS_X_VECTOR_ONLY", "").strip().lower() == "true":
    payload["x_vector_only_mode"] = True

mode = os.environ.get("TTS_MODE", "").strip()
if mode:
    payload["mode"] = mode

speaker = os.environ.get("TTS_SPEAKER", "").strip()
if speaker:
    payload["speaker"] = speaker

instruct = os.environ.get("TTS_INSTRUCT", "").strip()
if instruct:
    payload["instruct"] = instruct

language = os.environ.get("TTS_LANGUAGE", "").strip()
if language:
    payload["language"] = language

print(json.dumps(payload, ensure_ascii=False))
PY
}

outdir="${HOME}/.openclaw/workspace/audio"
mkdir -p "$outdir"
outpath="${outdir}/${filename}"

reference_audio_value="$(prepare_reference_audio)"
reference_text_value="$reference_text"
if [[ -n "$reference_audio" && -z "$reference_text_value" ]]; then
  expanded_reference_audio="$(python3 - <<'PY' "$reference_audio"
import os
import sys
from pathlib import Path
print(Path(os.path.expanduser(sys.argv[1])).resolve())
PY
)"
  if [[ -f "$expanded_reference_audio" ]]; then
    if transcript="$(transcribe_reference_audio "$expanded_reference_audio" "$ASR_MODEL" 2>/dev/null)"; then
      reference_text_value="$transcript"
    else
      x_vector_only="true"
    fi
  fi
fi

payload="$(build_payload "$reference_audio_value" "$reference_text_value" "$x_vector_only")"
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
