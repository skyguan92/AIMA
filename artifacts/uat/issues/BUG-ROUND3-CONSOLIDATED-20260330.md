# Round-3 Consolidated Bugs

**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `ee66ac1`  
**策略**：本轮只记录新问题，不做修复。

## 高优先级

### BUG-R3-001：MUSA 平台的 native engine 路径前后自相矛盾，导致引擎准备与一键运行都不可用

- **影响机器**：`m1000`、`aibook`
- **影响场景**：`UAT-RW-05`、`UAT-RW-06`、`UAT-RW-07`、`UAT-RW-09`
- **现象**：
- `engine plan` 在 MUSA 机器上没有给出 `vllm-musa`，只显示一组不相干的 container engines。
- `deploy qwen3-8b --dry-run` 却解析到 `engine=vllm runtime=native`，并出现异常的 `engine_image=":"`。
- `engine pull vllm-musa` 直接失败：`engine "vllm-musa" has no download source for platform linux/arm64`。
- `run qwen3-8b` 也在引擎阶段失败：`engine "vllm" has no download source for platform linux/arm64`。
- **用户影响**：MUSA 用户会遇到“看起来能解析，但实际既不能准备引擎，也不能跑起来”的断裂体验。
- **结论**：MUSA native engine 的 `plan`、`deploy`、`pull`、`run` 四个入口没有对齐，当前链路对真实用户不可用。

## 中优先级

### BUG-R3-002：native `run` 的第二次尝试会重新下载同一份 engine binary，无法复用已完成的引擎准备

- **影响机器**：`dev-mac`、`test-win`、`amd395`
- **影响场景**：`UAT-RW-07`、`UAT-RW-08`
- **现象**：
- 第一次 `run` 会把 `llama.cpp` 二进制下载到当前 `AIMA_DATA_DIR/dist/...`，然后进入模型下载。
- 第二次在同一 `AIMA_DATA_DIR` 下再次执行同一 `run`，模型能够进入恢复路径，但引擎阶段仍会重新下载同一份 archive。
- 该现象在 macOS、Windows、Linux native/llamacpp 三条路径上都复现。
- **用户影响**：用户明明已经完成过引擎准备，但第二次重试体感仍然像“重新来过”，浪费时间和带宽。
- **结论**：native engine 的缓存/注册状态没有被 `run` 的重试路径正确复用。

## 本轮不记为产品 bug 的事项

- `linux-1`、`gb10` 的 retry 路径表现符合预期：第二次 `run` 会进入 HTTP resume，并跳过已完成文件。
- `hygon`、`qjq2`、`metax-n260` 本轮是实验室接入失败，不作为产品 bug 计入。
