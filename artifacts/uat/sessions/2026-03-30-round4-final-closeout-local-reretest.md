# Round-4 Final Closeout Local Re-retest

**日期**：2026-03-30  
**构建**：`v0.2-dev` / `develop` worktree 本地构建（含未提交修复）  
**目标**：收掉 Round-4 剩余 active bug：`BUG-R4-DRU-005`、`006`、`008`、`009`、`010`。  

## 本轮修复

- 本地 `safetensors` 目录复用判定继续收紧：
  - 必须有可读 `config.json`
  - 必须有可读权重分片，坏 symlink 不再算“可用模型目录”
  - 必须带可用 tokenizer 资产，不再把半下载目录当成可部署模型
- 量化兼容判断继续收紧：
  - 量化 safetensors 现在要求有明确量化元数据
  - 新增对 `quantize_config.json` / `quantization_config.json` / `quant_config.json` 的 GPTQ/INT4 检测
  - 目录名本身也纳入量化识别（如 `gptq-Qwen3-8B`、`Qwen3-30B-A3B-GPTQ-Int4`）
- 本地兼容资产发现新增 alias 匹配：
  - `findModelDir` 不再只找完全同名目录
  - 现在会把兼容 alias 本地目录当成候选，并继续套用格式/量化兼容检查
- `deploy` 对已有 deployment 改为幂等入口：
  - ready 实例直接回返回现有 endpoint
  - starting/running 实例直接复用当前 deployment，而不是再抛 `already running`
- 移动端 Dashboard 首屏重排：
  - `Deployments` 提升到 mobile Dashboard 顶部
  - 保留 ready / starting / failed 的细节、进度条和失败摘要

## 自动化验证

- 新增/增强回归覆盖：
  - `TestPathLooksUsableSafetensorsRequiresTokenizerAssets`
  - `TestPathLooksUsableSafetensorsRejectsBrokenShardSymlink`
  - `TestPathLooksCompatibleRequiresExplicitSafetensorsQuantizationMetadata`
  - `TestFindModelDirPrefersCompatibleAliasDirectory`
  - `TestFindExistingDeploymentFallsBackToLabelMatch`
- `go test ./internal/model ./internal/knowledge ./cmd/aima`：PASS
- `go test ./...`：PASS
- `go build ./cmd/aima`：PASS

## UI 实测

- 本机起 `serve --addr 127.0.0.1:6191`
- Playwright `390x844` 视口打开 `/ui/`
- 点击 `Dashboard` 后，首个面板已变为 `Deployments`
- 证据：
  - snapshot：`output/playwright/mobile-dashboard-deployments-priority-20260330.yml`
  - screenshot：`output/playwright/mobile-dashboard-deployments-priority-20260330.png`
- 控制台日志：`output/playwright/mobile-dashboard-console-20260330.log`
- 控制台唯一错误仍是 `favicon.ico` 404，无新的前端脚本异常

## 对剩余 bug 的关闭结论

- `BUG-R4-DRU-005`：已关闭  
  `deploy` 对 ready / starting 实例已改为复用语义，不再把“已有实例”直接折成底层 `already running`。

- `BUG-R4-DRU-006`：已关闭  
  移动端 Dashboard 首屏现在优先暴露 `Deployments`，不再先被 `Hardware / Engines / Models` 占满。

- `BUG-R4-DRU-008`：已关闭  
  `m1000` 这类半下载 safetensors 目录现在会在部署前被拒绝；同机兼容 alias 目录会被识别出来并优先复用。

- `BUG-R4-DRU-009`：已关闭  
  `aibook` 这类“目录结构像 safetensors，但量化元数据不成立”的本地目录现在不会再被当成 GPTQ 可部署模型；兼容的 `gptq-*` alias 目录会被重新纳入候选。

- `BUG-R4-DRU-010`：已关闭  
  由于无效本地模型目录会在 deploy 前被及时拦下，`run --no-pull` 不再需要等到深层 engine init 失败后才把错误带回用户。

## 说明

- 本轮关闭结论基于代码修复 + 本地自动化/浏览器回归。
- 远端实机矩阵本轮没有重新完整跑一遍，因此这份记录代表“当前 worktree 的修复已具备收口条件”，不是新的整机 SSH 轮次报告。
