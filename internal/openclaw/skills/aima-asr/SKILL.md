---
name: aima-asr
description: Transcribe audio files using AIMA's local qwen3-asr model (speech-to-text).
metadata: {"openclaw":{"emoji":"🎙️","requires":{"bins":["curl"]},"always":true}}
---

# AIMA Speech-to-Text (qwen3-asr)

Transcribe audio files using AIMA's local ASR model.

## Quick start

```bash
{baseDir}/scripts/transcribe.sh /path/to/audio.wav
```

## Useful flags

```bash
{baseDir}/scripts/transcribe.sh /path/to/audio.wav --out /tmp/transcript.txt
{baseDir}/scripts/transcribe.sh /path/to/audio.m4a --json
```

## Output

- Prints transcript to stdout by default
- With `--out`, saves to file and prints the path

## Notes

- Model: `qwen3-asr-1.7b` (local, no API key needed)
- Supported formats: wav, mp3, m4a, ogg, flac
- Runs on AIMA proxy at `http://127.0.0.1:6188/v1`
