# Round-2 linux-1 Bug Report

**机器**：`linux-1`  
**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `77f815a`

## BUG-R2-001：K3S 可用时，版本引擎下载被 containerd 导入门槛阻断

- **场景**：用户想下载 `vllm-ada` 这个明确版本的引擎。
- **期望**：AIMA 把目标版本下载完成，并把引擎置为可用。
- **实际**：
  - 首次 `aima engine pull vllm-ada` 失败，只返回 `all registries failed: exit status 1`
  - 手动 `docker pull vllm/vllm-openai:v0.8.5` 成功后，再次执行 `aima engine pull vllm-ada`
  - AIMA 报告镜像只在 Docker 中，不在 K3S containerd 中，要求 root 导入
- **影响**：高。标准用户无法仅通过 AIMA 完成“下载某个版本引擎”这件事。
- **用户视角问题**：用户已经把正确版本拉下来了，但 AIMA 仍然不承认它“可用”。

## BUG-R2-002：`aima run qwen3-4b` 在引擎阶段直接失败，无法进入自动模型下载

- **场景**：空模型目录下一键运行 `qwen3-4b`
- **期望**：自动补齐引擎、模型和部署
- **实际**：
  - 解析成功
  - 识别到 runtime=`k3s`
  - 在“Checking engine vllm...”阶段直接失败
  - 错误与 BUG-R2-001 相同
- **影响**：高。用户最关心的一键链路在第一阶段即中断。

## BUG-R2-003：引擎下载错误信息缺少 per-registry 诊断

- **场景**：首次 `aima engine pull vllm-ada`
- **期望**：告诉用户哪个 registry 成功、哪个失败、是网络问题还是权限问题
- **实际**：CLI 只返回 `all registries failed: exit status 1`
- **补充证据**：
  - Docker Hub 实际可拉取
  - 阿里云镜像地址返回 denied
- **影响**：中。定位成本高，用户无法判断接下来该等、该重试还是该换源。
