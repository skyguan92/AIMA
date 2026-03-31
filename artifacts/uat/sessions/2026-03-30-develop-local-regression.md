# develop Local Regression Validation

**日期**：2026-03-30  
**工作树**：`develop` (`/Users/jguan/projects/AIMA/.claude/worktrees/develop-uat-20260330`)  
**基线**：`9a527bf` + 本地未提交修复  
**性质**：本地自动化回归 + CLI smoke。不是远端实机矩阵，不替代 round-4 closeout 结论。  

## 背景

- 当前 `develop` worktree 已包含一组未提交修复，集中在 `cmd/aima/main.go`、`internal/model/downloader.go`、`internal/runtime/native.go` 等 deploy/run 主链路文件。
- 本次验证目标是确认这些修复至少没有破坏 `develop` 的本地自动化覆盖面，并且 CLI 主入口还能给出自洽输出。

## 自动化回归

### 命令

```bash
go test ./internal/model ./internal/runtime ./internal/knowledge ./cmd/aima
go test ./...
go build ./cmd/aima
```

### 结果

- `go test ./internal/model ./internal/runtime ./internal/knowledge ./cmd/aima`：PASS
- `go test ./...`：PASS
- `go build ./cmd/aima`：PASS

## 关键测试覆盖点

以下测试名已存在且包含在本次通过结果内：

- `internal/runtime`
  - `TestMetaToStatusMarksMissingProcessFailed`
  - `TestMetaToStatusMarksStalePortReuseFailed`
  - `TestNativeDeployIgnoresStaleMetadataUsingOccupiedPort`
  - `TestHealthCheckAndWarmupRequiresSuccessfulWarmup`
  - `TestHealthCheckAndWarmupUsesActualModelName`
- `internal/model`
  - `TestPathLooksUsableSafetensorsRequiresAllIndexedShards`
  - `TestPathLooksCompatibleRejectsQuantizationMismatch`
  - `TestPathLooksCompatibleAcceptsGGUFQuantization`
- `cmd/aima`
  - `TestSummarizeDeploymentFailure`
  - `TestVariantQuantizationHint`

## CLI Smoke

使用临时 `AIMA_DATA_DIR` 执行：

```bash
AIMA_DATA_DIR="$tmpdir" go run ./cmd/aima version
AIMA_DATA_DIR="$tmpdir" go run ./cmd/aima engine plan
AIMA_DATA_DIR="$tmpdir" go run ./cmd/aima deploy qwen3-4b --dry-run
```

### 结果

- `version`：PASS，输出 `aima v0.2-dev`
- `engine plan`：PASS，在当前本机环境下稳定返回 `native` 兼容引擎集合
- `deploy qwen3-4b --dry-run`：PASS，稳定解析到 `engine=llamacpp`、`runtime=native`，并返回完整 `fit_report`

## 本次结论

- 当前 `develop` worktree 的本地修复没有破坏全量 Go 自动化测试。
- deploy/run 主链路新增的几类语义修复已有直接测试覆盖：
  - 本地模型目录复用增加量化兼容性判断
  - native stale metadata / 端口复用不再误判
  - warmup 只有真实成功才会标记 ready
  - `run --no-pull` 失败摘要更具体
- 从本机 CLI smoke 看，`engine plan` 和 `deploy --dry-run` 仍能给出自洽结果，没有出现明显的新断裂。

## 边界

- 本次没有重新执行远端实机矩阵。
- `dev-mac` / `amd395` / `m1000` / `aibook` 的 round-4 剩余现场问题，仍应以 `2026-03-30-round4-closeout-reverify.md` 为准。
