#!/usr/bin/env python3
"""
Qwen3-TTS inference service.

This variant exposes the full practical TTS surface AIMA/OpenClaw needs:
- OpenAI-compatible /v1/audio/speech
- JSON /v1/tts with base64 audio payload
- request-level reference-audio voice cloning
- x-vector-only cloning fallback
- model generation knobs passed through when supported
- optional speed post-processing through ffmpeg

It stays compatible with the existing container runtime contract:
- load Qwen3-TTS from /model or /models
- configurable device/device_map/dtype
- fixed default reference audio still works when callers do not override it
"""

import base64
import os
import subprocess
import tempfile
from contextlib import asynccontextmanager
from typing import Any, Optional

import numpy as np
import torch
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from pydantic import BaseModel, Field
from scipy.io import wavfile

from qwen_tts import Qwen3TTSModel

# Runtime configuration
MODEL_PATH = os.getenv("MODEL_PATH", "/model")
DEVICE = os.getenv("DEVICE", "cpu")
DEVICE_MAP = os.getenv("DEVICE_MAP", "")
DTYPE = os.getenv("DTYPE", "")
ATTN_IMPLEMENTATION = os.getenv("ATTN_IMPLEMENTATION", "")
CUDA_MEMORY_FRACTION = os.getenv("CUDA_MEMORY_FRACTION", "")
CACHE_DIR = os.getenv("CACHE_DIR", "/cache")
REFERENCE_AUDIO = os.getenv("REFERENCE_AUDIO", "/tmp/ref_zh.wav")
REFERENCE_TEXT = os.getenv(
    "REFERENCE_TEXT",
    "其实我真的有发现，我是一个特别善于观察别人情绪的人。比如说你现在开心不开心，我一眼就能看出来。",
)

_FORMAT_CONFIG = {
    "wav": {"ext": ".wav", "args": [], "mime": "audio/wav"},
    "mp3": {"ext": ".mp3", "args": ["-c:a", "libmp3lame", "-b:a", "128k"], "mime": "audio/mpeg"},
    "opus": {"ext": ".ogg", "args": ["-c:a", "libopus", "-b:a", "64k"], "mime": "audio/ogg"},
    "flac": {"ext": ".flac", "args": ["-c:a", "flac"], "mime": "audio/flac"},
    "aac": {"ext": ".aac", "args": ["-c:a", "aac", "-b:a", "128k"], "mime": "audio/aac"},
    "pcm": {
        "ext": ".pcm",
        "args": ["-f", "s16le", "-acodec", "pcm_s16le", "-ar", "24000"],
        "mime": "audio/pcm",
    },
}
SUPPORTED_FORMATS = list(_FORMAT_CONFIG.keys())
SUPPORTED_TTS_MODES = ["auto", "voice_clone", "custom_voice", "voice_design"]

tts_model: Optional[Qwen3TTSModel] = None
model_loaded = False


def parse_torch_dtype(value: str) -> Any:
    value = (value or "").strip().lower()
    if value == "":
        return None
    if value == "auto":
        return "auto"
    mapping = {
        "float16": torch.float16,
        "fp16": torch.float16,
        "half": torch.float16,
        "bfloat16": torch.bfloat16,
        "bf16": torch.bfloat16,
        "float32": torch.float32,
        "fp32": torch.float32,
    }
    if value not in mapping:
        raise ValueError(f"unsupported dtype {value!r}")
    return mapping[value]


def effective_device_map(device: str, device_map: str) -> Optional[str]:
    device_map = (device_map or "").strip()
    if device_map:
        return device_map
    device = (device or "").strip().lower()
    if device == "" or device == "cpu":
        return None
    if device == "cuda":
        return "cuda:0"
    if device.startswith("cuda:"):
        return device
    return device


def device_index_from(value: Optional[str]) -> int:
    value = (value or "").strip().lower()
    if value.startswith("cuda:"):
        try:
            return int(value.split(":", 1)[1])
        except ValueError:
            return 0
    return 0


def apply_cuda_memory_fraction(device: str, device_map: Optional[str], fraction: Optional[float]) -> None:
    if fraction is None:
        return
    if fraction <= 0 or fraction > 1:
        raise ValueError(f"cuda_memory_fraction must be in (0, 1], got {fraction}")
    if not (device or "").startswith("cuda"):
        print(f"Skipping CUDA memory fraction because device={device!r}")
        return
    if not torch.cuda.is_available():
        print("Skipping CUDA memory fraction because torch.cuda.is_available() is false")
        return
    device_index = device_index_from(device_map or device)
    torch.cuda.set_per_process_memory_fraction(fraction, device_index)
    print(f"Set CUDA memory fraction to {fraction:.4f} on cuda:{device_index}")


def build_model_load_kwargs() -> dict[str, Any]:
    kwargs: dict[str, Any] = {}
    resolved_device_map = effective_device_map(DEVICE, DEVICE_MAP)
    if resolved_device_map:
        kwargs["device_map"] = resolved_device_map
    resolved_dtype = parse_torch_dtype(DTYPE)
    if resolved_dtype is not None:
        kwargs["dtype"] = resolved_dtype
    if (ATTN_IMPLEMENTATION or "").strip():
        kwargs["attn_implementation"] = ATTN_IMPLEMENTATION.strip()
    return kwargs


def audio_to_int16(audio_array: np.ndarray) -> np.ndarray:
    if audio_array.dtype.kind == "f":
        return np.clip(audio_array * 32767, -32768, 32767).astype(np.int16)
    return audio_array.astype(np.int16)


def normalize_optional_string(value: Optional[str]) -> Optional[str]:
    if not isinstance(value, str):
        return None
    trimmed = value.strip()
    return trimmed or None


def normalize_response_format(value: Optional[str]) -> str:
    output_format = (value or "wav").strip().lower()
    if output_format not in _FORMAT_CONFIG:
        raise ValueError(f"unsupported response_format {value!r}, expected one of {SUPPORTED_FORMATS}")
    return output_format


def normalize_mode(value: Optional[str]) -> str:
    mode = (value or "auto").strip().lower()
    if mode not in SUPPORTED_TTS_MODES:
        raise ValueError(f"unsupported mode {value!r}, expected one of {SUPPORTED_TTS_MODES}")
    return mode


def default_reference_pair() -> tuple[Optional[str], Optional[str]]:
    ref_audio = REFERENCE_AUDIO if os.path.exists(REFERENCE_AUDIO) else None
    ref_text = REFERENCE_TEXT if ref_audio else None
    return ref_audio, ref_text


def current_model_type() -> Optional[str]:
    if tts_model is None or not model_loaded:
        return None
    return getattr(tts_model.model, "tts_model_type", None)


def current_supported_languages() -> Optional[list[str]]:
    if tts_model is None or not model_loaded:
        return None
    try:
        return tts_model.get_supported_languages()
    except Exception:
        return None


def current_supported_speakers() -> Optional[list[str]]:
    if tts_model is None or not model_loaded:
        return None
    try:
        return tts_model.get_supported_speakers()
    except Exception:
        return None


def build_generate_kwargs(request: "TTSBaseRequest") -> dict[str, Any]:
    kwargs: dict[str, Any] = {}
    for field in [
        "do_sample",
        "top_k",
        "top_p",
        "temperature",
        "repetition_penalty",
        "subtalker_dosample",
        "subtalker_top_k",
        "subtalker_top_p",
        "subtalker_temperature",
        "max_new_tokens",
    ]:
        value = getattr(request, field, None)
        if value is not None:
            kwargs[field] = value
    return kwargs


def resolve_reference_inputs(request: "TTSBaseRequest") -> tuple[Optional[str], Optional[str], bool, str]:
    ref_audio = normalize_optional_string(request.reference_audio)
    ref_text = normalize_optional_string(request.reference_text)
    x_vector_only_mode = bool(request.x_vector_only_mode)
    reference_source = "request" if ref_audio else ""

    if ref_audio:
        if not ref_text:
            x_vector_only_mode = True
        if x_vector_only_mode:
            ref_text = None
        return ref_audio, ref_text, x_vector_only_mode, reference_source

    if bool(request.use_default_reference):
        default_audio, default_text = default_reference_pair()
        if default_audio:
            return default_audio, None if x_vector_only_mode else default_text, x_vector_only_mode, "default"

    return None, None, x_vector_only_mode, reference_source


def resolve_effective_mode(request: "TTSBaseRequest", model_type: Optional[str]) -> str:
    mode = normalize_mode(request.mode)
    if mode != "auto":
        return mode
    if model_type == "custom_voice":
        return "custom_voice"
    if model_type == "voice_design":
        return "voice_design"
    return "voice_clone"


def resolve_speaker(request: "TTSBaseRequest") -> Optional[str]:
    speaker = normalize_optional_string(request.speaker)
    if speaker:
        return speaker
    voice = normalize_optional_string(request.voice)
    if voice and voice.lower() not in {"default", "alloy"}:
        return voice
    return None


def maybe_build_atempo_filters(speed: float) -> list[str]:
    speed = float(speed)
    if abs(speed - 1.0) < 1e-6:
        return []
    if speed <= 0:
        raise ValueError(f"speed must be > 0, got {speed}")

    filters: list[str] = []
    remaining = speed
    while remaining < 0.5:
        filters.append("atempo=0.5")
        remaining /= 0.5
    while remaining > 2.0:
        filters.append("atempo=2.0")
        remaining /= 2.0
    filters.append(f"atempo={remaining:.6f}")
    return filters


def write_temp_wav(audio_int16: np.ndarray, sample_rate: int) -> str:
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        wav_path = tmp.name
    wavfile.write(wav_path, sample_rate, audio_int16)
    return wav_path


def convert_audio(wav_path: str, output_format: str, speed: float) -> tuple[str, str]:
    cfg = _FORMAT_CONFIG[output_format]
    filters = maybe_build_atempo_filters(speed)
    if output_format == "wav" and len(filters) == 0:
        return wav_path, cfg["mime"]

    out_path = wav_path.rsplit(".", 1)[0] + cfg["ext"]
    cmd = ["ffmpeg", "-y", "-loglevel", "error", "-i", wav_path, "-ar", "24000", "-ac", "1"]
    if filters:
        cmd.extend(["-filter:a", ",".join(filters)])
    cmd.extend(cfg["args"])
    cmd.append(out_path)
    result = subprocess.run(cmd, capture_output=True, timeout=60)
    if result.returncode != 0:
        raise RuntimeError(f"ffmpeg conversion failed: {result.stderr.decode()[:500]}")
    try:
        os.unlink(wav_path)
    except OSError:
        pass
    return out_path, cfg["mime"]

def estimate_duration_seconds(audio_int16: np.ndarray, sample_rate: int, speed: float) -> float:
    duration = len(audio_int16) / max(sample_rate, 1)
    return round(duration / max(speed, 1e-6), 2)


class TTSBaseRequest(BaseModel):
    voice: str = Field("default", description="Voice alias or speaker hint")
    speaker: Optional[str] = Field(None, description="Explicit speaker id for custom-voice models")
    language: Optional[str] = Field(None, description="Optional language hint")
    instruct: Optional[str] = Field(None, description="Optional natural-language voice/style instruction")
    mode: str = Field("auto", description="auto | voice_clone | custom_voice | voice_design")
    response_format: str = Field("wav", description="Output audio format")
    speed: float = Field(1.0, description="Playback speed multiplier", ge=0.25, le=4.0)
    reference_audio: Optional[str] = Field(None, description="Reference audio as URL, base64 string, or data URL")
    reference_text: Optional[str] = Field(None, description="Transcript for the reference audio")
    x_vector_only_mode: bool = Field(False, description="Use speaker embedding only for voice cloning")
    use_default_reference: bool = Field(True, description="Allow fallback to the configured default reference audio")
    non_streaming_mode: bool = Field(True, description="Use non-streaming generation path where supported")
    do_sample: Optional[bool] = Field(None, description="Sampling toggle")
    top_k: Optional[int] = Field(None, description="Top-k sampling")
    top_p: Optional[float] = Field(None, description="Top-p sampling")
    temperature: Optional[float] = Field(None, description="Sampling temperature")
    repetition_penalty: Optional[float] = Field(None, description="Repetition penalty")
    subtalker_dosample: Optional[bool] = Field(None, description="Subtalker sampling toggle")
    subtalker_top_k: Optional[int] = Field(None, description="Subtalker top-k")
    subtalker_top_p: Optional[float] = Field(None, description="Subtalker top-p")
    subtalker_temperature: Optional[float] = Field(None, description="Subtalker temperature")
    max_new_tokens: Optional[int] = Field(None, description="Maximum generated codec tokens")


class TTSRequest(TTSBaseRequest):
    model: Optional[str] = Field(None, description="Optional model id placeholder")
    text: str = Field(..., description="Text to synthesize", min_length=1, max_length=5000)


class OpenAITTSRequest(TTSBaseRequest):
    model: str = Field("qwen3-tts", description="Model to use")
    input: str = Field(..., description="Text to synthesize", min_length=1, max_length=5000)


@asynccontextmanager
async def lifespan(app: FastAPI):
    global tts_model, model_loaded

    print("Loading TTS service...")
    print(f"Model path: {MODEL_PATH}")
    print(f"Using device: {DEVICE}")
    print(f"Using device_map: {effective_device_map(DEVICE, DEVICE_MAP) or 'default'}")
    print(f"Using dtype: {DTYPE or 'default'}")
    print(f"Using attn_implementation: {ATTN_IMPLEMENTATION or 'default'}")
    print(f"Using cuda_memory_fraction: {CUDA_MEMORY_FRACTION or 'default'}")
    print(f"Default reference audio: {REFERENCE_AUDIO}")
    print(f"Supported output formats: {SUPPORTED_FORMATS}")

    model_files_exist = os.path.exists(MODEL_PATH) and any(
        f.endswith((".bin", ".safetensors", ".pt", ".pth"))
        for f in os.listdir(MODEL_PATH)
    ) if os.path.exists(MODEL_PATH) else False

    if model_files_exist:
        try:
            cuda_memory_fraction = float(CUDA_MEMORY_FRACTION) if CUDA_MEMORY_FRACTION else None
            model_load_kwargs = build_model_load_kwargs()
            apply_cuda_memory_fraction(DEVICE, model_load_kwargs.get("device_map"), cuda_memory_fraction)
            print(f"Model load kwargs: {model_load_kwargs}")
            tts_model = Qwen3TTSModel.from_pretrained(MODEL_PATH, **model_load_kwargs)
            model_loaded = True
            print("✓ Model loaded successfully")
            print(f"✓ TTS model type: {current_model_type()}")
            print(f"✓ Supported languages: {current_supported_languages()}")
        except Exception as e:
            print(f"✗ Error loading model: {e}")
            import traceback

            traceback.print_exc()
            model_loaded = False
            tts_model = None
    else:
        print(f"✗ Model files not found at {MODEL_PATH}")
        model_loaded = False
        tts_model = None

    yield

    print("Shutting down TTS service...")
    model_loaded = False
    tts_model = None


app = FastAPI(
    title="Qwen3-TTS Service",
    description="AIMA-patched TTS inference service for OpenClaw and OpenAI-compatible routing",
    version="1.2.0",
    lifespan=lifespan,
)


def synthesize_with_model(request: TTSRequest) -> tuple[np.ndarray, int, dict[str, Any]]:
    model_type = current_model_type()
    if tts_model is None or not model_loaded or model_type is None:
        raise RuntimeError("Model not loaded")

    mode = resolve_effective_mode(request, model_type)
    generate_kwargs = build_generate_kwargs(request)
    speed = float(request.speed)

    if mode == "voice_clone":
        if model_type != "base":
            raise ValueError(f"voice_clone mode is unsupported for tts_model_type={model_type}")
        ref_audio, ref_text, x_vector_only_mode, reference_source = resolve_reference_inputs(request)
        if ref_audio is None:
            raise ValueError("voice_clone mode requires reference_audio or a configured default reference")
        audios, sample_rate = tts_model.generate_voice_clone(
            text=request.text,
            language=request.language or "auto",
            ref_audio=ref_audio,
            ref_text=ref_text,
            x_vector_only_mode=x_vector_only_mode,
            non_streaming_mode=bool(request.non_streaming_mode),
            **generate_kwargs,
        )
        return audio_to_int16(audios[0]), sample_rate, {
            "mode": mode,
            "tts_model_type": model_type,
            "reference_source": reference_source,
            "x_vector_only_mode": x_vector_only_mode,
        }

    if mode == "custom_voice":
        if model_type != "custom_voice":
            raise ValueError(f"custom_voice mode is unsupported for tts_model_type={model_type}")
        speaker = resolve_speaker(request)
        if not speaker:
            raise ValueError("custom_voice mode requires speaker or a non-default voice alias")
        audios, sample_rate = tts_model.generate_custom_voice(
            text=request.text,
            speaker=speaker,
            language=request.language,
            instruct=request.instruct,
            non_streaming_mode=bool(request.non_streaming_mode),
            **generate_kwargs,
        )
        return audio_to_int16(audios[0]), sample_rate, {
            "mode": mode,
            "tts_model_type": model_type,
            "speaker": speaker,
        }

    if mode == "voice_design":
        if model_type != "voice_design":
            raise ValueError(f"voice_design mode is unsupported for tts_model_type={model_type}")
        instruct = normalize_optional_string(request.instruct)
        if not instruct:
            raise ValueError("voice_design mode requires instruct")
        audios, sample_rate = tts_model.generate_voice_design(
            text=request.text,
            instruct=instruct,
            language=request.language,
            non_streaming_mode=bool(request.non_streaming_mode),
            **generate_kwargs,
        )
        return audio_to_int16(audios[0]), sample_rate, {
            "mode": mode,
            "tts_model_type": model_type,
            "instruct": instruct,
        }

    raise ValueError(f"unsupported synthesis mode {mode!r}")


def render_audio_bytes(request: TTSRequest) -> tuple[bytes, dict[str, Any]]:
    output_format = normalize_response_format(request.response_format)

    if tts_model is None or not model_loaded:
        raise RuntimeError("Model not loaded")

    audio_int16, sample_rate, metadata = synthesize_with_model(request)
    wav_path = write_temp_wav(audio_int16, sample_rate)
    out_path, mime_type = convert_audio(wav_path, output_format, request.speed)
    try:
        with open(out_path, "rb") as fh:
            audio_bytes = fh.read()
    finally:
        try:
            os.unlink(out_path)
        except OSError:
            pass

    response_meta = {
        "sample_rate": sample_rate,
        "duration_seconds": estimate_duration_seconds(audio_int16, sample_rate, request.speed),
        "format": output_format,
        "mime_type": mime_type,
        "real_model": True,
        "speed": request.speed,
    }
    response_meta.update(metadata)
    return audio_bytes, response_meta


def build_tts_request_from_openai(request: OpenAITTSRequest) -> TTSRequest:
    return TTSRequest(
        model=request.model,
        text=request.input,
        voice=request.voice,
        speaker=request.speaker,
        language=request.language,
        instruct=request.instruct,
        mode=request.mode,
        response_format=request.response_format,
        speed=request.speed,
        reference_audio=request.reference_audio,
        reference_text=request.reference_text,
        x_vector_only_mode=request.x_vector_only_mode,
        use_default_reference=request.use_default_reference,
        non_streaming_mode=request.non_streaming_mode,
        do_sample=request.do_sample,
        top_k=request.top_k,
        top_p=request.top_p,
        temperature=request.temperature,
        repetition_penalty=request.repetition_penalty,
        subtalker_dosample=request.subtalker_dosample,
        subtalker_top_k=request.subtalker_top_k,
        subtalker_top_p=request.subtalker_top_p,
        subtalker_temperature=request.subtalker_temperature,
        max_new_tokens=request.max_new_tokens,
    )


@app.get("/health")
async def health_check():
    return {
        "status": "healthy",
        "model": "Qwen3-TTS",
        "device": DEVICE,
        "device_map": effective_device_map(DEVICE, DEVICE_MAP),
        "dtype": DTYPE or None,
        "attn_implementation": ATTN_IMPLEMENTATION or None,
        "cuda_memory_fraction": float(CUDA_MEMORY_FRACTION) if CUDA_MEMORY_FRACTION else None,
        "loaded": model_loaded,
        "real_model": tts_model is not None,
        "fallback_mode": tts_model is None,
        "supported_formats": SUPPORTED_FORMATS,
        "supported_modes": SUPPORTED_TTS_MODES,
        "tts_model_type": current_model_type(),
        "supported_languages": current_supported_languages(),
        "supported_speakers": current_supported_speakers(),
        "default_reference_audio": REFERENCE_AUDIO if os.path.exists(REFERENCE_AUDIO) else None,
    }


@app.get("/")
async def root():
    speakers = current_supported_speakers()
    return {
        "service": "Qwen3-TTS Service",
        "version": "1.2.0",
        "model_loaded": model_loaded,
        "device": DEVICE,
        "device_map": effective_device_map(DEVICE, DEVICE_MAP),
        "dtype": DTYPE or None,
        "tts_model_type": current_model_type(),
        "supported_formats": SUPPORTED_FORMATS,
        "supported_modes": SUPPORTED_TTS_MODES,
        "supported_languages": current_supported_languages(),
        "speaker_count": len(speakers or []),
        "endpoints": {
            "health": "/health",
            "tts_json": "/v1/tts (POST)",
            "openai_compat": "/v1/audio/speech (POST)",
        },
    }


@app.post("/v1/tts")
async def text_to_speech(request: TTSRequest):
    try:
        audio_bytes, meta = render_audio_bytes(request)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except RuntimeError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:
        import traceback

        traceback.print_exc()
        raise HTTPException(status_code=500, detail=f"TTS error: {str(e)}")

    return {
        "text": request.text,
        "audio_base64": base64.b64encode(audio_bytes).decode("utf-8"),
        "sample_rate": meta["sample_rate"],
        "format": meta["format"],
        "duration_seconds": meta["duration_seconds"],
        "real_model": meta["real_model"],
        "mode": meta.get("mode"),
        "tts_model_type": meta.get("tts_model_type"),
        "reference_source": meta.get("reference_source"),
        "x_vector_only_mode": meta.get("x_vector_only_mode"),
        "speaker": meta.get("speaker"),
        "instruct": meta.get("instruct"),
        "speed": meta.get("speed", request.speed),
    }


@app.post("/v1/audio/speech")
async def openai_compatible_tts(request: OpenAITTSRequest):
    try:
        tts_request = build_tts_request_from_openai(request)
        audio_bytes, meta = render_audio_bytes(tts_request)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except RuntimeError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:
        import traceback

        traceback.print_exc()
        raise HTTPException(status_code=500, detail=f"TTS error: {str(e)}")

    suffix = _FORMAT_CONFIG[meta["format"]]["ext"]
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as tmp:
        tmp.write(audio_bytes)
        tmp_path = tmp.name
    return FileResponse(
        tmp_path,
        media_type=meta["mime_type"],
        filename=f"speech{suffix}",
    )


if __name__ == "__main__":
    import uvicorn
    import argparse

    parser = argparse.ArgumentParser(description="Qwen3-TTS Service")
    parser.add_argument("--model", type=str, default=MODEL_PATH, help="Model path")
    parser.add_argument("--port", type=int, default=8002, help="Server port")
    parser.add_argument("--device", type=str, default=DEVICE, help="Device (cpu/cuda)")
    parser.add_argument("--device-map", type=str, default=DEVICE_MAP, help="Transformers device_map, e.g. cuda:0 or auto")
    parser.add_argument("--dtype", type=str, default=DTYPE, help="Model dtype: auto/bfloat16/float16/float32")
    parser.add_argument("--attn-implementation", type=str, default=ATTN_IMPLEMENTATION, help="Attention implementation override")
    parser.add_argument("--cuda-memory-fraction", type=float, default=float(CUDA_MEMORY_FRACTION) if CUDA_MEMORY_FRACTION else None, help="torch.cuda per-process memory fraction in (0,1]")
    args = parser.parse_args()

    MODEL_PATH = args.model
    DEVICE = args.device
    DEVICE_MAP = args.device_map
    DTYPE = args.dtype
    ATTN_IMPLEMENTATION = args.attn_implementation
    CUDA_MEMORY_FRACTION = "" if args.cuda_memory_fraction is None else str(args.cuda_memory_fraction)

    print(f"Starting Qwen3-TTS server on port {args.port}")
    uvicorn.run(app, host="0.0.0.0", port=args.port, log_level="info")
