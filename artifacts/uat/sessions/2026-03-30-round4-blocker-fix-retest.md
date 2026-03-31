# Round-4 Blocker Fix Retest

**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `9a527bf`  
**范围**：只复测 round-4 中 3 个功能阻塞点：`dev-mac`、`amd395`、`aibook`。  

## 本轮代码修复

- 运行时不再把 `quantization` 无差别展开成 CLI flag：
- `llama.cpp + GGUF` 路径会跳过 `--quantization`
- 本地 `config.json` 不声明 `quantization_config` 时，不再强塞 `--quantization`
- native deployment 元数据状态判定增强：
- `metaToStatus` 不再被“同端口的别的进程”误判成 running
- `Deploy()` 遇到 stale metadata 时会清理并重新发起部署
- `run --no-pull` 失败摘要增强：
- 失败时会优先回显 `message/startup_message`
- 若为空或过于泛化，则回退到 `error_lines` 中最相关的一行

## 本地验证

- `go test ./internal/knowledge ./internal/runtime`：PASS
- `go test ./...`：PASS
- `go test ./cmd/aima`：PASS
- `go build ./cmd/aima`：PASS

## 真实复测

| 设备 | 命令 | 结果 | 结论 |
| --- | --- | --- | --- |
| `dev-mac` | `build/aima-darwin-arm64 deploy qwen3-4b` | PASS，启动命令里已无 `--quantization` | 原始 blocker 已修复 |
| `dev-mac` | `build/aima-darwin-arm64 run qwen3-4b --no-pull` | PASS，返回 `http://127.0.0.1:8080` | native llama.cpp 真正闭环 |
| `amd395` | `~/aima-fix-9a527bf deploy status qwen3-30b-a3b` | PASS，直接显示 `phase=failed`，`message=deployment metadata is stale; port is in use by another process` | 原始“running, ready=false 长期悬空” blocker 已修复 |
| `amd395` | `~/aima-fix-9a527bf deploy qwen3-30b-a3b` | PASS，不再直接报 `already running`，会重新发起 native deploy | stale metadata 清理生效 |
| `amd395` | `~/aima-fix-9a527bf run qwen3-30b-a3b --no-pull` | FAIL，但会快速收敛并回显 `main: exiting due to HTTP server error` | 原始“无限等 + 空报错”已改善为明确失败 |
| `aibook` | `~/aima-fix-9a527bf deploy qwen3-8b` | PASS，启动命令里已无 `--quantization gptq` | 原始 GPTQ 参数 blocker 已修复 |
| `aibook` | `~/aima-fix-9a527bf deploy status qwen3-8b-vllm` | FAIL，但失败原因已变为权重/资源层问题，不再是 `Cannot find the config file for gptq` | 原始 blocker 已转化为新现场问题 |

## 新的现场结论

- `dev-mac`：`qwen3-4b/llamacpp/native` 已恢复正常。
- `amd395`：状态机修复完成，但现场仍有别的进程占用 `8080`，所以真实部署仍失败；现在用户能直接看到明确错误。
- `aibook`：GPTQ 参数问题已消失，真实失败已推进到更深层的现场问题：
- 一次复测中看到 `ValueError: Free memory on device ... is less than desired GPU memory utilization`
- 后续复测中又看到 `KeyError: 'layers.18.mlp.down_proj.g_idx'`
- 这说明当前阻塞已不再是 AIMA 把模型目录“接错了”，而是预装模型内容/显存占用本身还不稳定
