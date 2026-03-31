# OpenClaw-Multi 端到端实测 Bug 报告

**日期**: 2026-03-31
**设备**: gb10 (NVIDIA GB10, 120GB 统一内存, Ubuntu 24.04 aarch64)
**版本**: aima v0.2-dev (develop@4c52b69)
**场景**: openclaw-multi (4 模型: LLM/VLM + TTS + ASR + ImageGen)
**状态**: **ALL FIXED & VERIFIED**

## 测试方法

通过 `aima scenario apply openclaw-multi` 部署全部 4 个服务，然后模拟完整
ASR → 文生图 → VLM → TTS 链路：用 TTS 生成 "生成一个小猫的图片" 语音，
ASR 转写，ImageGen 生图，VLM 看图描述，TTS 朗读描述。

## 测试结果概览

| 步骤 | 功能 | 结果 | 问题 |
|------|------|------|------|
| scenario apply | 4 服务部署 | PASS | CDI fallback 正常 |
| openclaw sync | 配置同步 | **PASS** | BUG-3/4/5 已修复 |
| /v1/models | proxy 注册 | PASS | 4 个模型全部可见 |
| TTS → WAV | 语音合成 | PASS | 200, 268KB |
| ASR → Text | 语音识别 | **PASS** | BUG-1 已修复，清洁文本 |
| LLM chat | 聊天推理 | **PASS** | BUG-2 已修复，无思维链 |
| ImageGen | 文生图 | PASS | 451KB b64 |
| openclaw.json | 配置正确性 | **PASS** | 全部字段正确 |

---

## BUG-1: ASR 响应包含原始元数据前缀 (P0) — ✅ FIXED

**现象**: ASR 转写结果包含 `language Chinese<asr_text>` 前缀

**修复**: 在 `internal/openclaw/routes.go` 新增 `forwardASR()` + `cleanASRResponse()` + `stripASRPrefix()`，
读取后端响应后剥离 `<asr_text>` 标记前的所有前缀文本。

**验证**: ASR 返回 `{"text":"生成一个小猫的图片。"}` — 清洁文本，无前缀。

**影响文件**: `internal/openclaw/routes.go`, `internal/openclaw/routes_test.go` (+3 tests)

---

## BUG-2: Qwen3.5-9B 输出包含完整思维链 (P1) — ✅ FIXED

**现象**: VLM/LLM 请求返回 thinking process 而非简洁回答。

**修复**: 双管齐下：
1. 移除 YAML 中的 `chat_template_kwargs` 配置（该 vLLM 版本不支持 CLI flag）
2. 在 `internal/proxy/server.go` 新增 `injectDisableThinking()`，自动为所有
   `/v1/chat/completions` 请求注入 `chat_template_kwargs: {"enable_thinking": false}`
   （per-request 参数，vLLM 支持）

**验证**: `"我是 Qwen3.5，阿里巴巴最新研发的通义千问大语言模型..."` — 清洁回复，无思维链。

**影响文件**: `catalog/models/qwen3.5-9b.yaml`, `internal/proxy/server.go`, `internal/proxy/server_test.go` (+3 tests)

---

## BUG-3: openclaw.json 中 qwen3.5-9b 缺少 vision 模态 (P1) — ✅ FIXED

**修复**: 将 `catalog/models/qwen3.5-9b.yaml` 中 `metadata.type` 从 `llm` 改为 `vlm`。
sync.go 根据此字段在 `input` 中添加 `"image"`。

**验证**: openclaw.json 中 `"input": ["text", "image"]` — vision 已启用。

**影响文件**: `catalog/models/qwen3.5-9b.yaml`

---

## BUG-4: openclaw.json 中 aima provider 缺少 apiKey (P2) — ✅ FIXED

**修复**: `internal/openclaw/config.go` 的 `mergeLLMProvider()` 中使用 `directToolAPIKey()`
确保 apiKey 在为空时 fallback 到 `"local"`。

**验证**: openclaw.json 中 `"apiKey": "local"` — 认证正常。

**影响文件**: `internal/openclaw/config.go`

---

## BUG-5: z-image 被错误注册为 chat completions 模型 (P2) — ✅ FIXED

**修复**: 将 image gen provider ID 从 `"openai"` 重命名为 `"aima-imagegen"`，
避免与 ASR/media 的 openai-compatible provider 冲突。添加 legacy provider ID 清理。

**验证**: openclaw.json 中无 `"openai"` provider，image gen 正确使用 `"aima-imagegen/z-image"`。

**影响文件**: `internal/openclaw/config.go`, `internal/openclaw/claim.go`,
`internal/openclaw/managed.go`, `internal/openclaw/sync_test.go`

---

## BUG-6 (新发现): Docker bash -c 命令 shell 转义不正确 — ✅ FIXED

**现象**: 含空格/特殊字符的 config 值（如 JSON `{"enable_thinking": false}`）
在 `bash -c` 命令中未被引号保护，导致 shell 分词错误。

**修复**: `internal/runtime/docker.go` 新增 `shellJoin()` 函数，使用单引号保护
含 shell 元字符的参数。

**影响文件**: `internal/runtime/docker.go`, `internal/runtime/docker_test.go` (+1 test)

---

## 非 Bug 观察

### OBS-1: CDI GPU 路径每次 fallback
所有容器都先尝试 CDI，失败后 fallback 到 `--gpus all`。不影响功能。

### OBS-2: scenario apply 总耗时
4 服务完整部署约 10 分钟。VLM 启动含 pip install ~6 分钟。

### OBS-3: 统一内存 swap 警告
每次部署 WARN `unified memory system has swap enabled`。
