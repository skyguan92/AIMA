# OpenClaw MCP 双向集成设计方案

> 版本: v1.0 | 日期: 2026-03-31 | 状态: 草案

## 1. 背景与动机

### 现状

当前 AIMA → OpenClaw 是**单向集成**：

```
AIMA (80+ MCP tools, 推理引擎管理)
  │
  ├── openclaw sync ──→ 写入 openclaw.json providers（模型服务）
  ├── proxy routes ──→ /v1/audio/speech, /v1/audio/transcriptions, /v1/images/generations
  ├── skills 部署 ──→ ~/.openclaw/skills/aima-{asr,tts,image-gen}/
  └── auto-sync loop ──→ 每 10s 检查漂移并修正
```

OpenClaw 只能"使用"AIMA 的推理服务（聊天、ASR、TTS、图像生成），**无法操控 AIMA**——不能部署/删除模型、切换引擎、管理硬件、运行 benchmark、查询知识库等。

### 目标

让 OpenClaw 的 AI Agent 能通过 MCP 协议调用 AIMA 的**全部 80+ 工具**，实现双向集成：

```
OpenClaw Agent
  │
  ├── 推理服务（现有）──→ AIMA proxy /v1/chat/completions 等
  └── 设备操控（新增）──→ AIMA MCP Server (80+ tools)
       ├── deploy.run / deploy.delete / deploy.status
       ├── model.pull / model.list / engine.scan
       ├── hardware.detect / hardware.metrics
       ├── benchmark.run / knowledge.resolve
       └── ... 全部工具
```

### 关键发现

经过对两个系统的调研（2026-03-31），确认：

| 组件 | 状态 | 说明 |
|------|------|------|
| AIMA MCP Server | ✅ 已完备 | stdio (`ServeStdio`) + HTTP (`ServeHTTP`), 80+ tools |
| OpenClaw MCP Client Registry | ✅ 已完备 | `mcp.servers` 配置, `openclaw mcp set/list/show/unset` CLI |
| OpenClaw Agent 运行时消费 MCP | ✅ 已确认 | embedded Pi agent 读取 `mcp.servers`, 支持 `command` (stdio) 和 `url` (HTTP) |
| 连接线 | ❌ 缺失 | AIMA sync 未注册自己到 `mcp.servers` |

**结论：两边基础设施都已完备，只需把管子接上。**

## 2. OpenClaw MCP 能力详情

### 版本信息

- 测试版本: OpenClaw 2026.3.28 (f9b1079)
- 安装位置: `~/.npm-global/lib/node_modules/openclaw/`

### MCP Server 配置格式

```typescript
// openclaw.json → mcp.servers
type McpServerConfig = {
    command?: string;           // stdio 传输: 可执行文件路径
    args?: string[];            // stdio 传输: 命令参数
    env?: Record<string, string | number | boolean>;  // 环境变量
    cwd?: string;               // 工作目录
    workingDirectory?: string;  // 工作目录（别名）
    url?: string;               // HTTP 传输: URL
    [key: string]: unknown;     // 扩展字段
};
```

### CLI 管理命令

```bash
openclaw mcp list              # 列出已注册的 MCP servers
openclaw mcp show <name>       # 查看某个 server 配置
openclaw mcp set <name> <json> # 注册/更新 MCP server
openclaw mcp unset <name>      # 删除 MCP server
```

### 运行时消费路径

```
openclaw.json → mcp.servers.aima
  → OpenClaw Agent 运行时 (embedded Pi / runtime adapters)
    → 读取 server 配置
    → 根据传输类型连接:
       stdio: spawn command + args, JSON-RPC over stdin/stdout
       HTTP:  POST url, JSON-RPC over HTTP
    → tools/list 发现可用工具
    → tools/call 调用工具
```

### 配置示例

```json
{
  "mcp": {
    "servers": {
      "aima": {
        "command": "aima",
        "args": ["mcp"],
        "env": { "AIMA_API_KEY": "xxx" }
      }
    }
  }
}
```

## 3. AIMA MCP Server 现状

### 传输层

| 传输 | 实现 | 入口 | 说明 |
|------|------|------|------|
| stdio | `server.go:117 ServeStdio()` | 已实现，无 CLI 入口 | JSON-RPC over stdin/stdout |
| HTTP POST | `server.go:165 ServeHTTP()` | `serve --mcp` 启动 `:9090/mcp` | JSON-RPC over HTTP |

### 工具清单（80+ tools, 16 categories）

| 类别 | 工具数 | 示例 |
|------|--------|------|
| hardware | 2 | detect, metrics |
| model | 6 | scan, list, pull, import, info, remove |
| engine | 7 | scan, list, info, pull, import, remove, plan |
| deploy | 7 | apply, run, dry_run, approve, delete, status, list, logs |
| knowledge | 18 | resolve, search, save, generate_pod, list_profiles, list_engines, list_models, search_configs, compare, similar, lineage, gaps, aggregate, promote, export, import, list, validate |
| benchmark | 4 | record, run, matrix, list |
| stack | 3 | preflight, init, status |
| agent | 5 | ask, status, guide, rollback_list, rollback |
| patrol | 4 | patrol_status, alerts, patrol_config, patrol_actions |
| tuning | 4 | start, status, stop, results |
| explore | 4 | start, status, stop, result |
| fleet | 4 | list_devices, device_info, device_tools, exec_tool |
| app | 3 | register, provision, list |
| system | 2 | status, config |
| openclaw | 2 | sync, status |
| scenario | 3 | list, show, apply |
| discover | 1 | lan |
| shell | 1 | exec |
| device | 2 | power_history, power_mode |
| catalog | 3 | override, status, validate |
| knowledge sync | 3 | sync_push, sync_pull, sync_status |
| support | 1 | askforhelp |
| engine switch | 1 | engine_switch_cost |
| open questions | 1 | open_questions |

### Profile 机制

AIMA 已有工具分层暴露机制：

```go
ProfileFull     // 全部工具（默认）
ProfileOperator // 日常运维子集（~50 tools）
ProfilePatrol   // 巡检最小集
ProfileExplorer // 探索/调优集
```

## 4. 实现方案

### 4.1 新增 `aima mcp` CLI 命令

**目的**：为 OpenClaw 提供 stdio MCP 入口

**新文件**: `internal/cli/mcp.go`

```go
// 命令用法
aima mcp                     // stdio MCP server, 全部工具
aima mcp --profile operator  // 仅 operator 子集
```

**实现要点**：

1. 复用现有初始化链：HAL detect → catalog load → SQLite open → buildToolDeps()
2. 构建 MCP server, 注册工具
3. 调用 `server.ServeStdio(ctx)` — 阻塞直到 stdin 关闭
4. **不启动** HTTP proxy — 纯 stdio 模式
5. stdio 模式下无 proxy server，部分依赖 proxy backends 的工具需 graceful degradation

**修改文件**: `cmd/aima/main.go`
- 注册 `mcp` 子命令
- 提供轻量初始化路径（无 proxy, 无 fleet, 无 support）

**设计约束**：
- stdio 模式读 SQLite 获取部署状态，不依赖运行中的 proxy
- 如 AIMA serve 已在运行，两者共享 SQLite（`MaxOpenConns(1)` 无碍，MCP 调用是串行的）
- API key 通过环境变量 `AIMA_API_KEY` 传入（OpenClaw 的 `env` 字段支持）

### 4.2 扩展 `openclaw sync` 注册 MCP Server

**目的**：sync 时自动把 AIMA 注册到 OpenClaw 的 `mcp.servers`

**修改文件**:
- `internal/openclaw/managed.go` — ManagedState 增加 MCP 字段
- `internal/openclaw/config.go` — 增加 MCP server 注册/清理函数
- `internal/openclaw/sync.go` — Sync() 流程增加 MCP 注册步骤

**ManagedState 扩展**：

```go
type ManagedState struct {
    // ... 现有字段
    McpServerName string `json:"mcp_server_name,omitempty"` // 注册到 mcp.servers 的 key
}
```

**配置写入逻辑**：

```go
// 在 MergeAIMAConfigWithState 中增加:
func mergeMcpServer(cfg map[string]any, managed, next *ManagedState, result *SyncResult) {
    servers := ensureMap(ensureMap(cfg, "mcp"), "servers")

    if hasReadyBackends(result) {
        // 注册 AIMA MCP server (stdio 模式)
        serverConfig := map[string]any{
            "command": "aima",
            "args":    []any{"mcp"},
        }
        if result.APIKey != "" {
            serverConfig["env"] = map[string]any{
                "AIMA_API_KEY": result.APIKey,
            }
        }
        servers[aimaMcpServerName] = serverConfig
        next.McpServerName = aimaMcpServerName
    } else if managed != nil && managed.McpServerName != "" {
        // 无 ready backends → 清理 MCP server
        delete(servers, managed.McpServerName)
    }
}
```

**所有权保护**：
- 如果 `mcp.servers.aima` 已存在且不是 AIMA managed 的（ManagedState 中无记录），不覆盖
- 与现有 LLM provider 所有权逻辑一致

**清理逻辑**：
- 当所有 backends 下线时，删除 `mcp.servers.aima`
- 与现有 provider 清理逻辑对称

### 4.3 扩展 `openclaw status` 报告 MCP 状态

**修改文件**: `internal/openclaw/status.go`

**Status 结构体扩展**：

```go
type Status struct {
    // ... 现有字段
    McpRegistered bool   `json:"mcp_registered"`
    McpServerName string `json:"mcp_server_name,omitempty"`
}
```

**Inspect() 增加检查**：
- 检查 `mcp.servers.aima` 是否存在且配置正确
- 在 Issues 中报告 MCP 未注册情况

### 4.4 部署 AIMA 能力指南 Skill

**新文件**: `internal/openclaw/skills/aima-control/SKILL.md`

**目的**：帮助 OpenClaw Agent 理解 AIMA 工具的使用方式

**内容概要**：

```markdown
# AIMA Device Control

AIMA exposes 80+ MCP tools for AI inference device management.
Tools are auto-discovered via MCP protocol — use tools/list to see all available.

## Quick Reference Workflows

### Deploy a model
1. hardware.detect → 识别设备 GPU
2. knowledge.resolve → 获取推荐配置
3. deploy.run → 一键部署（自动拉取引擎+模型）
4. deploy.status → 检查部署状态
5. openclaw.sync → 同步到 OpenClaw providers

### Check device status
1. system.status → 整体状态
2. hardware.metrics → GPU 利用率/温度/显存
3. deploy.list → 运行中的部署

### Run performance test
1. benchmark.run → 执行基准测试
2. benchmark.list → 查看历史结果

### Manage models
1. model.list → 已下载的模型
2. model.pull → 下载新模型
3. engine.list → 已安装的引擎
4. engine.pull → 拉取引擎
```

### 4.5 SyncResult 扩展

**修改文件**: `internal/openclaw/sync.go`

SyncResult 增加 MCP 信息：

```go
type SyncResult struct {
    // ... 现有字段
    McpServer *McpServerEntry `json:"mcpServer,omitempty"`
}

type McpServerEntry struct {
    Name    string         `json:"name"`
    Config  map[string]any `json:"config"`
}
```

## 5. 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/cli/mcp.go` | **新增** | `aima mcp` CLI 命令（~50 行） |
| `cmd/aima/main.go` | 修改 | 注册 mcp 命令, 轻量初始化路径 |
| `internal/openclaw/managed.go` | 修改 | ManagedState 增加 McpServerName 字段 |
| `internal/openclaw/config.go` | 修改 | 增加 mergeMcpServer() 和清理逻辑（~40 行） |
| `internal/openclaw/sync.go` | 修改 | Sync() 和 SyncResult 增加 MCP 步骤 |
| `internal/openclaw/status.go` | 修改 | Status + Inspect() 增加 MCP 检查（~15 行） |
| `internal/openclaw/sync_test.go` | 修改 | MCP 注册/清理测试用例 |
| `internal/openclaw/status_test.go` | 修改 | MCP 状态检查测试 |
| `internal/openclaw/skills/aima-control/SKILL.md` | **新增** | 能力指南 Skill |

**预估代码量**: ~200 行 Go 代码变更 + ~60 行测试 + ~40 行 Skill markdown

## 6. 数据流全景

```
┌─────────────────────────────────────────────────────────────────┐
│                        AIMA serve                               │
│                                                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────────┐  │
│  │ Proxy    │  │ MCP HTTP │  │ SQLite   │  │ Knowledge     │  │
│  │ :6188    │  │ :9090    │  │          │  │ YAML Catalog  │  │
│  └────┬─────┘  └──────────┘  └────┬─────┘  └───────┬───────┘  │
│       │                           │                 │          │
│  ┌────┴─────────────────────┐     │                 │          │
│  │  80+ MCP Tools           │─────┴─────────────────┘          │
│  │  (buildToolDeps)         │                                  │
│  └────┬─────────────────────┘                                  │
│       │                                                        │
│  ┌────┴──────────────────┐                                     │
│  │  openclaw sync        │                                     │
│  │  ├─ providers (现有)  │──→ openclaw.json models.providers   │
│  │  ├─ media/tts (现有)  │──→ openclaw.json tools/messages     │
│  │  ├─ skills (现有)     │──→ ~/.openclaw/skills/              │
│  │  └─ MCP server (新增) │──→ openclaw.json mcp.servers.aima   │
│  └───────────────────────┘                                     │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     OpenClaw Gateway                            │
│                                                                 │
│  openclaw.json                                                  │
│  ├─ models.providers.aima  → 推理服务（LLM/VLM/ASR/TTS/IMG）  │
│  ├─ tools.media.*          → 多模态工具                        │
│  ├─ mcp.servers.aima       → AIMA MCP Server ──┐              │
│  └─ ...                                        │              │
│                                                 ▼              │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  OpenClaw Agent Runtime                              │      │
│  │  ├─ 读取 mcp.servers.aima                           │      │
│  │  ├─ spawn: aima mcp (stdio JSON-RPC)                │      │
│  │  ├─ tools/list → 发现 80+ AIMA tools                │      │
│  │  └─ tools/call → 调用: deploy.run, model.pull, ...  │      │
│  └──────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────┘
```

## 7. 验证方案

### 单元测试

```bash
go test ./internal/openclaw/... -v -run TestMcp
```

覆盖场景：
- MCP server 注册到 config（有 backends 时）
- MCP server 清理（无 backends 时）
- 不覆盖用户手动配置的 MCP server
- ManagedState 序列化/反序列化包含 McpServerName
- Status.McpRegistered 正确反映配置状态

### 集成验证

```bash
# 1. stdio MCP 基本通信
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | aima mcp
# 期望: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05",...}}

echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | aima mcp
# 期望: 80+ tools 列表

# 2. sync 注册 MCP
aima openclaw sync --dry-run
# 检查输出包含 mcpServer 字段

aima openclaw sync
cat ~/.openclaw/openclaw.json | python3 -c "
import sys, json
cfg = json.load(sys.stdin)
print(json.dumps(cfg.get('mcp', {}), indent=2))
"
# 期望: {"servers": {"aima": {"command": "aima", "args": ["mcp"]}}}

# 3. OpenClaw 验证
openclaw mcp list
# 期望: 显示 aima server

# 4. status 检查
aima openclaw status
# 期望: mcp_registered: true, mcp_server_name: "aima"
```

### 远程设备端到端测试

```bash
# 在 gb10/aima-spark 上:
# 1. 编译 + 部署
GOOS=linux GOARCH=arm64 go build -o build/aima-linux-arm64 ./cmd/aima
scp build/aima-linux-arm64 qujing@100.105.58.16:~/aima

# 2. 启动 serve
ssh qujing@100.105.58.16 './aima serve --mcp &'

# 3. sync + 验证
ssh qujing@100.105.58.16 './aima openclaw sync'
ssh qujing@100.105.58.16 'cat ~/.openclaw/openclaw.json | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin).get(\"mcp\",{}), indent=2))"'
ssh qujing@100.105.58.16 'openclaw mcp list'
```

## 8. 后续演进

### 短期优化（v0.2.x）
- **Streamable HTTP MCP**: 升级 AIMA HTTP MCP 为标准 Streamable HTTP 传输，支持长连接
- **工具描述增强**: 为每个工具添加更详细的中文描述和使用示例
- **Profile 自动选择**: 根据 OpenClaw Agent 角色自动选择合适的 Profile

### 中期能力（v0.3.x）
- **双向事件通知**: AIMA 部署状态变化时主动通知 OpenClaw（MCP notifications）
- **Fleet MCP 聚合**: 通过 fleet MCP 代理多台 AIMA 设备，OpenClaw 统一管理
- **OpenClaw Agent Skill 自动生成**: 根据 AIMA 工具清单自动生成 OpenClaw skill 文档

## 9. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| stdio MCP 无 proxy 实时状态 | 部分工具返回不完整数据 | 工具读 SQLite 获取持久化状态，graceful degradation |
| OpenClaw 运行时不支持 HTTP MCP | HTTP 模式不可用 | 默认用 stdio，HTTP 作为可选增强 |
| aima binary 不在 PATH 中 | OpenClaw spawn 失败 | config 中用绝对路径, sync 时检测 binary 位置 |
| SQLite 并发冲突 | stdio MCP + serve 同时写 | SQLite WAL 模式 + MaxOpenConns(1) 保证串行 |
| OpenClaw 升级改变 MCP 配置格式 | sync 写入无效配置 | 配置格式简单稳定（command/args），低风险 |
