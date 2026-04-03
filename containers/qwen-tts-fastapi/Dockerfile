ARG BASE_IMAGE=qwen3-tts-cuda-arm64:latest
FROM ${BASE_IMAGE}

WORKDIR /app
COPY main.py /app/main.py
COPY ref_zh.wav /tmp/ref_zh.wav
