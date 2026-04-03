# Round-4 dev-mac Detached Launch Re-retest

**日期**：2026-03-30  
**构建**：`v0.2-dev` / `develop` worktree 本地构建（含未提交修复）  
**目标**：复测 `BUG-R4-DRU-007`，确认 `dev-mac` 的 native `llama.cpp` 是否仍会在 `run` 返回 ready 后随 shell/session 结束而退出。  

## 本轮修复

- Unix native launch 改为独立 session / 进程组启动。
- native deployment metadata 新增 `process_group_id`。
- `undeploy` / native delete 优先按进程组清理，避免只杀根进程留下 worker/子进程。

## 自动化验证

- `go test ./internal/runtime`：PASS
- 新增回归覆盖：
  - `TestNativeDeployCreatesDetachedProcessGroup`
  - `TestNativeDeleteKillsDetachedProcessGroupChildren`
- `go test ./...`：PASS
- `go build ./cmd/aima`：PASS

## 本机实测

### 方法

- 使用隔离 `AIMA_DATA_DIR`
- 复用本机已存在的：
  - `~/.aima/models/qwen3-4b`
  - `~/.aima/dist/darwin-arm64/llama-server`
- 执行：`run qwen3-4b --no-pull`
- 等命令返回 ready 后，让原 shell/session 结束
- 再从全新 CLI 进程执行 `deploy status qwen3-4b`

### 结果

- `run qwen3-4b --no-pull`：PASS
  - 返回 `Endpoint: http://127.0.0.1:8080`
- 原 shell 仍存活时，`ps` 观察到：
  - `ppid=1`
  - `pgid=pid`
  - 进程状态为 `Ss`
  - `llama-server` 持续监听 `8080`
- 原 shell 结束后，从新 CLI 进程执行 `deploy status qwen3-4b`：
  - 返回 `phase=running`
  - 返回 `ready=true`
- 再查 `ps` / `lsof`：
  - 进程仍在
  - 端口仍在监听

## 结论

- `BUG-R4-DRU-007` 在当前 worktree 上已修复。
- 之前的失败现象不是模型自身在 warmup 后立刻崩溃，而是 native 进程没有脱离原 shell/session，导致 session 结束后服务随之消失。
- 现版本下，`dev-mac` 的 `qwen3-4b/llamacpp/native` 已能在 `run` 返回后继续存活，并可被后续 `deploy status` 正确识别为 `running/ready=true`。
