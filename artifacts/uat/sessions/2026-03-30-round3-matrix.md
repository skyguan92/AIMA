# Round-3 Real-World UAT Matrix

**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `ee66ac1`  
**策略**：先完整收集，再统一归并，只记录新 bug，不做修复。

## 本轮执行逻辑

- 按 `CLAUDE.md` 要求先确认设备可达性，再统一分发同一 commit 的构建产物。
- 可达设备统一执行 Round-3 场景。
- 不可达设备记为 `UNREACHABLE`，不跳过、不脑补。
- 原始命令输出只保留在本地临时目录，本仓库只记录会话结论。

## 可达性结果

| 设备 | 结果 | 说明 |
| --- | --- | --- |
| `dev-mac` | REACHABLE | 本机直接执行 |
| `test-win` | REACHABLE | Windows 11 / Ada |
| `linux-1` | REACHABLE | Ubuntu / 2×4090 Ada |
| `gb10` | REACHABLE | Ubuntu / GB10 Blackwell |
| `amd395` | REACHABLE | Ubuntu / AMD Strix Halo |
| `m1000` | REACHABLE | Ubuntu / Moore Threads M1000 |
| `aibook` | REACHABLE | Ubuntu / M1000 SoC |
| `hygon` | UNREACHABLE | SSH `Permission denied (publickey)` |
| `qjq2` | UNREACHABLE | 代理接入所需主机认证失败 |
| `metax-n260` | UNREACHABLE | Tailscale 地址超时，LAN 地址 host down |

## 场景矩阵

| 设备 | UAT-RW-05 | UAT-RW-06 | UAT-RW-07 | UAT-RW-08 | 结论 |
| --- | --- | --- | --- | --- | --- |
| `dev-mac` | PASS | PASS | PARTIAL | FAIL | 首次运行可进入模型下载，但第二次重试会重新下载同一份 native engine |
| `test-win` | PASS | PASS | PARTIAL | FAIL | 首次运行可进入模型下载，但第二次重试会重新下载同一份 native engine |
| `linux-1` | PASS | PASS | PARTIAL | PASS | 明确引擎准备和重试恢复都成立，第二次进入 HTTP resume 并跳过已完成文件 |
| `gb10` | PASS | PASS | PARTIAL | PASS | 明确引擎准备和重试恢复都成立，第二次进入 HTTP resume 并跳过已完成文件 |
| `amd395` | PASS | PASS | PARTIAL | FAIL | 第二次重试会重新下载同一份 `llama.cpp` native engine，再继续模型恢复 |
| `m1000` | FAIL | FAIL | FAIL | N/A | `plan`/`deploy`/`pull`/`run` 对 MUSA native engine 表达不一致，链路断在引擎阶段 |
| `aibook` | FAIL | FAIL | FAIL | N/A | `plan`/`deploy`/`pull`/`run` 对 MUSA native engine 表达不一致，链路断在引擎阶段 |
| `hygon` | UNREACHABLE | UNREACHABLE | UNREACHABLE | UNREACHABLE | 实验室接入问题，非产品结论 |
| `qjq2` | UNREACHABLE | UNREACHABLE | UNREACHABLE | UNREACHABLE | 实验室接入问题，非产品结论 |
| `metax-n260` | UNREACHABLE | UNREACHABLE | UNREACHABLE | UNREACHABLE | 实验室接入问题，非产品结论 |

## 关键观察

- `linux-1` 和 `gb10` 上，Round-2 修过的主链路继续成立：
- 明确引擎准备已经不再被旧问题卡住。
- 第二次 `run` 会进入 `resuming HuggingFace download via HTTP`，并跳过已完成文件。
- `m1000` 和 `aibook` 上出现新的高优先级断裂：
- `engine plan` 没有暴露 `vllm-musa`。
- `deploy --dry-run` 却解析到 `engine=vllm runtime=native`，且 `engine_image` 变成了 `":"`。
- `engine pull vllm-musa` 与 `run qwen3-8b` 都在引擎阶段报 `has no download source for platform linux/arm64`。
- native 路径出现新的中优先级问题：
- `dev-mac`、`test-win`、`amd395` 的第二次 `run` 都会重新下载同一份 `llama.cpp` 二进制。
- 同一数据目录下，模型恢复成立，但引擎恢复没有成立。

## 本轮输出

- 新场景设计：`artifacts/uat/plan/ROUND-3-REAL-WORLD-SCENARIOS-20260330.md`
- 新问题汇总：`artifacts/uat/issues/BUG-ROUND3-CONSOLIDATED-20260330.md`
