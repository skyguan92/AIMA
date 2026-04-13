# 多模态 Benchmark 设计

> 状态：Draft
> 日期：2026-04-13
> 作者：Claude Code + jguan

## 1. 问题陈述

AIMA 的 benchmark 功能完全硬编码为 LLM text-to-text 工作负载：

- **请求构造**：硬编码 OpenAI chat/completions + SSE streaming
- **指标体系**：仅 TTFT / TPOT / TPS（token 维度）
- **输入生成**：64KB 文本 padding，程序化生成
- **DB schema**：`modality` 列硬编码为 `"text"`

然而 catalog 已定义 7 种模态（llm, vlm, asr, tts, image_gen, video_gen, embedding），Explorer 能识别模型类型（`LocalModel.Type`），但 benchmark 层无法为非 LLM 模型产出有效知识。

**结果**：Explorer 发现 ASR/TTS/T2I/T2V 模型 → 尝试 benchmark → 报错或产生无意义数据 → 无法形成知识闭环。

## 2. 设计约束

1. 一次性覆盖 VLM、TTS、ASR、文生图（T2I）、文生视频（T2V）
2. MCP 工具提供多模态 benchmark 能力，Explorer Planner LLM 决定编排策略（方案 C）
3. 统一 BenchmarkResult schema + 模态特有 nullable 字段（方案 A，强类型可索引）
4. Runner 只管计时/并发/统计，请求构造交给模态适配器（方案 B，Go Interface）

## 3. 业界调研总结

### 3.1 各模态 Benchmark 方法论

| 维度 | VLM | TTS | ASR | T2I | T2V |
|------|-----|-----|-----|-----|-----|
| **核心指标** | TTFT, TPOT, tok/s（同 LLM） | RTF, TTFA, audio-sec/s | RTF, audio-hrs/hr, latency p95 | latency/img, it/s, img/s | latency/video, it/s, VRAM peak |
| **API 协议** | chat/completions + image_url | `/v1/audio/speech` | `/v1/audio/transcriptions` multipart | `/v1/images/generations` 或自定义 | 无标准，异步 POST + 轮询 |
| **输入构造** | 文本 + 图片 URL/base64 | 文本（不同长度） | 音频文件（不同时长） | 文本 prompt（内容无关紧要） | 文本 prompt |
| **关键参数** | image_count, image_resolution | text_length_chars | audio_duration_s, format | resolution, steps, guidance, batch | resolution, fps, duration, steps |
| **响应格式** | SSE streaming tokens | 音频 bytes (streaming/完整) | JSON 文本 | base64 图片 / URL | 视频文件 / job_id |
| **并发特点** | 同 LLM | 可高并发 | 可高并发 | 中等并发 | 低并发（单 video 占满 GPU） |
| **预热** | 同 LLM | 2-3 次丢弃 | 2-3 次丢弃 | 1-3 次丢弃（torch.compile） | 首次生成丢弃 |
| **业界标准** | vLLM benchmark_serving.py | 无（自定义脚本） | LibriSpeech test-clean | MLPerf SDXL | MLPerf v6.0 Wan2.2 |

### 3.2 关键发现

- **VLM** 性能 benchmark 本质就是 LLM + image_url 输入。指标完全相同（TTFT/TPOT/TPS），无 VLM 特有指标。图片 token 数由 vision encoder 决定，是输入侧变量。
- **TTS/ASR** 共用 RTF（Real-Time Factor）作为核心指标。RTF = processing_time / media_duration，越小越好，< 1.0 为实时。TTS 额外关注 TTFA（首音延迟），ASR 无此概念。
- **T2I** prompt 内容不影响性能（UNet/DiT FLOPs 恒定）。性能完全由 resolution × steps × batch_size × precision 决定。MLPerf 有 SDXL 标准。
- **T2V** 生成时间可达分钟级。MLPerf v6.0 (2026.04) 刚建立 Wan2.2 标准。并发通常为 1（独占 GPU）。API 完全非标准，通常需要异步轮询。

## 4. 架构设计

### 4.1 Requester 接口

Runner 和模态适配器之间的契约。每种模态实现一个 Requester，Runner 只负责并发调度 + 聚合统计。

```go
// internal/benchmark/requester.go

type Requester interface {
    // Do 构造并发送一个推理请求，返回采样结果。
    // seq 是请求序号（用于区分并发请求）。
    Do(ctx context.Context, endpoint string, seq int) (*Sample, error)

    // Modality 返回该适配器测试的模态标识。
    Modality() string  // "llm", "vlm", "tts", "asr", "image_gen", "video_gen"

    // WarmupRequests 返回该模态需要的预热请求数。
    WarmupRequests() int
}
```

### 4.2 Sample 结构

统一所有模态的单次请求采样。通用字段 + 模态特有字段，非相关字段为零值。

```go
// internal/benchmark/requester.go

type Sample struct {
    // ---- 通用字段（所有模态） ----
    Seq          int
    LatencyMs    float64
    Error        error

    // ---- LLM/VLM 字段 ----
    TTFTMs       float64   // Time-to-First-Token
    InputTokens  int
    OutputTokens int

    // ---- TTS 字段 ----
    TTFAMs          float64  // Time-to-First-Audio chunk
    AudioDurationS  float64  // 生成的音频时长（秒）
    InputChars      int      // 输入文本字符数

    // ---- ASR 字段 ----
    InputAudioS     float64  // 输入音频时长（秒）
    OutputChars     int      // 识别输出字符数

    // ---- T2I 字段 ----
    ImagesGenerated int
    StepsCompleted  int
    WidthPx         int
    HeightPx        int

    // ---- T2V 字段 ----
    VideoDurationS  float64
    FramesGenerated int
    FPS             int
    VideoWidthPx    int
    VideoHeightPx   int
    VideoSteps      int
}
```

### 4.3 RunResult 扩展

现有 LLM 字段全部保留。新增模态特有指标字段（非相关模态为零值）：

```go
// internal/benchmark/runner.go — RunResult 新增字段

// ---- 模态标识 ----
Modality string  // 从 Requester.Modality() 获取

// ---- TTS/ASR 共用 ----
RTFP50, RTFP95, RTFMean float64

// ---- TTS 特有 ----
TTFAP50ms, TTFAP95ms float64
AudioThroughput float64  // audio-seconds generated / wall-second
AvgInputChars int
AvgAudioDurationS float64

// ---- ASR 特有 ----
ASRThroughput float64  // audio-hours processed / wall-hour
AvgInputAudioS float64
AvgOutputChars int

// ---- T2I 特有 ----
LatencyP50ms, LatencyP95ms, LatencyP99ms float64
ImagesPerSec float64
AvgSteps int
ImageWidth, ImageHeight int

// ---- T2V 特有 ----
VideoLatencyP50s, VideoLatencyP95s float64  // 秒级（非毫秒）
VideosPerHour float64
AvgVideoDurationS float64
AvgFrames int
VideoFPS int
VideoWidth, VideoHeight int
VideoSteps int
```

### 4.4 指标计算规则

Runner 聚合 `[]Sample` 时根据 Modality 分支计算：

| 模态 | 从 Sample 取什么 | 计算什么 |
|------|-----------------|---------|
| llm/vlm | TTFTMs, InputTokens, OutputTokens | TTFT p50/p95/p99, TPOT = (Latency-TTFT)/(OutputTokens-1), TPS = Σ OutputTokens/Duration |
| tts | LatencyMs, AudioDurationS, TTFAMs, InputChars | RTF = LatencyMs/1000 / AudioDurationS, TTFA p50/p95, AudioThroughput = Σ AudioDurationS / Duration |
| asr | LatencyMs, InputAudioS, OutputChars | RTF = LatencyMs/1000 / InputAudioS, ASRThroughput = Σ InputAudioS/3600 / (Duration/3600) |
| image_gen | LatencyMs, ImagesGenerated, StepsCompleted | Latency p50/p95/p99, ImagesPerSec = Σ ImagesGenerated / Duration |
| video_gen | LatencyMs, VideoDurationS, FramesGenerated | VideoLatency p50/p95（秒级）, VideosPerHour = SuccessfulReqs / (Duration/3600) |

## 5. 适配器实现

### 5.1 ChatRequester（LLM + VLM）

```
文件: internal/benchmark/requester_chat.go
```

从现有 runner.go 提取 HTTP/SSE 逻辑。VLM 模式在 messages 中插入 image_url content blocks。

```go
type ChatRequester struct {
    Model       string
    MaxTokens   int
    InputTokens int
    ImageURLs   []string  // 非空时为 VLM 模式
}

func (r *ChatRequester) Modality() string {
    if len(r.ImageURLs) > 0 { return "vlm" }
    return "llm"
}

func (r *ChatRequester) WarmupRequests() int { return 2 }
```

- API: `POST /v1/chat/completions`，SSE streaming
- Sample 填充: TTFTMs, InputTokens, OutputTokens（从 SSE usage 提取）

### 5.2 AudioSpeechRequester（TTS）

```
文件: internal/benchmark/requester_tts.go
```

```go
type AudioSpeechRequester struct {
    Model   string
    Voice   string    // "alloy" 等
    Format  string    // "pcm", "mp3", "wav"
    Texts   []string  // 测试文本集，按 seq 轮询
}

func (r *AudioSpeechRequester) Modality() string      { return "tts" }
func (r *AudioSpeechRequester) WarmupRequests() int    { return 2 }
```

- API: `POST /v1/audio/speech`，body `{"model":"","input":"","voice":"","response_format":""}`
- 流式：记录首 chunk 到达时间 → TTFAMs；从 bytes + sample_rate 计算 AudioDurationS
- 非流式：整个音频返回，从 header 计算 AudioDurationS

### 5.3 TranscriptionRequester（ASR）

```
文件: internal/benchmark/requester_asr.go
```

```go
type TranscriptionRequester struct {
    Model      string
    AudioFiles []AudioInput  // 预加载到内存
    Language   string
}

type AudioInput struct {
    Filename  string
    Data      []byte
    DurationS float64
}

func (r *TranscriptionRequester) Modality() string   { return "asr" }
func (r *TranscriptionRequester) WarmupRequests() int { return 2 }
```

- API: `POST /v1/audio/transcriptions`，multipart/form-data
- AudioFiles 在初始化时一次性读入内存，避免磁盘 IO 干扰计时
- Sample 填充: InputAudioS（已知）, OutputChars（响应 text 字符数）, LatencyMs

### 5.4 ImageGenRequester（文生图）

```
文件: internal/benchmark/requester_image.go
```

```go
type ImageGenRequester struct {
    Model         string
    Prompt        string
    Width, Height int
    Steps         int
    GuidanceScale float64
    NumImages     int
}

func (r *ImageGenRequester) Modality() string   { return "image_gen" }
func (r *ImageGenRequester) WarmupRequests() int { return 3 }  // torch.compile 预热更长
```

- API: 先尝试 OpenAI `POST /v1/images/generations`，不兼容则走 `POST /generate`
- 同步等待，无 streaming
- 默认 prompt: `"A photo of an astronaut riding a horse on Mars"`（性能无关）

### 5.5 VideoGenRequester（文生视频 / 图生视频）

```
文件: internal/benchmark/requester_video.go
```

```go
type VideoGenRequester struct {
    Model          string
    Prompt         string
    Width, Height  int
    DurationS      float64
    FPS            int
    Steps          int
    GuidanceScale  float64
    InputImageURL  string  // 非空时为 I2V 模式
}

func (r *VideoGenRequester) Modality() string   { return "video_gen" }
func (r *VideoGenRequester) WarmupRequests() int { return 1 }  // 生成太慢，预热 1 次
```

- API 双模式：
  - 同步: `POST /generate`，等待视频返回
  - 异步: `POST /generate` 返回 job_id，轮询 `GET /status/{job_id}` 直到完成
- LatencyMs = 提交到完成的总时间

## 6. Runner 重构

### 6.1 签名变更

```go
// 现有
func Run(ctx context.Context, cfg RunConfig) (*RunResult, error)

// 重构后
func Run(ctx context.Context, cfg RunConfig, req Requester) (*RunResult, error)
```

### 6.2 内部流程

```
1. 预热: for i := 0; i < req.WarmupRequests(); i++ { req.Do(ctx, endpoint, -1) }
2. 测量: semaphore 并发控制 + req.Do() 采样（复用现有并发框架）
3. 聚合: switch req.Modality() 调用对应的 aggregateXxxMetrics()
```

模态分支**仅出现在聚合阶段**。并发控制、轮次管理、错误处理全部复用。

### 6.3 代码提取

| 现有代码 | 去向 |
|---------|------|
| HTTP 请求构造 + SSE 解析（~150 行） | `requester_chat.go` ChatRequester.Do() |
| promptPadding 生成 | `requester_chat.go`（只有 LLM/VLM 需要） |
| 并发控制、semaphore、轮次循环 | 保留 `runner.go` |
| percentile/stddev 统计工具 | 保留 `runner.go` |

## 7. MCP 工具接口变更

### 7.1 benchmark.run 扩展

新增 `modality` 必填参数 + 模态特有参数：

```yaml
tool: benchmark.run
params:
  # 通用
  modality:       string  # 必填 "llm"|"vlm"|"tts"|"asr"|"image_gen"|"video_gen"
  model:          string
  endpoint:       string
  concurrency:    int
  num_requests:   int
  hardware:       string
  engine:         string
  rounds:         int

  # LLM/VLM
  max_tokens:     int
  input_tokens:   int
  image_urls:     []string   # VLM

  # TTS
  voice:          string
  audio_format:   string
  texts:          []string

  # ASR
  audio_files:    []string   # 本地路径
  language:       string

  # T2I
  prompt:         string
  width:          int
  height:         int
  steps:          int
  guidance_scale: float
  num_images:     int

  # T2V（prompt/width/height/steps/guidance_scale 同上复用）
  duration_s:     float
  fps:            int
  input_image_url: string    # I2V
```

向后兼容：`modality` 为空时默认 `"llm"`。

### 7.2 benchmark.record 扩展

新增 `modality` 必填参数 + 所有模态的指标字段（nullable）。结构与 RunResult 一一对应。

### 7.3 benchmark.matrix 扩展

按模态扩展扫描维度：

| 模态 | 扫描维度 |
|------|---------|
| llm/vlm | concurrency × input_tokens × max_tokens |
| tts | concurrency × text_length（短/中/长） |
| asr | concurrency × audio_duration（5s/10s/30s/60s） |
| image_gen | concurrency × resolution × steps |
| video_gen | resolution × duration_s × steps（并发固定 1） |

### 7.4 benchmark.ensure_assets（新增工具）

确保 benchmark 测试素材就位。

```yaml
tool: benchmark.ensure_assets
params:
  modalities: []string  # 需要哪些模态的素材
returns:
  ready:     bool
  asset_dir: string     # ~/.aima/benchmark-assets/
  missing:   []string   # 无法下载时列出缺失模态
```

## 8. Benchmark Assets（预置测试素材）

### 8.1 存储方式

GitHub Release 附件，按模态打包：

```
aima-benchmark-assets-v1.tar.gz
├── audio/                    # ASR 测试用
│   ├── zh_5s.wav
│   ├── zh_10s.wav
│   ├── zh_30s.wav
│   ├── en_5s.wav
│   ├── en_10s.wav
│   ├── en_30s.wav
│   └── manifest.json
├── images/                   # VLM / I2V 测试用
│   ├── scene_natural.jpg
│   ├── chart_simple.png
│   ├── text_document.png
│   └── manifest.json
└── texts/                    # TTS 测试用
    ├── zh_short.txt          # 20字
    ├── zh_medium.txt         # 100字
    ├── zh_long.txt           # 500字
    ├── en_short.txt
    ├── en_medium.txt
    ├── en_long.txt
    └── manifest.json
```

### 8.2 本地缓存

`~/.aima/benchmark-assets/`，与 runtime overlay 同级。

### 8.3 下载机制

- Explorer Planner 在 benchmark 前调用 `benchmark.ensure_assets`
- 已下载 → 立即返回（offline-first）
- 未下载 + 有网 → 从 GitHub Release 下载
- 未下载 + 无网 → `ready: false`，Planner 自行决策（跳过或用 TTS 生成音频等 fallback）

### 8.4 不嵌入 binary

音频/图片可能几十 MB，go:embed 会导致 binary 膨胀，违背边缘设备轻量部署原则。

## 9. Explorer 集成

### 9.1 Planner 模态感知

Explorer Planner 根据 `ExplorationTarget.ModelType` 决定调用 `benchmark.run` 时传什么 `modality` 和参数。不需要 Go 代码变更 — Planner 是 LLM 驱动的，在 system prompt 中增加各模态的 benchmark 指导即可（方案 C）。

### 9.2 Omni 模型处理

一个 omni 模型（如 Qwen2.5-Omni）同时具备 ASR + TTS + VLM 能力。Planner 对同一模型执行多轮 benchmark，每轮测试一种能力：

```
ExplorationTarget: Qwen2.5-Omni-7B
  Round 1: benchmark.run modality="vlm"  → VLM 性能知识
  Round 2: benchmark.run modality="tts"  → TTS 性能知识
  Round 3: benchmark.run modality="asr"  → ASR 性能知识
```

每轮产出独立 BenchmarkResult，独立 L2c golden config。

### 9.3 Model YAML 扩展

```yaml
# 单模态
metadata:
  type: llm

# Omni 模型
metadata:
  type: omni
  capabilities: [vlm, tts, asr]
```

### 9.4 知识产出链路

- **L2c promote** 用 `(model, engine, hardware, modality)` 四元组作 key
- **PerfVector** 相似性搜索加 `WHERE modality = ?`，不跨模态比较
- **KnowledgeNote** 自动标注测试的模态

## 10. SQLite Schema 迁移（migrateV8）

所有新列 nullable，默认 NULL，现有数据不受影响。

```sql
-- TTS/ASR
ALTER TABLE benchmark_results ADD COLUMN rtf_p50 REAL;
ALTER TABLE benchmark_results ADD COLUMN rtf_p95 REAL;
ALTER TABLE benchmark_results ADD COLUMN rtf_mean REAL;

-- TTS
ALTER TABLE benchmark_results ADD COLUMN ttfa_p50_ms REAL;
ALTER TABLE benchmark_results ADD COLUMN ttfa_p95_ms REAL;
ALTER TABLE benchmark_results ADD COLUMN audio_throughput REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_input_chars INTEGER;
ALTER TABLE benchmark_results ADD COLUMN avg_audio_duration_s REAL;

-- ASR
ALTER TABLE benchmark_results ADD COLUMN asr_throughput REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_input_audio_s REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_output_chars INTEGER;

-- T2I
ALTER TABLE benchmark_results ADD COLUMN latency_p50_ms REAL;
ALTER TABLE benchmark_results ADD COLUMN latency_p95_ms REAL;
ALTER TABLE benchmark_results ADD COLUMN latency_p99_ms REAL;
ALTER TABLE benchmark_results ADD COLUMN images_per_sec REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_steps INTEGER;
ALTER TABLE benchmark_results ADD COLUMN image_width INTEGER;
ALTER TABLE benchmark_results ADD COLUMN image_height INTEGER;

-- T2V
ALTER TABLE benchmark_results ADD COLUMN video_latency_p50_s REAL;
ALTER TABLE benchmark_results ADD COLUMN video_latency_p95_s REAL;
ALTER TABLE benchmark_results ADD COLUMN videos_per_hour REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_video_duration_s REAL;
ALTER TABLE benchmark_results ADD COLUMN avg_frames INTEGER;
ALTER TABLE benchmark_results ADD COLUMN video_fps INTEGER;
ALTER TABLE benchmark_results ADD COLUMN video_width INTEGER;
ALTER TABLE benchmark_results ADD COLUMN video_height INTEGER;
ALTER TABLE benchmark_results ADD COLUMN video_steps INTEGER;
```

## 11. 向后兼容

| 变更点 | 兼容策略 |
|-------|---------|
| `modality` 参数 | 未传时默认 `"llm"` |
| 历史数据 `modality="text"` | 不迁移，查询时 `WHERE (modality = ? OR (? = 'llm' AND modality = 'text'))` |
| `Run()` 函数签名 | 内部 API，一次性修改所有调用方 |
| PerfVector 查询 | 加 `WHERE modality = ?` 条件 |
| Export/Import | 新列自然出现在 JSON 中，import 用 sql.NullFloat64 |
| CLI | 保持不变（默认 LLM），新增 `--modality` flag |

## 12. 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/benchmark/requester.go` | 新增 | Requester 接口 + Sample 结构 |
| `internal/benchmark/requester_chat.go` | 新增 | ChatRequester（LLM/VLM），从 runner.go 提取 |
| `internal/benchmark/requester_tts.go` | 新增 | AudioSpeechRequester |
| `internal/benchmark/requester_asr.go` | 新增 | TranscriptionRequester + AudioInput |
| `internal/benchmark/requester_image.go` | 新增 | ImageGenRequester |
| `internal/benchmark/requester_video.go` | 新增 | VideoGenRequester |
| `internal/benchmark/runner.go` | 修改 | Run() 签名 + 聚合分支 + 提取 HTTP/SSE 逻辑 |
| `internal/state/sqlite.go` | 修改 | migrateV8 + BenchmarkResult struct 新字段 + 写入/查询逻辑 |
| `internal/mcp/tools.go` | 修改 | benchmark.run/record/matrix schema + benchmark.ensure_assets |
| `cmd/aima/tooldeps_benchmark.go` | 修改 | buildRequester() 工厂 + 参数解析 + 资产下载 |
| `internal/cli/benchmark.go` | 修改 | --modality flag |
| `internal/knowledge/resolver.go` | 修改 | PerfVector 查询加 modality 条件 |
| `internal/knowledge/query.go` | 修改 | Similar() 加 modality 过滤 |
| `catalog/models/*.yaml` | 修改 | omni 模型加 capabilities 字段 |

## 13. 不做的事

- 不迁移历史 `modality="text"` → `"llm"` — 查询时兼容更安全
- 不拆分 benchmark_results 表 — 统一表 + nullable 列
- 不为每种模态创建独立 MCP 工具 — 单个 benchmark.run + modality 参数
- 不内置 benchmark 编排逻辑 — Explorer Planner LLM 决定
- 不嵌入测试素材到 binary — GitHub Release 按需下载
