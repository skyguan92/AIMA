# Round-2 Repair Validation

**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `a596c3f`  
**目标**：针对 Round-2 汇总 bug 做最小实机回归，确认关键链路是否已经推进到下一阶段。

## linux-1

### 验证点 1：`engine pull vllm-ada`

结果：PASS

关键信号：

- 镜像只在 Docker 中、不在 K3S containerd 中时，命令不再直接失败
- CLI 明确提示：Docker runtime 可以直接使用该镜像

### 验证点 2：`deploy qwen3-4b --dry-run`

结果：PASS

关键信号：

- 输出中的 `runtime` 已从旧路径的 `k3s` 变为 `docker`
- 同时保留 warning，解释为什么发生了 runtime 回退

### 验证点 3：`run qwen3-4b`

结果：PASS（阶段性）

关键信号：

- 链路已越过旧的引擎阻塞点
- 进入 `Checking model qwen3-4b...`
- 开始真实模型下载

## gb10

### 验证点 1：`run qwen3-4b` 在已有部分下载数据下重试

结果：PASS（阶段性）

关键信号：

- 不再直接回到 `huggingface-cli` / `modelscope` 的新一轮全量下载
- 日志明确显示 `resuming HuggingFace download via HTTP`
- 已完成文件被 `skipping already downloaded`
- 只继续下载缺失的大分片文件

## 当前结论

- `BUG-R2-001`：已修复
- `BUG-R2-002`：已修复
- `BUG-R2-003`：代码已修复，未在实机上重新打到同一失败点
- `BUG-R2-004`：已明显缓解，但还需要一次完整长时 UAT 来确认最终能稳定到 ready
- `BUG-R2-005`：已修复到“可感知恢复”层面
- `NOTICE-R2-001`：在本轮 fresh run 中未再观察到同类 Hugging Face cache 权限告警
