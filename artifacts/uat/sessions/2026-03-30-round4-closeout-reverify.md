# Round-4 Closeout Re-verify

**日期**：2026-03-30  
**构建**：`v0.2-dev` / 基于 `develop` `9a527bf` 的本地 UAT 构建（含未提交修复）  
**方法**：继续遵循 `CLAUDE.md` 的 `ALL COLLECT, THEN ANALYZE, THEN RE-VERIFY`。本轮先修阻塞点，再对可达机器统一重分发和统一回归。  

## 本轮修复摘要

- `llama.cpp` / 本地 safetensors 路径不再无差别展开 `--quantization`。
- 本地模型目录复用新增量化兼容性判断：
- `resolve` 不再把量化类型不匹配的扫描目录直接塞回 `ModelPath`
- `pull/run` 复用本地目录时不再只看“目录结构像不像模型”，还看量化提示是否匹配
- native runtime 的 warmup 现在会：
- 使用真实 `model` 名发送 warmup 请求
- 只有 warmup 真成功才标记 ready，避免“health 200 但实际是别的服务”的假 ready

## Reachability Barrier

| 设备 | 结果 | 说明 |
| --- | --- | --- |
| `dev-mac` | REACHABLE | 本机 |
| `test-win` | REACHABLE | Windows 11 / RTX 4060 |
| `gb10` | REACHABLE | Ubuntu / GB10 |
| `linux-1` | REACHABLE | Ubuntu / 2×4090 |
| `amd395` | REACHABLE | Ubuntu / AMD Strix Halo |
| `m1000` | REACHABLE | Ubuntu / Moore Threads M1000 |
| `aibook` | REACHABLE | Ubuntu / M1000 SoC |
| `hygon` | UNREACHABLE | `Permission denied (publickey)` |
| `metax-n260` | UNREACHABLE | SSH connect timeout |
| `qjq2` | UNREACHABLE | 当前环境缺少可用 SSH 入口/别名 |

## 最新功能矩阵

| 设备 | `run --no-pull` | `deploy status` | 结论 |
| --- | --- | --- | --- |
| `dev-mac` | PASS，返回 `http://127.0.0.1:8080` | FAIL，稍后收敛成 `native failed` | 部署可起且可响应首个请求，但进程会在随后退出，仍不能视为稳定 ready |
| `test-win` | PASS，直接返回现有 endpoint `http://127.0.0.1:8080` | PASS，`ready=true` | ready 复用链路稳定 |
| `gb10` | PASS，直接返回现有 endpoint `http://127.0.0.1:8000` | PASS，`ready=true` | 正向样本稳定 |
| `linux-1` | PASS，直接返回现有 endpoint `http://127.0.0.1:8000` | PASS，`ready=true` | 正向样本稳定 |
| `amd395` | FAIL，快速回显 `main: exiting due to HTTP server error` | FAIL，明确为 `port is in use by another process` | 原始 blocker 已修成“诚实失败”，现场仍有端口冲突 |
| `m1000` | FAIL，最终回显 `TypeError: expected str, bytes or os.PathLike object, not NoneType` | FAIL，`phase=failed`，且明确是 stale/port reuse + tokenizer 初始化失败 | 假 ready 已消失，真实失败原因被暴露出来 |
| `aibook` | FAIL，观察窗口内 `run` 会话未及时返回，但并行 `deploy status` 已稳定 failed | FAIL，`RuntimeError: Engine core initialization failed` | 不再误用 `/opt/mt-ai/...` 预装目录，但当前本地模型仍在更深层初始化失败 |

## 关键回归结论

- `dev-mac`：
- 旧 blocker `--quantization` 参数错误已修复。
- 现在 `run qwen3-4b --no-pull` 可以真实返回 endpoint。
- 但 `deploy status` 稍后仍会掉回 `failed`，说明本机 llama.cpp 进程稳定性还有后续问题。

- `amd395`：
- 旧 blocker“长期 `running, ready=false` 不收敛”已修复。
- 当前行为是快速、明确地失败，并把端口占用原因带给用户。
- 这轮没有再出现“无限等待 + 空报错”。

- `m1000`：
- 这一轮最关键的变化是：不再假装 ready。
- 修复前，`run` 会误把别的 8000 端口服务当成新 deployment 已 ready。
- 修复后，warmup 404 不再被当成成功，最终 `run` 会收敛到真实失败，并回显 tokenizer 初始化错误。

- `aibook`：
- 这轮已不再复用 `/opt/mt-ai/llm/models/gptq-Qwen3-8B` 这个量化不匹配的预装目录。
- `deploy --dry-run` 和真实 `deploy` 都会打印 `ignoring incompatible scanned model path ... detected_quantization=bf16 expected_quantization=gptq`。
- 失败点已经推进到当前 `~/.aima/models/qwen3-8b` 的更深层 vLLM engine init，而不再是 AIMA 参数链路本身。

## UI 回归

### `dev-mac` 失败态

- 本机 `serve --addr 127.0.0.1:16189` 可正常起服务，`/ui/` 可打开。
- Playwright snapshot 中，`Deployments` 面板明确显示 `qwen3-4b` / `native failed`。
- 控制台唯一错误仍是 `favicon.ico` 404，没有新的前端脚本异常。

### `gb10` ready 态

- 通过 SSH 本地转发访问 `http://127.0.0.1:16188/ui/`，页面正常加载。
- 桌面态 snapshot 中，`Deployments` 面板同时显示：
- `qwen3-8b-vllm` 为 `Ready: 127.0.0.1:8000`
- 旧实例 `qwen3-coder-next-fp8-vllm-nightly` 为 `docker failed`
- 移动视口 `390x844` 下，页面没有明显错位或崩坏，但首屏仍被 `Hardware / Engines / Models` 占据，`Deployments` 不是首屏主信号。

## 本轮结论

- 这轮阻塞修复已经把几条最危险的错误语义修掉了：
- `dev-mac` 的参数链路不再直接错
- `amd395` 不再无限悬空
- `m1000` 不再给出假 ready
- `aibook` 不再误吃量化不匹配的预装目录
- 当前 `develop` 的 deploy/run 可信度明显提升，但还没到“全绿可放心”的程度。
- 还需要继续处理的主要是 3 类剩余问题：
- `dev-mac` 的 native llama.cpp 进程短命
- `m1000` 的本地 qwen3-30b-a3b 目录/入口仍会掉进 tokenizer 初始化失败
- `aibook` 的当前本地 qwen3-8b 路径仍会在 vLLM engine init 深处失败，且 `run --no-pull` 失败回传不够及时
