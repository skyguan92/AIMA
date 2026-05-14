package inferencehttp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const maxTTSRequestBody = 16 << 20

const (
	adapterLiteTTSHTTP = "litetts_http"
	adapterMooERGRPC   = "mooer_grpc"
	adapterVoxCPMClone = "voxcpm_clone"
)

// RegisterRoutes returns a function that registers AIMA inference proxy routes.
// Pattern follows internal/fleet/handler.go.
func RegisterRoutes(deps *Deps) func(*http.ServeMux) {
	return func(mux *http.ServeMux) {
		mux.HandleFunc("/v1/audio/speech", deps.handleTTS)
		mux.HandleFunc("/v1/tts", deps.handleTTS)
		mux.HandleFunc("/v1/audio/transcriptions", deps.handleASR)
		mux.HandleFunc("/v1/audio/quality", deps.handleAudioQuality)
		mux.HandleFunc("/v1/images/generations", deps.handleImageGen)
	}
}

// handleTTS proxies TTS requests to the backend serving the requested model.
// Expects JSON body including "model" and one of "input" or "text".
//
// The request body is forwarded with light normalization:
//   - /v1/audio/speech prefers "input"
//   - /v1/tts prefers "text"
//
// Additional fields such as response_format, speed, reference_audio, and
// reference_text are preserved for backends that support them.
func (d *Deps) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body to extract model name, then reset for proxying
	body, err := io.ReadAll(io.LimitReader(r.Body, maxTTSRequestBody)) // Allows base64 reference audio clips.
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	body, raw, err := normalizeTTSRequestBody(r.URL.Path, body)
	if err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	model, _ := raw["model"].(string)
	if model == "" {
		http.Error(w, `{"error":"missing or invalid model field"}`, http.StatusBadRequest)
		return
	}

	backend := d.findBackend(model)
	if backend == nil {
		http.Error(w, fmt.Sprintf(`{"error":"model %q not found"}`, model), http.StatusNotFound)
		return
	}

	adapter := d.adapterFor(model, r.URL.Path)
	if adapter == adapterLiteTTSHTTP {
		d.handleLiteTTS(w, r, backend, raw)
		return
	}
	if adapter == adapterVoxCPMClone && hasTTSReferenceAudio(raw) {
		d.handleVoxCPMClone(w, r, backend, raw, body)
		return
	}

	switch r.URL.Path {
	case "/v1/tts":
		d.forwardTTSJSON(w, r, backend, body)
	default:
		d.forwardTTSAudio(w, r, backend, body)
	}
}

func normalizeTTSRequestBody(path string, body []byte) ([]byte, map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, err
	}

	switch path {
	case "/v1/audio/speech":
		if input, _ := raw["input"].(string); strings.TrimSpace(input) == "" {
			if text, _ := raw["text"].(string); strings.TrimSpace(text) != "" {
				raw["input"] = text
			}
		}
	case "/v1/tts":
		if text, _ := raw["text"].(string); strings.TrimSpace(text) == "" {
			if input, _ := raw["input"].(string); strings.TrimSpace(input) != "" {
				raw["text"] = input
			}
		}
	}

	out, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

// handleASR proxies ASR (transcription) requests to the backend.
// Expects multipart/form-data with a "model" field.
func (d *Deps) handleASR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// For multipart, we need to parse just the model field.
	// Read the full body so we can forward it as-is.
	body, err := io.ReadAll(io.LimitReader(r.Body, 100<<20)) // 100 MB limit for audio
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	upload, err := parseASRUpload(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	model := ""
	if upload != nil {
		model = upload.Model
	}
	if model == "" {
		// Try JSON body as fallback
		var req struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &req)
		model = req.Model
	}

	if model == "" {
		http.Error(w, `{"error":"missing model field"}`, http.StatusBadRequest)
		return
	}

	backend := d.findBackend(model)
	if backend == nil {
		http.Error(w, fmt.Sprintf(`{"error":"model %q not found"}`, model), http.StatusNotFound)
		return
	}

	if d.adapterFor(model, r.URL.Path) == adapterMooERGRPC {
		d.handleMooERASR(w, r, backend, upload)
		return
	}

	d.forwardASR(w, r, backend.Address, body)
}

// handleAudioQuality routes audio quality scoring to the requested quality backend.
// JSON requests are forwarded to /score; multipart uploads are forwarded to /score/upload.
func (d *Deps) handleAudioQuality(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	model, targetPath, err := qualityRequestTarget(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if model == "" {
		http.Error(w, `{"error":"missing model field"}`, http.StatusBadRequest)
		return
	}

	backend := d.findBackend(model)
	if backend == nil {
		http.Error(w, fmt.Sprintf(`{"error":"model %q not found"}`, model), http.StatusNotFound)
		return
	}

	d.forwardRequest(w, r, backend.Address, targetPath, r.Header.Get("Content-Type"), body)
}

func qualityRequestTarget(r *http.Request, body []byte) (string, string, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && strings.TrimSpace(r.Header.Get("Content-Type")) != "" {
		return "", "", fmt.Errorf(`{"error":"invalid content type"}`)
	}

	switch mediaType {
	case "", "application/json":
		var req struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return "", "", fmt.Errorf(`{"error":"invalid JSON body"}`)
		}
		return strings.TrimSpace(req.Model), "/score", nil
	case "multipart/form-data":
		upload, err := parseASRUpload(r, body)
		if err != nil {
			return "", "", err
		}
		if upload == nil {
			return "", "", fmt.Errorf(`{"error":"invalid multipart form body"}`)
		}
		return strings.TrimSpace(upload.Model), "/score/upload", nil
	default:
		return "", "", fmt.Errorf(`{"error":"unsupported content type %q"}`, mediaType)
	}
}

// forwardASR forwards the ASR request and cleans the response text.
// vLLM Qwen3-ASR returns text like "language Chinese<asr_text>你好" —
// we strip the metadata prefix to return clean transcription text.
func (d *Deps) forwardASR(w http.ResponseWriter, r *http.Request, targetAddr string, body []byte) {
	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}
	target, err := url.Parse(targetAddr)
	if err != nil {
		slog.Error("aima proxy: invalid ASR backend address", "addr", targetAddr, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		target.String()+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("aima proxy: ASR backend request failed", "backend", targetAddr, "err", err)
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read backend response", http.StatusBadGateway)
		return
	}

	// Clean ASR metadata prefix from the text field.
	if resp.StatusCode == http.StatusOK {
		respBody = cleanASRResponse(respBody)
	}

	for k, vals := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue // recalculated below
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// cleanASRResponse strips vLLM Qwen-ASR metadata prefixes from the text field.
// Input:  {"text":"language Chinese<asr_text>你好世界。",...}
// Output: {"text":"你好世界。",...}
func cleanASRResponse(body []byte) []byte {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body
	}
	text, ok := resp["text"].(string)
	if !ok {
		return body
	}
	cleaned := stripASRPrefix(text)
	if cleaned == text {
		return body
	}
	resp["text"] = cleaned
	out, err := json.Marshal(resp)
	if err != nil {
		return body
	}
	return out
}

// stripASRPrefix removes "language <lang><asr_text>" prefix from ASR output.
func stripASRPrefix(text string) string {
	const marker = "<asr_text>"
	if idx := strings.Index(text, marker); idx >= 0 {
		return strings.TrimSpace(text[idx+len(marker):])
	}
	return text
}

func RequestBodyRewriter(cat CatalogReader) func(path, contentType, model, engineType string, body []byte) []byte {
	if cat == nil {
		return nil
	}
	return func(path, contentType, model, engineType string, body []byte) []byte {
		if !isJSONContentType(contentType) {
			return body
		}
		for _, patch := range cat.RequestPatches(model) {
			if !matchesRequestPatch(patch, path, engineType) {
				continue
			}
			body = mergeRequestPatchBody(body, patch.Body)
		}
		body = stripOrphanedToolChoice(body)
		return body
	}
}

func (d *Deps) adapterFor(model, path string) string {
	if d == nil || d.Catalog == nil {
		return ""
	}
	for _, adapter := range d.Catalog.Adapters(model) {
		if strings.TrimSpace(adapter.Path) == path {
			return strings.ToLower(strings.TrimSpace(adapter.Kind))
		}
	}
	return ""
}

// stripOrphanedToolChoice removes tool_choice from JSON request bodies when
// tools is empty or absent. Prevents vLLM 400 errors from OpenAI-compatible clients
// that send tool_choice:"auto" without defining any tools.
func stripOrphanedToolChoice(body []byte) []byte {
	// Fast path: skip full JSON parse if no tool_choice present.
	if !bytes.Contains(body, []byte(`"tool_choice"`)) {
		return body
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	if _, has := req["tool_choice"]; !has {
		return body
	}
	tools, _ := req["tools"].([]any)
	if len(tools) > 0 {
		return body
	}
	delete(req, "tool_choice")
	delete(req, "tools")
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func matchesRequestPatch(patch RequestPatch, path, engineType string) bool {
	if patch.Path != "" && patch.Path != path {
		return false
	}
	if len(patch.EnginePrefixes) == 0 {
		return true
	}
	engineType = strings.ToLower(strings.TrimSpace(engineType))
	for _, prefix := range patch.EnginePrefixes {
		if strings.HasPrefix(engineType, strings.ToLower(strings.TrimSpace(prefix))) {
			return true
		}
	}
	return false
}

func isJSONContentType(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

func mergeRequestPatchBody(body []byte, patch map[string]any) []byte {
	if len(patch) == 0 {
		return body
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	mergeJSONDefaults(req, patch)
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func mergeJSONDefaults(dst, defaults map[string]any) {
	for key, value := range defaults {
		defMap, defIsMap := value.(map[string]any)
		if existing, ok := dst[key]; ok {
			existingMap, existingIsMap := existing.(map[string]any)
			if defIsMap && existingIsMap {
				mergeJSONDefaults(existingMap, defMap)
			}
			continue
		}
		dst[key] = cloneJSONValue(value)
	}
}

func cloneJSONValue(value any) any {
	switch raw := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(raw))
		for key, item := range raw {
			out[key] = cloneJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(raw))
		for i, item := range raw {
			out[i] = cloneJSONValue(item)
		}
		return out
	default:
		return raw
	}
}

func (d *Deps) forwardTTSAudio(w http.ResponseWriter, r *http.Request, backend *Backend, body []byte) {
	resp, respBody, err := d.callBackend(r, backend.Address, "/v1/audio/speech", r.Header.Get("Content-Type"), body)
	if err != nil {
		slog.Warn("aima proxy: TTS backend request failed", "backend", backend.Address, "err", err)
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	if isMissingRoute(resp.StatusCode) {
		fallbackBody, _, normErr := normalizeTTSRequestBody("/v1/tts", body)
		if normErr == nil {
			resp, respBody, err = d.callBackend(r, backend.Address, "/v1/tts", r.Header.Get("Content-Type"), fallbackBody)
			if err != nil {
				slog.Warn("aima proxy: TTS fallback request failed", "backend", backend.Address, "err", err)
				http.Error(w, "backend unreachable", http.StatusBadGateway)
				return
			}
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && writeAudioFromJSON(w, respBody, body, resp.StatusCode) {
		return
	}
	writeBackendResponse(w, resp, respBody)
}

func (d *Deps) forwardTTSJSON(w http.ResponseWriter, r *http.Request, backend *Backend, body []byte) {
	resp, respBody, err := d.callBackend(r, backend.Address, "/v1/tts", r.Header.Get("Content-Type"), body)
	if err != nil {
		slog.Warn("aima proxy: TTS backend request failed", "backend", backend.Address, "err", err)
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	if isMissingRoute(resp.StatusCode) {
		fallbackBody, _, normErr := normalizeTTSRequestBody("/v1/audio/speech", body)
		if normErr == nil {
			resp, respBody, err = d.callBackend(r, backend.Address, "/v1/audio/speech", r.Header.Get("Content-Type"), fallbackBody)
			if err != nil {
				slog.Warn("aima proxy: TTS fallback request failed", "backend", backend.Address, "err", err)
				http.Error(w, "backend unreachable", http.StatusBadGateway)
				return
			}
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && isAudioContent(resp.Header.Get("Content-Type")) {
		writeAudioJSON(w, respBody, body, resp.Header.Get("Content-Type"), resp.StatusCode)
		return
	}
	writeBackendResponse(w, resp, respBody)
}

func (d *Deps) handleVoxCPMClone(w http.ResponseWriter, r *http.Request, backend *Backend, raw map[string]any, requestBody []byte) {
	body, contentType, err := buildVoxCPMCloneRequest(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, respBody, err := d.callBackend(r, backend.Address, "/v1/clone", contentType, body)
	if err != nil {
		slog.Warn("aima proxy: VoxCPM clone backend request failed", "backend", backend.Address, "err", err)
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}

	if r.URL.Path == "/v1/tts" && resp.StatusCode >= 200 && resp.StatusCode < 300 && isAudioContent(resp.Header.Get("Content-Type")) {
		writeAudioJSON(w, respBody, requestBody, resp.Header.Get("Content-Type"), resp.StatusCode)
		return
	}
	if r.URL.Path == "/v1/audio/speech" && resp.StatusCode >= 200 && resp.StatusCode < 300 && writeAudioFromJSON(w, respBody, requestBody, resp.StatusCode) {
		return
	}
	writeBackendResponse(w, resp, respBody)
}

func buildVoxCPMCloneRequest(raw map[string]any) ([]byte, string, error) {
	text := extractTTSText(raw)
	if text == "" {
		return nil, "", fmt.Errorf(`{"error":"missing or invalid input field"}`)
	}
	refAudio := firstTTSString(raw, "reference_audio", "ref_audio")
	if refAudio == "" {
		return nil, "", fmt.Errorf(`{"error":"missing or invalid reference_audio field"}`)
	}
	audio, filename, err := decodeReferenceAudio(refAudio)
	if err != nil {
		return nil, "", err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("text", text); err != nil {
		return nil, "", err
	}
	if refText := firstTTSString(raw, "reference_text", "ref_text"); refText != "" {
		if err := writer.WriteField("ref_text", refText); err != nil {
			return nil, "", err
		}
	}
	for _, key := range []string{"response_format", "temperature", "cfg", "max_length"} {
		if value, ok := raw[key]; ok {
			if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
				return nil, "", err
			}
		}
	}
	part, err := writer.CreateFormFile("ref_audio", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func hasTTSReferenceAudio(raw map[string]any) bool {
	return firstTTSString(raw, "reference_audio", "ref_audio") != ""
}

func firstTTSString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, _ := raw[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func decodeReferenceAudio(value string) ([]byte, string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		return decodeReferenceAudioDataURL(value)
	}
	audio, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf(`{"error":"reference_audio must be a data URL or base64 audio"}`)
	}
	return audio, "reference.wav", nil
}

func decodeReferenceAudioDataURL(value string) ([]byte, string, error) {
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return nil, "", fmt.Errorf(`{"error":"invalid reference_audio data URL"}`)
	}
	meta := value[len("data:"):comma]
	payload := value[comma+1:]
	if !strings.Contains(strings.ToLower(meta), ";base64") {
		return nil, "", fmt.Errorf(`{"error":"reference_audio data URL must be base64 encoded"}`)
	}
	audio, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf(`{"error":"invalid reference_audio base64 data"}`)
	}

	contentType := strings.TrimSpace(strings.Split(meta, ";")[0])
	format := audioFormatFromContentType(contentType)
	if format == "" {
		format = "wav"
	}
	return audio, "reference." + format, nil
}

func (d *Deps) callBackend(r *http.Request, targetAddr, targetPath, contentType string, body []byte) (*http.Response, []byte, error) {
	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}
	target, err := url.Parse(targetAddr)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		strings.TrimRight(target.String(), "/")+targetPath, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, readErr
	}
	return resp, respBody, nil
}

func isMissingRoute(statusCode int) bool {
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func isAudioContent(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(contentType))
	}
	return strings.HasPrefix(strings.ToLower(mediaType), "audio/")
}

func writeBackendResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	for k, vals := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func writeAudioJSON(w http.ResponseWriter, audio []byte, requestBody []byte, contentType string, statusCode int) {
	format := audioFormatFromContentType(contentType)
	if format == "" {
		format = ttsResponseFormat(requestBody)
	}
	if format == "" {
		format = "wav"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"audio_base64": base64.StdEncoding.EncodeToString(audio),
		"format":       format,
		"content_type": contentTypeForAudioFormat(format),
	})
}

func writeAudioFromJSON(w http.ResponseWriter, body []byte, requestBody []byte, statusCode int) bool {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return false
	}
	rawAudio, _ := resp["audio_base64"].(string)
	if strings.TrimSpace(rawAudio) == "" {
		return false
	}
	audio, err := base64.StdEncoding.DecodeString(rawAudio)
	if err != nil {
		return false
	}
	format, _ := resp["format"].(string)
	if strings.TrimSpace(format) == "" {
		format = ttsResponseFormat(requestBody)
	}
	w.Header().Set("Content-Type", contentTypeForAudioFormat(format))
	w.WriteHeader(statusCode)
	_, _ = w.Write(audio)
	return true
}

func ttsResponseFormat(body []byte) string {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	for _, key := range []string{"response_format", "format"} {
		if value, _ := req[key].(string); strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func audioFormatFromContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(contentType))
	}
	switch strings.ToLower(mediaType) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg":
		return "ogg"
	case "audio/opus":
		return "opus"
	case "audio/flac":
		return "flac"
	case "audio/aac":
		return "aac"
	}
	return ""
}

func contentTypeForAudioFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3":
		return "audio/mpeg"
	case "ogg":
		return "audio/ogg"
	case "opus":
		return "audio/opus"
	case "flac":
		return "audio/flac"
	case "aac":
		return "audio/aac"
	default:
		return "audio/wav"
	}
}

// handleImageGen proxies image generation requests to the backend serving the requested model.
// Expects JSON body: {"model":"<model-name>", "prompt":"...", ...}
func (d *Deps) handleImageGen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		http.Error(w, `{"error":"missing or invalid model field"}`, http.StatusBadRequest)
		return
	}

	backend := d.findBackend(req.Model)
	if backend == nil {
		http.Error(w, fmt.Sprintf(`{"error":"model %q not found"}`, req.Model), http.StatusNotFound)
		return
	}

	d.reverseProxy(w, r, backend.Address, body)
}

// findBackend looks up a ready, local backend by model name.
func (d *Deps) findBackend(model string) *Backend {
	backends := d.Backends.ListBackends()
	for _, b := range backends {
		if b.ModelName == model && b.Ready && !b.Remote {
			return b
		}
	}
	return nil
}

// reverseProxy sends the request to the target backend.
func (d *Deps) reverseProxy(w http.ResponseWriter, r *http.Request, targetAddr string, body []byte) {
	// Backend addresses may be stored as "host:port" without scheme
	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}
	target, err := url.Parse(targetAddr)
	if err != nil {
		slog.Error("aima proxy: invalid backend address", "addr", targetAddr, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = r.URL.Path
			req.Host = target.Host
			req.ContentLength = int64(len(body))
			req.Body = io.NopCloser(strings.NewReader(string(body)))
		},
	}
	proxy.ServeHTTP(w, r)
}

func (d *Deps) handleLiteTTS(w http.ResponseWriter, r *http.Request, backend *Backend, raw map[string]any) {
	text := extractTTSText(raw)
	if text == "" {
		http.Error(w, `{"error":"missing or invalid input field"}`, http.StatusBadRequest)
		return
	}

	speaker, _ := raw["voice"].(string)
	if speaker == "" || speaker == "default" {
		speaker = "AIBC006_lite"
	}

	payload := map[string]any{
		"text":    text,
		"speaker": speaker,
		"version": "v2.0",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"failed to encode LiteTTS request"}`, http.StatusInternalServerError)
		return
	}

	d.forwardRequest(w, r, backend.Address, "/tts/api/v1/generate", "application/json", body)
}

func extractTTSText(raw map[string]any) string {
	if text, _ := raw["text"].(string); strings.TrimSpace(text) != "" {
		return text
	}
	if text, _ := raw["input"].(string); strings.TrimSpace(text) != "" {
		return text
	}
	return ""
}

func (d *Deps) forwardRequest(w http.ResponseWriter, r *http.Request, targetAddr, targetPath, contentType string, body []byte) {
	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}
	target, err := url.Parse(targetAddr)
	if err != nil {
		slog.Error("aima proxy: invalid backend address", "addr", targetAddr, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.String()+targetPath, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("aima proxy: backend request failed", "backend", targetAddr, "path", targetPath, "err", err)
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
