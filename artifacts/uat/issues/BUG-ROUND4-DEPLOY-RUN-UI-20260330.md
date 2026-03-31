# Round-4 Deploy-Run-UI Active Bugs

**日期**：2026-03-30  
**构建**：`v0.2-dev` / 基于 `develop` `9a527bf` 的本地 UAT 构建（含未提交修复）  
**范围**：已有 knowledge + 真实已有资产 + `deploy` / `run` / `serve/ui`。  
**状态**：本轮 active bug 已清零。阻塞点修复、后续定点修复和本地 closeout re-retest 已完成。

## 本轮已关闭

- `BUG-R4-DRU-005`：`deploy` 对已有 ready / starting 实例已改为幂等复用入口
- `BUG-R4-DRU-006`：移动端 Dashboard 首屏已把 `Deployments` 提升为第一优先级
- `BUG-R4-DRU-007`：`dev-mac` native Unix launch 已改为 detached session / process group
- `BUG-R4-DRU-008`：半下载 safetensors 目录不再被当成可部署模型；兼容 alias 本地资产可被发现
- `BUG-R4-DRU-009`：量化不明确或污染的 safetensors 目录不再被当成 GPTQ 可部署模型
- `BUG-R4-DRU-010`：`run --no-pull` 对无效本地模型目录会更早失败返回，不再拖到深层 engine init

## 本轮清掉的 blocker

- `dev-mac`：`llama.cpp` 启动命令不再错误携带 `--quantization`
- `amd395`：native 失败实例不再长期卡在 `running, ready=false`
- `run --no-pull`：失败摘要不再只剩空的 `deployment failed:`
- `aibook`：不再误用 `/opt/mt-ai/llm/models/gptq-Qwen3-8B` 这个量化不匹配的预装目录
- `m1000`：native warmup 不再因为别的 8000 端口服务而给出假 ready
- `dev-mac`：Unix native launch 已改为 detached session / process group，`run qwen3-4b --no-pull` 返回后服务不会再随原 shell/session 一起退出。复测见 `sessions/2026-03-30-round4-devmac-detach-reretest.md`

## 关闭依据

- 阻塞点修复复测：`sessions/2026-03-30-round4-blocker-fix-retest.md`
- `dev-mac` detached launch 定点复测：`sessions/2026-03-30-round4-devmac-detach-reretest.md`
- 本地最终 closeout 回归：`sessions/2026-03-30-round4-final-closeout-local-reretest.md`
