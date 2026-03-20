#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  generate.sh <prompt> [--filename output.png] [--size 512x512]
EOF
  exit 2
}

if [[ "${1:-}" == "" || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
fi

prompt="${1:-}"
shift || true

filename="$(date +%Y-%m-%d-%H-%M-%S)-image.png"
size="512x512"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --filename) filename="${2:-}"; shift 2 ;;
    --size) size="${2:-}"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; usage ;;
  esac
done

AIMA_BASE_URL="${AIMA_BASE_URL:-http://127.0.0.1:6188/v1}"

# Workspace output directory
outdir="${HOME}/.openclaw/workspace/images"
mkdir -p "$outdir"
outpath="${outdir}/${filename}"

# Call AIMA image generation API
response=$(curl -sS -X POST "${AIMA_BASE_URL}/images/generations" \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"z-image\", \"prompt\": $(printf '%s' "$prompt" | python3 -c 'import sys,json; print(json.dumps(sys.stdin.read()))'), \"n\": 1, \"size\": \"${size}\", \"response_format\": \"b64_json\"}")

# Extract base64 image and save
echo "$response" | python3 -c "
import sys, json, base64
data = json.load(sys.stdin)
if 'data' in data and len(data['data']) > 0:
    b64 = data['data'][0].get('b64_json', '')
    if b64:
        with open('${outpath}', 'wb') as f:
            f.write(base64.b64decode(b64))
        print('Image saved: ${outpath}')
    else:
        print('Error: no image data', file=sys.stderr)
        sys.exit(1)
else:
    print(f'Error: {data}', file=sys.stderr)
    sys.exit(1)
"

echo "MEDIA: ${outpath}"
