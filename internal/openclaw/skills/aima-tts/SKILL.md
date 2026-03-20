---
name: aima-tts
description: Text-to-speech using AIMA's local qwen3-tts model. Generate audio files from text.
metadata: {"openclaw":{"emoji":"🔊","requires":{"bins":["curl"]},"always":true}}
---

# AIMA Text-to-Speech (qwen3-tts)

Generate speech audio from text using AIMA's local TTS model.

## Quick start

```bash
{baseDir}/scripts/speak.sh "你好世界" --filename hello.wav
```

## Useful flags

```bash
{baseDir}/scripts/speak.sh "今天天气真好" --filename weather.wav
{baseDir}/scripts/speak.sh "Hello AIMA" --filename greeting.wav --voice default
```

## Output

- WAV audio file saved to workspace
- `MEDIA:` line printed for OpenClaw auto-attachment

## Notes

- Model: `qwen3-tts-0.6b` (local, no API key needed)
- Voice: `default` (single voice)
- Output format: WAV
- Runs on AIMA proxy at `http://127.0.0.1:6188/v1`
