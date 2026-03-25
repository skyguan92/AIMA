---
name: aima-image-gen
description: Generate images using AIMA's local z-image model (OpenAI-compatible API).
metadata: {"openclaw":{"emoji":"🎨","requires":{"bins":["curl"]},"always":true}}
---

# AIMA Image Generation (z-image)

Generate images using AIMA's local z-image model via the OpenAI-compatible Images API.

## Generate

```bash
{baseDir}/scripts/generate.sh "your image description" --filename output.png
```

Useful flags:

```bash
{baseDir}/scripts/generate.sh "a cute cat" --filename cat.png --size 512x512
{baseDir}/scripts/generate.sh "mountain sunset" --filename sunset.png --size 1024x1024
```

## Output

- The script saves the image to the workspace and prints the path.
- `MEDIA:` line is printed for OpenClaw to auto-attach on supported chat providers.

## Notes

- Model: `z-image` (local, no API key needed)
- Runs on AIMA proxy at `http://127.0.0.1:6188/v1`
- Response format: base64 PNG
