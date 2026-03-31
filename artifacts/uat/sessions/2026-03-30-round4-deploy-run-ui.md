# Round-4 Deploy-Run-UI Session

**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `11f01e8`  
**策略**：按 `CLAUDE.md` 先 barrier、同构建分发、真实执行、统一记录，不做代码修复。  
**范围**：已有 knowledge + 当前机器已有资产；重点看 `deploy`、`run --no-pull`、`serve/ui`。

## 执行逻辑

- 先确认设备 reachability，再把同一 commit 的二进制分发到可达机器独立 UAT 路径。
- 第一层执行 `hal detect`、`engine plan`、`model scan`、`deploy list`，明确每台机器的候选部署对象。
- 第二层统一执行 `deploy --dry-run`，确认 knowledge 解析出的 engine/runtime 路径。
- 第三层统一执行真实 `deploy`、`deploy status`、`run --no-pull`。
- 最后起两个真实 UI 样本：
- `dev-mac`：失败态样本。
- `gb10`：ready 态样本，并补一个移动视口检查。

## 可达性

| 设备 | 结果 | 说明 |
| --- | --- | --- |
| `dev-mac` | REACHABLE | 本机直接执行 |
| `test-win` | REACHABLE | Windows 11 / RTX 4060 Laptop |
| `gb10` | REACHABLE | Ubuntu / GB10 |
| `linux-1` | REACHABLE | Ubuntu / 2×4090 |
| `amd395` | REACHABLE | Ubuntu / AMD Strix Halo |
| `m1000` | REACHABLE | Ubuntu / Moore Threads M1000 |
| `aibook` | REACHABLE | Ubuntu / M1000 SoC |
| `hygon` | UNREACHABLE | SSH `Permission denied (publickey)` |
| `metax-n260` | UNREACHABLE | Tailscale 地址超时 |
| `qjq2` | UNREACHABLE | 当前环境缺少可用 SSH 入口/别名 |

## 基线与真实命令矩阵

| 设备 | 候选模型 | `deploy --dry-run` | `deploy` | `deploy status` | `run --no-pull` | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| `dev-mac` | `qwen3-4b` | PASS，解析到 `llamacpp/native` | FAIL，真实起进程但 llama-server 直接退出 | FAIL，`phase=failed`，日志里是 `error: invalid argument: --quantization` | FAIL，最终只返回空的 `deployment failed:` | native llama.cpp 参数链路不成立，且 `run` 丢失失败细节 |
| `test-win` | `qwen3-8b` | PASS，解析到 `llamacpp/native` | PARTIAL，直接报 `deployment already running` | PASS，已有实例 `ready=true` | PASS，直接返回现有 endpoint `http://127.0.0.1:8080` | `run` 复用语义成立，但 `deploy` 不是幂等入口 |
| `gb10` | `qwen3-8b` | PASS，解析到 `vllm/docker` | PASS，先走 CDI 失败后自动回退 `--gpus all` 并成功启动 | PASS，`ready=true` | PASS，直接返回 endpoint `http://127.0.0.1:8000` | 当前最完整的正向样本 |
| `linux-1` | `qwen3-4b` | PASS，解析到 `vllm/docker`，并提示 K3S 镜像需回退 Docker | PASS，但先触发一次缺失分片补拉 | PASS，`ready=true` | PASS，直接返回 endpoint `http://127.0.0.1:8000` | 真实链路闭环；已有资产会被补齐，但不是整仓重下 |
| `amd395` | `qwen3-30b-a3b` | PASS，解析到 `llamacpp/native` | PARTIAL，直接报 `deployment already running` | FAIL，旧实例长期 `phase=running, ready=false`，日志实际是 `couldn't bind HTTP server socket` | FAIL，观察窗口内持续等待，不返回 endpoint，也不收敛成失败 | 状态机仍把已失败实例当成“还在启动” |
| `m1000` | `qwen3-30b-a3b` | PASS，解析到 `vllm/native` | PARTIAL，直接报 `deployment already running` | PASS，已有实例 `ready=true` | PASS，直接返回 endpoint `http://127.0.0.1:8000` | ready 复用链路成立，`deploy` 仍非幂等 |
| `aibook` | `qwen3-8b` | PASS，解析到 `vllm/native` | FAIL，真实起 native vLLM 后进程退出 | FAIL，`phase=failed`，错误是 `Cannot find the config file for gptq` | FAIL，重新部署后仍失败，最后只返回空的 `deployment failed:` | 现有 GPTQ 模型目录仍不能被 native vLLM-MUSA 正确接住 |

## 关键观察

- 本轮 `develop` 上，`deploy --dry-run` 在 7 台可达机器全部能给出自洽计划，没有再出现“解析阶段就互相打架”的问题。
- `deploy` 的真实执行结果已经明显分层：
- 正向闭环：`gb10`、`linux-1`
- 复用型闭环：`test-win`、`m1000`
- 明确失败：`dev-mac`、`aibook`
- 长期悬空：`amd395`
- `linux-1` 这次没有重下整仓，而是只补缺失的大分片后就继续部署；这比之前的“整仓重拉”更接近用户预期。
- `gb10` 这次 Docker CDI 失败后能自动回退 `--gpus all`，真实部署和 `run --no-pull` 都闭环到 ready。
- `run --no-pull` 在 ready 实例上的语义已经成立：
- `test-win`、`gb10`、`linux-1`、`m1000` 都直接返回了可用 endpoint。
- `deploy` 自身仍然不是幂等入口：
- 对 `test-win`、`amd395`、`m1000` 这类已有实例，直接返回底层 `already running`。
- 失败态的用户反馈仍偏弱：
- `dev-mac` 与 `aibook` 的 `run --no-pull` 最终都只剩下空的 `deployment failed:`，真正错误只在 `deploy status/logs` 里。

## UI 样本核验

### `dev-mac` 失败态样本

- `aima serve` 本地起在 `127.0.0.1:16189` 后，`/ui/` 可正常打开。
- Deployments 面板能看到 `qwen3-4b` 的 `native failed` 状态，并把 `error: invalid argument: --quantization` 作为失败细节展示出来。
- 页面控制台唯一错误是 `favicon.ico` 404，不影响主流程。

### `gb10` ready 态样本

- 通过 SSH 转发访问 `127.0.0.1:16188/ui/`，页面可正常加载。
- Deployments 面板同时显示：
- `qwen3-8b-vllm` 为 ready，且直接显示 `127.0.0.1:8000`
- 旧的 `qwen3-coder-next-fp8-vllm-nightly` 为 failed
- 这说明 UI 能同时承接 healthy 与 failed 状态，不会只显示一种乐观结果。
- 移动视口 (`390x844`) 下，底部 tab 导航能工作，Dashboard 也能打开；但 hardware/engines/models 卡片很长，导致 deployment 状态不在首屏可见区域，核心任务信号不够靠前。

## 本轮结论

- 当前 `develop` 上，`deploy/run` 的核心闭环已经在 `gb10`、`linux-1`、`test-win`、`m1000` 上拿到真实正向结果，不再是“全线不可信”。
- 但它还没有达到“已有 knowledge 场景整体放心”的程度，主要阻塞点仍是：
- `dev-mac` 的 native llama.cpp 启动参数错误
- `amd395` 的失败状态不收敛
- `aibook` 的 GPTQ 路径无法被 native vLLM-MUSA 正确接住
- UI 整体可进入、能反映真状态，桌面态基本合格；移动态的信息层级还不够偏向“先看部署是否好坏”。
- 本轮只记录，不修复。问题已归并到 `artifacts/uat/issues/BUG-ROUND4-DEPLOY-RUN-UI-20260330.md`。
