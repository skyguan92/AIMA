function asString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function parseModelRef(raw) {
  const value = asString(raw);
  const slash = value.indexOf("/");
  if (slash <= 0 || slash >= value.length - 1) return null;
  return {
    provider: value.slice(0, slash).trim(),
    model: value.slice(slash + 1).trim(),
  };
}

function parseModelRefs(raw) {
  if (typeof raw === "string") {
    const ref = parseModelRef(raw);
    return ref ? [ref] : [];
  }
  if (!raw || typeof raw !== "object") return [];

  const refs = [];
  const primary = parseModelRef(raw.primary);
  if (primary) refs.push(primary);

  if (Array.isArray(raw.fallbacks)) {
    for (const fallback of raw.fallbacks) {
      const ref = parseModelRef(fallback);
      if (ref) refs.push(ref);
    }
  }
  return refs;
}

function unique(values) {
  return [...new Set(values.filter(Boolean))];
}

function resolveImageGenerationSpec(cfg) {
  const providerId = "aima-imagegen";
  const provider = resolveProviderConfig(cfg, providerId);
  if (!provider) return null;

  const refs = parseModelRefs(cfg?.agents?.defaults?.imageGenerationModel).filter(
    (ref) => ref.provider === providerId,
  );
  const models = unique(refs.map((ref) => ref.model));
  if (models.length === 0) return null;

  return {
    ...provider,
    id: providerId,
    defaultModel: models[0],
    models,
  };
}

function resolveProviderConfig(cfg, providerId) {
  const provider = cfg?.models?.providers?.[providerId];
  if (!provider || typeof provider !== "object") return null;
  const baseUrl = asString(provider.baseUrl);
  if (!baseUrl) return null;
  return {
    baseUrl: baseUrl.replace(/\/+$/, ""),
    apiKey: asString(provider.apiKey),
  };
}

function readSize(req) {
  const size = asString(req?.size);
  return size || "512x512";
}

function buildImageResults(payload) {
  const data = Array.isArray(payload?.data) ? payload.data : [];
  return data
    .map((entry, index) => {
      const b64 = asString(entry?.b64_json);
      if (!b64) return null;
      const image = {
        buffer: Buffer.from(b64, "base64"),
        mimeType: "image/png",
        fileName: `image-${index + 1}.png`,
      };
      const revisedPrompt = asString(entry?.revised_prompt);
      if (revisedPrompt) image.revisedPrompt = revisedPrompt;
      return image;
    })
    .filter((image) => image !== null);
}

function errorMessage(payload, status) {
  return (
    asString(payload?.error?.message) ||
    asString(payload?.error) ||
    `HTTP ${status}`
  );
}

export default function register(api) {
  api.registerImageGenerationProvider({
    id: "aima-imagegen",
    label: "AIMA Local Image",
    get defaultModel() {
      return resolveImageGenerationSpec(api.config)?.defaultModel || "";
    },
    get models() {
      return resolveImageGenerationSpec(api.config)?.models || [];
    },
    capabilities: {
      generate: {
        maxCount: 1,
        supportsSize: true,
        supportsAspectRatio: false,
        supportsResolution: false,
      },
      edit: {
        enabled: false,
        maxCount: 0,
        maxInputImages: 0,
        supportsSize: false,
        supportsAspectRatio: false,
        supportsResolution: false,
      },
      geometry: {
        sizes: ["512x512"],
      },
    },
    async generateImage(req) {
      if ((req.inputImages?.length ?? 0) > 0) {
        throw new Error("AIMA local image provider does not support reference-image edits");
      }

      const prompt = asString(req.prompt);
      if (!prompt) throw new Error("prompt required");

      const spec = resolveImageGenerationSpec(req.cfg ?? api.config);
      if (!spec) {
        throw new Error("AIMA local image provider is not configured");
      }

      const headers = { "Content-Type": "application/json" };
      if (spec.apiKey) {
        headers.Authorization = `Bearer ${spec.apiKey}`;
      }

      const response = await fetch(`${spec.baseUrl}/images/generations`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          model: req.model || spec.defaultModel,
          prompt,
          n: req.count ?? 1,
          size: readSize(req),
          response_format: "b64_json",
        }),
      });

      const payload = await response.json().catch(() => null);
      if (!response.ok) {
        throw new Error(`image generation failed: ${errorMessage(payload, response.status)}`);
      }

      const images = buildImageResults(payload);
      if (images.length === 0) {
        throw new Error("image generation response missing b64_json");
      }

      return {
        images,
        model: req.model || spec.defaultModel,
      };
    },
  });
}
