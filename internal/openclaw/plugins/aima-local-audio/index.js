import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const defaultWorkspaceDir = path.join(os.homedir(), ".openclaw", "workspace");
const defaultLocalAPIKey = "local";
const allowedAudioExtensions = new Set([
  ".aac",
  ".flac",
  ".m4a",
  ".mp3",
  ".mp4",
  ".oga",
  ".ogg",
  ".opus",
  ".wav",
  ".webm",
]);

function asString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function normalizeHomePath(raw) {
  const value = asString(raw);
  if (!value) return "";
  if (value === "~") return os.homedir();
  if (value.startsWith("~/") || value.startsWith("~\\")) {
    return path.join(os.homedir(), value.slice(2));
  }
  if (value.startsWith("~")) return path.join(os.homedir(), value.slice(1));
  return value;
}

function toTextResult(text, details) {
  return {
    content: [{ type: "text", text }],
    details,
  };
}

function normalizeBaseUrl(raw) {
  const value = asString(raw).replace(/\/+$/, "");
  return value;
}

function normalizeAudioConfigEntry(entry) {
  if (!entry || typeof entry !== "object") return null;
  const model = asString(entry.model);
  if (!model) return null;
  return {
    model,
    baseUrl: normalizeBaseUrl(entry.baseUrl),
    apiKey: asString(entry.apiKey),
  };
}

function resolveConfiguredAudioRoutes(cfg) {
  const providers = cfg?.models?.providers && typeof cfg.models.providers === "object"
    ? cfg.models.providers
    : {};
  const aimaMediaProvider = providers["aima-media"];
  const aimaProvider = providers.aima;
  const fallbackBaseUrl = normalizeBaseUrl(
    aimaMediaProvider?.baseUrl || aimaProvider?.baseUrl,
  );
  const fallbackAPIKey =
    asString(aimaMediaProvider?.apiKey) ||
    asString(aimaProvider?.apiKey) ||
    defaultLocalAPIKey;

  const explicit = Array.isArray(cfg?.tools?.media?.audio?.models)
    ? cfg.tools.media.audio.models
        .map((entry) => normalizeAudioConfigEntry(entry))
        .filter(Boolean)
        .map((entry) => ({
          model: entry.model,
          baseUrl: entry.baseUrl || fallbackBaseUrl,
          apiKey: entry.apiKey || fallbackAPIKey,
        }))
        .filter((entry) => entry.baseUrl)
    : [];
  if (explicit.length > 0) return explicit;

  const providerModels = Array.isArray(aimaMediaProvider?.models) ? aimaMediaProvider.models : [];
  const fallback = providerModels
    .filter((entry) => {
      if (!entry || typeof entry !== "object") return false;
      const id = asString(entry.id);
      if (!id) return false;
      const inputs = Array.isArray(entry.input) ? entry.input.map(asString).filter(Boolean) : [];
      return inputs.length === 1 && inputs[0] === "text";
    })
    .map((entry) => ({
      model: asString(entry.id),
      baseUrl: fallbackBaseUrl,
      apiKey: fallbackAPIKey,
    }))
    .filter((entry) => entry.baseUrl);
  return fallback;
}

function detectAudioMime(filePath) {
  switch (path.extname(filePath).toLowerCase()) {
    case ".aac":
      return "audio/aac";
    case ".flac":
      return "audio/flac";
    case ".m4a":
      return "audio/mp4";
    case ".mp3":
      return "audio/mpeg";
    case ".mp4":
      return "audio/mp4";
    case ".oga":
    case ".ogg":
      return "audio/ogg";
    case ".opus":
      return "audio/opus";
    case ".wav":
      return "audio/wav";
    case ".webm":
      return "audio/webm";
    default:
      return "application/octet-stream";
  }
}

function formatHTTPError(status, statusText, bodyText) {
  const prefix = statusText ? `${status} ${statusText}` : `${status}`;
  const trimmed = bodyText.trim();
  if (!trimmed) return prefix;
  return `${prefix}: ${trimmed.slice(0, 500)}`;
}

async function transcribeWithAIMA(cfg, filePath) {
  const routes = resolveConfiguredAudioRoutes(cfg);
  if (routes.length === 0) {
    throw new Error("no configured AIMA/OpenClaw audio transcription route");
  }

  const fileName = path.basename(filePath);
  const mime = detectAudioMime(filePath);
  const bytes = fs.readFileSync(filePath);
  const blob = new Blob([bytes], { type: mime });
  let lastError = "";

  for (const route of routes) {
    const url = `${route.baseUrl}/audio/transcriptions`;
    const form = new FormData();
    form.append("file", blob, fileName);
    form.append("model", route.model);

    const headers = new Headers();
    headers.set("authorization", `Bearer ${route.apiKey || defaultLocalAPIKey}`);

    const response = await fetch(url, {
      method: "POST",
      headers,
      body: form,
    });
    const bodyText = await response.text();
    if (!response.ok) {
      lastError = formatHTTPError(response.status, response.statusText, bodyText);
      continue;
    }

    let payload;
    try {
      payload = JSON.parse(bodyText);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      lastError = `invalid JSON transcription response: ${message}`;
      continue;
    }

    const transcript = asString(payload?.text);
    if (!transcript) {
      lastError = `missing text in transcription response from ${url}`;
      continue;
    }

    return {
      transcript,
      model: route.model,
      baseUrl: route.baseUrl,
    };
  }

  throw new Error(lastError || "audio transcription returned no transcript");
}

function workspaceRoots(ctx) {
  const roots = [];
  const workspaceDir = asString(ctx?.workspaceDir);
  if (workspaceDir) roots.push(path.resolve(normalizeHomePath(workspaceDir)));
  roots.push(path.resolve(defaultWorkspaceDir));
  return [...new Set(roots)];
}

function resolveCandidatePath(rawPath, ctx) {
  const value = normalizeHomePath(rawPath);
  if (!value) return "";
  if (path.isAbsolute(value)) return path.resolve(value);

  for (const root of workspaceRoots(ctx)) {
    const candidate = path.resolve(root, value);
    if (candidate.startsWith(root + path.sep) || candidate === root) {
      return candidate;
    }
  }
  return "";
}

function isPathWithinRoots(filePath, roots) {
  for (const root of roots) {
    if (filePath === root || filePath.startsWith(root + path.sep)) return true;
  }
  return false;
}

function readAudioPath(params) {
  const record = params && typeof params === "object" ? params : {};
  for (const key of ["path", "filePath", "file_path", "audio", "audioPath", "audio_path"]) {
    const value = asString(record[key]);
    if (value) return value;
  }
  return "";
}

function validateAudioPath(resolvedPath, roots) {
  if (!resolvedPath) return "path required";
  if (!isPathWithinRoots(resolvedPath, roots)) {
    return `path must stay within the OpenClaw workspace: ${roots[0]}`;
  }
  if (!fs.existsSync(resolvedPath)) return `audio file not found: ${resolvedPath}`;
  if (!fs.statSync(resolvedPath).isFile()) return `audio path is not a file: ${resolvedPath}`;
  const ext = path.extname(resolvedPath).toLowerCase();
  if (ext && !allowedAudioExtensions.has(ext)) {
    return `unsupported audio extension: ${ext}`;
  }
  return "";
}

export default function register(api) {
  api.registerTool((ctx) => ({
    name: "audio_transcribe",
    label: "Audio Transcribe",
    description:
      "Transcribe a local audio file from the OpenClaw workspace using the configured AIMA/OpenClaw audio provider. Use this when the user asks to transcribe or summarize a WAV/MP3/M4A/OGG/OPUS audio file path.",
    parameters: {
      type: "object",
      additionalProperties: false,
      properties: {
        path: {
          type: "string",
          description:
            "Workspace-relative or absolute path to the audio file under the OpenClaw workspace, for example audio-tests/sample.wav.",
        },
      },
      required: ["path"],
    },
    async execute(_toolCallId, params) {
      const rawPath = readAudioPath(params);
      const roots = workspaceRoots(ctx);
      const resolvedPath = resolveCandidatePath(rawPath, ctx);
      const validationError = validateAudioPath(resolvedPath, roots);
      if (validationError) {
        return toTextResult(validationError, {
          status: "failed",
          error: validationError,
          requestedPath: rawPath,
        });
      }

      try {
        const result = await transcribeWithAIMA(api.config, resolvedPath);
        const transcript = asString(result?.transcript);
        return toTextResult(`Transcript for ${resolvedPath}:\n${transcript}`, {
          status: "ok",
          path: resolvedPath,
          transcript,
          model: result.model,
          baseUrl: result.baseUrl,
        });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        return toTextResult(`Audio transcription failed for ${resolvedPath}: ${message}`, {
          status: "failed",
          path: resolvedPath,
          error: message,
        });
      }
    },
  }), { name: "audio_transcribe" });
}
