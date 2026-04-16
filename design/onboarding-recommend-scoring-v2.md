# Onboarding Recommend 评分重构设计 (v2)

## 1. 现状与问题

### 1.1 当前实现

评分逻辑在 `internal/onboarding/recommend.go` 的 `computeFitScore()` 函数（L307-363）。
当前已有的维度：

| 维度 | 分值范围 | 来源 |
|------|---------|------|
| 基础分 (fit=true) | +100 | `CheckFit()` |
| 模态权重 | 0-200 | `modalityWeight()` — LLM=200, VLM=150 |
| 本地资源 | 0-380 | model +200, engine +100, golden +80 |
| GPU arch 匹配 | 0 或 +60 | `variant.Hardware.GPUArch == hw.GPUArch` |
| VRAM 利用率 | -50 ~ +50 | `VRAMMinMiB / GPUVRAMMiB` |
| 最大可跑模型比 | 0-120 | `largestFittableBonus()` |
| 新鲜度 | 0-100 | `recencyBonus()` — 基于 `released_at` |
| 多卡惩罚 | -30×(N-1) | `GPUCountMin > 1` |

**理论极值**：0 ~ ~1010。实际典型分数在 200-700 之间。

### 1.2 三个问题

**P1: 无带宽-架构亲和度。**
GB10（128GB unified, ~273 GB/s）和 RTX 4090（24GB, 1008 GB/s）VRAM/带宽比相差 20 倍，
但评分不区分 MoE 和 Dense 模型的适配性。GB10 上 MoE (30B-A3B) 激活仅 3B 参数，
decode 带宽需求 ≈ Dense 8B，但能力远超 8B；RTX 4090 带宽充裕，Dense 8B 可跑 60 tok/s，
MoE 的 expert routing 开销反而是负担。

**P2: LLM 优先度不够稳。**
LLM=200, ASR=60, 差 140 分。但本地资源（+380）+ VRAM 利用率（+50）可达 430 分，
理论上一个全部本地就绪且 VRAM 利用率高的 ASR 可以反超一个无本地资源的 LLM。
onboarding 场景下 LLM 应该不可被 ASR/TTS 反超。

**P3: 分数无直观含义。**
200-700 的范围对用户和前端没有语义。百分制（70+ 强推荐、50-70 可用、<50 不推荐）更直觉。

---

## 2. 设计目标

1. 归一化到 0-100，分段有语义
2. LLM/VLM 在 onboarding 场景下不可能被 ASR/TTS/image_gen 反超
3. VRAM-rich 低带宽设备偏好 MoE，BW-rich 设备偏好 Dense
4. 改动最小化：复用现有数据源和函数，不引入新的检测机制

---

## 3. 新评分公式

```
TotalScore = D1(模态) + D2(硬件匹配) + D3(本地就绪) + D4(模型质量) + D5(部署简洁性)
           = [0-30]   + [0-25]        + [0-20]       + [0-15]       + [0-10]
           = [0-100]
```

### 3.1 D1: 模态优先级 (0-30)

改写现有 `modalityWeight()`，返回值从 0-200 缩至 0-30。

```go
func modalityScore(modelType string) int {
    switch strings.ToLower(strings.TrimSpace(modelType)) {
    case "llm":                                  return 30
    case "vlm":                                  return 25
    case "embedding", "rerank":                  return 8
    case "asr", "tts":                           return 5
    case "image_gen", "video_gen":               return 3
    default:                                     return 2
    }
}
```

**不可反超保证**：LLM(30) - ASR(5) = 25 分差距。其余 D2-D5 最多补偿 25+20+15+10 = 70 分，
但 ASR 在 D4（模型质量）上几乎不可能满分（参数量小、候选少），实际最大补偿 ≈ 15 分。
LLM 只需在 D2-D5 上不全丢分即可稳赢。

**与现有代码的差异**：现有 `modalityWeight()` 用 "image"/"video"/"t2i"/"t2v"/"i2v"，
但 catalog YAML 实际 type 是 "image_gen"/"video_gen"。需要修正这个 bug。

### 3.2 D2: 硬件匹配 (0-25)

```
D2 = D2a(VRAM 利用率甜区) + D2b(带宽架构亲和度) + D2c(GPU arch 匹配)
   = [0-12]              + [0-8]                + {0 或 5}
```

#### D2a: VRAM 利用率甜区 (0-12)

改写现有 VRAM 利用率逻辑（L339-351），从 -50~+50 缩至 0-12，改为倒 U 形曲线。

```
有效VRAM = effectiveVRAMMiB(hw)  // 见 §4.1
利用率 = variant.Hardware.VRAMMinMiB / 有效VRAM

利用率 ≤ 30%   →  2   (太小，浪费硬件)
30% - 50%      →  5
50% - 70%      → 10
70% - 85%      → 12   (最佳甜区)
85% - 95%      →  6   (接近上限)
> 95%          →  0   (几乎必然 OOM)
```

**数据来源**：`variant.Hardware.VRAMMinMiB`（已有）、`hw.GPUVRAMMiB`（已有）、`hw.RAMTotalMiB`（已有）、`hw.UnifiedMemory`（已有）。全部为当前代码已采集的字段。

#### D2b: 带宽架构亲和度 (0-8) ← 新增

核心指标 `ratio = effectiveVRAM(GB) / gpuBandwidth(GB/s)`：

```
ratio < 0.1   → BW-rich   (RTX 4090=0.024, RTX 4060=0.029, W7900D=0.056)
0.1 ≤ ratio ≤ 0.3 → Neutral (Apple M4=0.133, MetaX N260=0.160)
ratio > 0.3   → VRAM-rich (GB10=0.469, AMD 8060S=1.067, M1000=1.240)
```

得分矩阵：

|  | MoE 模型 | Dense 模型 |
|---|---------|-----------|
| BW-rich  | 5 | 8 |
| Neutral  | 6 | 6 |
| VRAM-rich | 8 | 2 |

**MoE 检测**：复用现有 `extractActiveParams()`（L571-590），若返回非空字符串则为 MoE。
当前 catalog 中所有 MoE 模型（qwen3-30b-a3b, qwen3.5-35b-a3b, qwen3.5-122b-a10b,
gemma-4-26b-a4b-it, wan22-t2v-a14b, wan22-i2v-a14b）均遵循 `-a{X}b` 命名，检测率 100%。

**带宽未知 fallback**：`gpuBandwidth ≤ 0` 时返回 5（中性分），不偏向任何架构。

**数据来源**：需要新增 `gpu.bandwidth_gbps` 字段到 hardware YAML（见 §5）。

#### D2c: GPU arch 匹配 (0 或 5)

与现有逻辑相同（L335-336），仅缩放：`variant.Hardware.GPUArch == hw.GPUArch` → +5（原 +60）。
通配 `gpu_arch: "*"` 不匹配，得 0。

### 3.3 D3: 本地就绪 (0-20)

缩放现有本地资源加分（原 +200/+100/+80 → +10/+6/+4）。

| 子项 | 分值 | 检测方式（已有） |
|------|------|----------------|
| 模型已下载 | +10 | `localModels[modelName] != nil`（`buildLocalModelMap`） |
| 引擎已装 | +6 | `engineStatus.Installed`（`checkEngineStatus`） |
| Golden config 存在 | +4 | `kStore.Search(..., Status:"golden")`（L160-172） |

**数据来源**：三个子项全部使用现有检测函数，零新增。

### 3.4 D4: 模型质量 (0-15)

```
D4 = D4a(最大可跑模型比) + D4b(新鲜度)
   = [0-8]              + [0-7]
```

#### D4a: 最大可跑模型比 (0-8)

缩放现有 `largestFittableBonus()` → 改名为 `largestFittableScore()`（原 0-120 → 0-8）。

```go
func largestFittableScore(ma *knowledge.ModelAsset, maxFitBillion float64) int {
    // 复用现有 parseParamBillion()
    b := parseParamBillion(ma.Metadata.ParameterCount)
    if b <= 0 || maxFitBillion <= 0 {
        return 0
    }
    ratio := b / maxFitBillion
    if ratio > 1 { ratio = 1 }
    return int(ratio * 8)
}
```

**数据来源**：`parseParamBillion()`（已有），`maxFitBillion`（Recommend 第一遍计算，已有）。

#### D4b: 新鲜度 (0-7)

缩放现有 `recencyBonus()` → 改名为 `recencyScore()`（原 0-100 → 0-7）。

```go
func recencyScore(releasedAt string) int {
    t := parseReleasedAt(releasedAt)  // 复用现有函数
    if t.IsZero() { return 0 }
    months := int(time.Since(t).Hours() / 24 / 30)
    if months < 0 { months = 0 }
    bonus := 7 - months/4  // 每 4 个月衰减 1 分
    if bonus < 0 { bonus = 0 }
    return bonus
}
```

当前 catalog `released_at` 分布（以 2026-04 为基准）：
- 2025-12（qwen3.5 系列）→ 4 个月 → 7 - 1 = **6**
- 2025-10（gemma-4 系列）→ 6 个月 → 7 - 1 = **6**
- 2025-09（qwen3.5-9b/27b）→ 7 个月 → 7 - 1 = **6**
- 2025-04（qwen3 系列）→ 12 个月 → 7 - 3 = **4**
- 2024-12（litetts）→ 16 个月 → 7 - 4 = **3**
- 2024-09（funasr）→ 19 个月 → 7 - 4 = **3**
- 2026-01（qwen3-coder-next）→ 3 个月 → 7 - 0 = **7**
- 2026-02（ltx23）→ 2 个月 → 7 - 0 = **7**

**数据来源**：`released_at` 字段（已有，28/28 模型已填写），`parseReleasedAt()`（已有）。

### 3.5 D5: 部署简洁性 (0-10)

缩放现有多卡惩罚（原 -30×(N-1) → 正分制）。

```
variant.Hardware.GPUCountMin ≤ 1 → 10  (单卡)
GPUCountMin = 2                  →  5  (双卡)
GPUCountMin ≥ 3                  →  2  (三卡+)
```

**数据来源**：`variant.Hardware.GPUCountMin`（已有）。

---

## 4. 辅助函数

### 4.1 effectiveVRAMMiB

```go
func effectiveVRAMMiB(hw knowledge.HardwareInfo) int {
    if hw.UnifiedMemory {
        if hw.RAMTotalMiB > hw.GPUVRAMMiB {
            return hw.RAMTotalMiB
        }
        return hw.GPUVRAMMiB
    }
    count := hw.GPUCount
    if count <= 0 { count = 1 }
    return hw.GPUVRAMMiB * count
}
```

验证：
- GB10: UnifiedMemory=true, RAMTotalMiB=131072 > GPUVRAMMiB=15360 → **131072 MiB (128 GB)** ✓
- RTX 4090 ×2: UnifiedMemory=false, GPUVRAMMiB=24576, GPUCount=2 → **49152 MiB (48 GB)** ✓
- Apple M4: UnifiedMemory=true, RAMTotalMiB=16384, GPUVRAMMiB=0 → **16384 MiB (16 GB)** ✓
- M1000: UnifiedMemory=true, RAMTotalMiB=63528, GPUVRAMMiB=63488 → **63528 MiB (62 GB)** ✓

注意：RTX 4090 profile 的 `gpu.vram_mib=49152`（是双卡总和），但 `hal detect` 返回的
`hw.GPUVRAMMiB` 是 per-GPU 值 24576。`effectiveVRAMMiB` 对离散 GPU 做 `per-GPU × count`，结果一致。

### 4.2 isMoE

```go
func isMoE(ma *knowledge.ModelAsset) bool {
    return extractActiveParams(ma) != ""
}
```

直接复用现有 `extractActiveParams()`。不新增 YAML 字段，不新增检测逻辑。

---

## 5. 数据变更：GPU 显存带宽

### 5.1 问题

当前 hardware YAML 中 `ram.bandwidth_gbps` 是**系统内存带宽**，不是 GPU 显存带宽。
对于统一内存设备两者相同，但对于离散 GPU 差异巨大：

| Profile | `ram.bandwidth_gbps` (系统) | GPU 显存带宽 (实际) |
|---------|---------------------------|-------------------|
| RTX 4090 | 50 | 1008 GB/s (GDDR6X) |
| RTX 4060 | 40 | 272 GB/s (GDDR6) |
| W7900D | 460 | 864 GB/s (GDDR6) |
| Ascend 910B | 200 | 394 GB/s (HBM2e) |
| MetaX N260 | 200 | 400 GB/s (HBM2e) |
| Hygon BW150 | 200 | 400 GB/s (HBM2) |

统一内存设备的 GPU 和 RAM 共享同一物理带宽：

| Profile | `ram.bandwidth_gbps` | GPU 带宽 (=RAM 带宽) |
|---------|---------------------|---------------------|
| GB10 | 200 | 273 GB/s (LPDDR5X 实测) |
| Apple M4 | 120 | 120 GB/s (LPDDR5X 规格) |
| AMD 8060S | 60 | 120 GB/s (LPDDR5X 规格) |
| M1000 | 50 | ~50 GB/s (LPDDR5 估算) |
| M1000 SoC | 60 | ~60 GB/s (LPDDR5X 估算) |

### 5.2 改动方案

**在 `GPUSpec` 增加一个字段**（`internal/knowledge/loader.go`）：

```go
type GPUSpec struct {
    Arch             string `yaml:"arch"`
    VRAMMiB          int    `yaml:"vram_mib"`
    BandwidthGbps    int    `yaml:"bandwidth_gbps"`  // 新增: per-GPU 显存带宽
    ComputeID        string `yaml:"compute_id"`
    // ...
}
```

**在 11 个 hardware YAML 中添加 `gpu.bandwidth_gbps`**：

| Profile | `gpu.bandwidth_gbps` | 数据来源 |
|---------|---------------------|---------|
| nvidia-rtx4090-x86 | 1008 | [NVIDIA 官方规格](https://www.nvidia.com/en-us/geforce/graphics-cards/40-series/rtx-4090/) |
| nvidia-rtx4060-x86 | 272 | [NVIDIA 官方规格](https://www.nvidia.com/en-us/geforce/graphics-cards/40-series/rtx-4060/) |
| nvidia-gb10-arm64 | 273 | [NVIDIA DGX Spark 规格](https://www.nvidia.com/en-us/products/workstations/dgx-spark/)，实测吻合 |
| apple-m4-arm64 | 120 | [Apple 规格](https://www.apple.com/macbook-pro/specs/) |
| amd-radeon-8060s-x86 | 120 | LPDDR5X-7500 双通道规格（APU 共享带宽） |
| amd-w7900d-x86 | 864 | [AMD 官方规格](https://www.amd.com/en/products/professional-graphics/amd-radeon-pro-w7900-dual-slot) |
| ascend-910b-arm64 | 394 | [Huawei Ascend 910B 规格](https://www.hiascend.com/hardware/product)，HBM2e |
| metax-n260-x86 | 400 | MetaX 产品文档，HBM2e |
| hygon-bw150-dcu-x86 | 400 | Hygon DCU 产品文档，HBM2 |
| moore-threads-m1000-arm64 | 50 | LPDDR5 估算（无官方 GPU 带宽数据） |
| moore-threads-m1000-soc-arm64 | 60 | LPDDR5X 估算（AIBook 产品文档） |

**传递到评分函数**（同 `TDPWatts` 的已有模式）：

1. `Catalog` 新增 `FindGPUBandwidth(hw HardwareInfo) int`（3 行，同 `FindHardwareTDP` 模式）
2. `HardwareInfo` 新增 `GPUBandwidthGbps int` 字段
3. `buildHardwareInfo()` 中追加一行 `hwInfo.GPUBandwidthGbps = cat.FindGPUBandwidth(hwInfo)`
4. `computeFitScore()` 通过 `hw.GPUBandwidthGbps` 读取

### 5.3 带宽数据验证状态

| Profile | 带宽值可信度 | 说明 |
|---------|------------|------|
| RTX 4090 | **高** — 官方规格 | 1008 GB/s (384-bit GDDR6X @21Gbps) |
| RTX 4060 | **高** — 官方规格 | 272 GB/s (128-bit GDDR6 @17Gbps) |
| GB10 | **高** — 官方+实测 | 273 GB/s (LPDDR5X-8533 实测) |
| Apple M4 | **高** — 官方规格 | 120 GB/s (LPDDR5X) |
| W7900D | **高** — 官方规格 | 864 GB/s (384-bit GDDR6 @18Gbps) |
| AMD 8060S | **中** — 规格推算 | ~120 GB/s (LPDDR5X-7500 APU 共享) |
| Ascend 910B | **中** — 产品文档 | ~394 GB/s (HBM2e) |
| MetaX N260 | **中** — 产品文档 | ~400 GB/s (HBM2e) |
| Hygon BW150 | **中** — 产品文档 | ~400 GB/s (HBM2) |
| M1000 | **低** — 估算 | ~50 GB/s (无官方 GPU 带宽) |
| M1000 SoC | **低** — 估算 | ~60 GB/s (AIBook 产品文档无精确值) |

低可信度的值（M1000/M1000 SoC）对推荐影响有限：这两台设备的 ratio 已经 >1.0（极端 VRAM-rich），
即使带宽估算偏差 ±30%，ratio 仍然 >0.3，MoE 偏好不变。

### 5.4 ratio 验证

基于新 `gpu.bandwidth_gbps` 计算的各设备 ratio：

| 设备 | 有效VRAM(GB) | GPU带宽(GB/s) | ratio | 分类 |
|------|-------------|-------------|-------|------|
| RTX 4090 ×2 | 48 | 2016 | 0.024 | BW-rich |
| RTX 4060 | 8 | 272 | 0.029 | BW-rich |
| W7900D ×8 | 384 | 6912 | 0.056 | BW-rich |
| Apple M4 | 16 | 120 | 0.133 | Neutral |
| MetaX N260 ×2 | 128 | 800 | 0.160 | Neutral |
| Hygon BW150 ×8 | 512 | 3200 | 0.160 | Neutral |
| Ascend 910B ×8 | 512 | 3152 | 0.162 | Neutral |
| GB10 | 128 | 273 | 0.469 | VRAM-rich |
| AIBook M1000 SoC | 32 | 60 | 0.533 | VRAM-rich |
| AMD 8060S | 128 | 120 | 1.067 | VRAM-rich |
| M1000 | 62 | 50 | 1.240 | VRAM-rich |

分类符合直觉：离散高端 GPU 全部 BW-rich，统一内存 SoC 全部 VRAM-rich，HBM 设备为 Neutral。

---

## 6. 模拟计算

### 6.1 GB10: qwen3-30b-a3b (MoE) vs qwen3-8b (Dense)

GB10: 128GB unified, 273 GB/s, Blackwell, ratio=0.469 (VRAM-rich)

| 维度 | qwen3-30b-a3b (MoE) | qwen3-8b (Dense) |
|------|---------------------|------------------|
| D1 模态 | 30 (LLM) | 30 (LLM) |
| D2a VRAM利用率 | 20480/131072=15.6% → **2** | 6144/131072=4.7% → **2** |
| D2b 带宽亲和度 | VRAM-rich + MoE → **8** | VRAM-rich + Dense → **2** |
| D2c arch匹配 | Blackwell variant → **5** | Blackwell variant → **5** |
| D3 本地就绪 | 0 (假设均未下载) | 0 |
| D4a 最大可跑比 | 30B 大模型 → **~6** | 8B 小模型 → **~2** |
| D4b 新鲜度 | 2025-04 → **4** | 2025-04 → **4** |
| D5 部署简洁 | 单卡 → **10** | 单卡 → **10** |
| **总分** | **65** | **55** |

MoE 高 10 分。带宽亲和度贡献 6 分，模型大小贡献 4 分。**符合预期**。

### 6.2 RTX 4090 单卡: qwen3-8b (Dense Ada) vs qwen3-30b-a3b (MoE llamacpp)

RTX 4090 ×1: 24GB, 1008 GB/s, Ada, ratio=0.024 (BW-rich)

| 维度 | qwen3-8b Dense (Ada vllm) | qwen3-30b-a3b MoE (universal) |
|------|--------------------------|-------------------------------|
| D1 模态 | 30 | 30 |
| D2a VRAM | 16384/24576=66.7% → **10** | universal vram_min=0 → 需按实际估算 |
| D2b 带宽 | BW-rich + Dense → **8** | BW-rich + MoE → **5** |
| D2c arch | Ada variant → **5** | universal → **0** |
| D3 | 0 | 0 |
| D4a 最大可跑比 | 8B → **~3** | 30B → **~6** |
| D4b 新鲜度 | 2025-04 → **4** | 2025-04 → **4** |
| D5 部署 | 单卡 → **10** | 单卡 → **10** |
| **总分** | **~70** | **~55** |

Dense 高 15 分。高带宽 + 有 Ada 专用变体 + VRAM 利用率更优。**符合预期**。

### 6.3 Apple M4: qwen3-4b (LLM) vs qwen3-asr-1.7b (ASR)

Apple M4: 16GB unified, 120 GB/s, ratio=0.133 (Neutral)

| 维度 | qwen3-4b (LLM) | qwen3-asr-1.7b (ASR) |
|------|----------------|----------------------|
| D1 模态 | **30** | **5** |
| D2 硬件 | ~8 | ~8 |
| D3 | 0 | 0 |
| D4 | ~5 | ~5 |
| D5 | 10 | 10 |
| **总分** | **~53** | **~28** |

LLM 高 25 分，差距来自 D1 模态。**不可能反超**。

### 6.4 M1000: qwen3.5-35b-a3b (MoE) vs qwen3-14b (Dense)

M1000: 62GB unified, ~50 GB/s, MUSA, ratio=1.24 (极端 VRAM-rich)

| 维度 | qwen3.5-35b-a3b (MoE) | qwen3-14b (Dense) |
|------|----------------------|-------------------|
| D1 | 30 | 30 |
| D2a | 40960/63528=64.5% → **10** | universal → **~2** |
| D2b | VRAM-rich + MoE → **8** | VRAM-rich + Dense → **2** |
| D2c | MUSA 无此模型变体 → **0** | 无 MUSA variant → **0** |
| D3 | 0 | 0 |
| D4a | 35B → **~7** | 14B → **~4** |
| D4b | 2025-12 → **6** | 2025-04 → **4** |
| D5 | 单卡 → **10** | 单卡 → **10** |
| **总分** | **71** | **52** |

MoE 高 19 分。VRAM 利用率 (+8) + 带宽亲和度 (+6) + 模型质量 (+5)。**符合预期**。

注意：qwen3.5-35b-a3b 在 M1000 上没有 MUSA 专用变体（MetaX BLOCKED），实际走不走得通取决于
catalog 是否有可用 variant。如果无 variant 则 `evaluateModelAsset` 直接 skip，不会出现在列表中。
此处用 qwen3-30b-a3b（有 MUSA variant）替代会得到更高分（D2c = +5）。

---

## 7. 改动范围

### 7.1 需要修改的文件

| 文件 | 改动 | 行数估算 |
|------|------|---------|
| `internal/knowledge/loader.go` | `GPUSpec` 加 `BandwidthGbps int` 字段 | +1 行 |
| `internal/knowledge/resolver.go` | `HardwareInfo` 加 `GPUBandwidthGbps int` 字段；`FindGPUBandwidth()` 方法 | +6 行 |
| `cmd/aima/resolve.go` | `buildHardwareInfo()` 加一行 `hwInfo.GPUBandwidthGbps = cat.FindGPUBandwidth(hwInfo)` | +1 行 |
| `catalog/hardware/*.yaml` ×11 | 每个文件在 `gpu:` 下加 `bandwidth_gbps: N` | 每个 +1 行 |
| `internal/onboarding/recommend.go` | 重写 `computeFitScore()` + 新增 `effectiveVRAMMiB()` / `bandwidthAffinityScore()` | ~80 行 net |
| `internal/onboarding/recommend_test.go` | 更新测试用例的期望分数范围 | ~30 行 |

### 7.2 不需要修改的文件

- `internal/onboarding/types.go` — `FitScore int` 字段不变，只是值域从 0-1000+ 变为 0-100
- `internal/onboarding/deps.go` — 不变
- `catalog/models/*.yaml` — 不变（MoE 通过名称检测，released_at 已有）
- `internal/mcp/tools_onboarding.go` — 不变（评分逻辑内聚在 recommend.go）
- CLI/UI — 不变（分数直接映射到 0-100）

### 7.3 复用汇总

| 现有函数/数据 | 用途 | 改动 |
|-------------|------|------|
| `extractActiveParams()` | MoE 检测 | 不改 |
| `parseParamBillion()` | 模型大小比较 | 不改 |
| `recencyBonus()` / `parseReleasedAt()` | 新鲜度 | 缩放返回值 + 改名 `recencyScore()` |
| `modalityWeight()` | 模态优先级 | 缩放返回值 + 修正 type 名称 bug + 改名 `modalityScore()` |
| `largestFittableBonus()` | 最大可跑模型比 | 缩放返回值 + 改名 `largestFittableScore()` |
| `checkEngineStatus()` | 引擎检测 | 不改 |
| `buildLocalModelMap()` | 本地模型检测 | 不改 |
| `kStore.Search()` | Golden config 检测 | 不改 |
| `variant.Hardware.VRAMMinMiB` | VRAM 需求 | 不改 |
| `variant.Hardware.GPUCountMin` | 多卡需求 | 不改 |
| `released_at` 字段 | 新鲜度 | 不改（28/28 已填写） |

---

## 7.4 模型变体 `ram_min_mib` 的数据来源

llamacpp GGUF variant 没有 `vram_min_mib`（走 CPU 推理），需要 `ram_min_mib` 支撑 D2a 打分。
数值按 Q4_K_M 量化经验公式估算：

```
ram_min_mib ≈ params(B) × 640 MiB + overhead(KV cache + runtime)
            = params(B) × 0.625 GB/param × 1024  +  1-3 GB overhead
```

当前已填入的 9 个 variant：

| 模型 | ram_min_mib (MiB) | params | Q4_K_M 权重 | overhead | 说明 |
|------|------------------|-------|------------|---------|------|
| qwen3-4b | 3072 (3 GB) | 4B | ~2.5 GB | 0.5 GB | 小模型，KV 开销占比较大 |
| qwen3-8b | 6144 (6 GB) | 8B | ~5.0 GB | 1.0 GB | |
| qwen3.5-9b | 7168 (7 GB) | 9B | ~5.5 GB | 1.5 GB | VLM，视觉编码器额外开销 |
| qwen3-14b | 10240 (10 GB) | 14B | ~8.5 GB | 1.5 GB | |
| qwen3-30b-a3b | 20480 (20 GB) | 30B | ~18 GB | 2.0 GB | MoE，全部专家都要加载 |
| qwen3-32b | 22528 (22 GB) | 32B | ~19 GB | 2.5 GB | |
| qwen3.5-35b-a3b | 24576 (24 GB) | 35B | ~21 GB | 3.0 GB | MoE（RDNA3 + universal 两处均填） |
| qwen3-coder-next-fp8 | 53248 (52 GB) | 80B | ~48 GB | 3.0 GB | 128K context 需要更大 KV |

**估算准确性**：误差 ±15%，对 D2a 分档（30/50/70/85/95% 阈值）足够。运行时实际内存占用受
上下文长度、batch size、KV cache 预留、并发请求数影响，不需要精确匹配。

**维护约定**：新增 llamacpp variant 时：
1. 默认量化为 Q4_K_M → `ram_min_mib = params_billion × 640 + 1024 × ceil(2-4GB)`
2. MoE 模型不 discount（所有专家都加载）
3. 数值以 MiB 为单位，便于与 `RAMTotalMiB` 直接比较

---

## 8. 不做什么

遵循 CLAUDE.md 设计原则，以下有意不做：

1. **不新增 `architecture: moe` YAML 字段** — `extractActiveParams()` 对当前 catalog 检测率 100%。
   如果未来有不遵循 `-a{X}b` 命名的 MoE 模型，那时再加。YAGNI。
2. **不新增 `released_at` 以外的新鲜度机制** — 不硬编码家族名映射。`released_at` 已全量覆盖。
3. **不修改 `RecommendResult`/`ModelRecommendation` 类型** — 分数字段 `FitScore int` 不变，只是值域变了。
4. **不新增 MCP tool** — 评分逻辑是 `Recommend()` 的内部实现，不暴露为独立工具。
5. **不修改 `ram.bandwidth_gbps` 的语义** — 保留为系统 RAM 带宽，新增 `gpu.bandwidth_gbps` 平行存在。
6. **不为带宽估算值不确定的设备（M1000）增加复杂 fallback 逻辑** — 当前估算值已足够区分 ratio 分类。
