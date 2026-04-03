# linux-1 Real-World UAT Session

**机器**：`linux-1`  
**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `77f815a`  
**二进制**：`~/aima-uat`  
**数据目录**：`~/aima-uat-data`  
**硬件摘要**：2× RTX 4090 48GB，`docker` 和 `k3s` 均可用  

## 覆盖场景

- `UAT-RW-01` 指定版本引擎下载
- `UAT-RW-02` 一条命令从空目录跑起模型
- `UAT-RW-03` 部分资产已存在时的重试体验

## 结果概览

- `engine info vllm-ada`：PASS
- `engine pull vllm-ada`：FAIL
- `run qwen3-4b`：FAIL

## 关键观察

### 1. 版本解析是对的

`engine info vllm-ada` 能正确解析到：

- 引擎资产：`vllm-ada`
- 版本：`0.8.5`
- 镜像：`vllm/vllm-openai:v0.8.5`

### 2. 首次引擎下载失败，但错误不够可诊断

首次执行 `aima engine pull vllm-ada` 时：

- Docker 里没有 `vllm/vllm-openai:v0.8.5`
- containerd 里也没有
- CLI 最终只返回 `all registries failed: exit status 1`

后续底层排查显示：

- `docker pull vllm/vllm-openai:v0.8.5` 实际可以成功
- `docker pull registry.cn-hangzhou.aliyuncs.com/aima/vllm-openai:v0.8.5` 返回 denied

结论：AIMA 在这一步没有把真正的 registry 失败原因透给用户。

### 3. 镜像已经在 Docker 里时，AIMA 仍不能把它视为“可用引擎”

手动把 `vllm/vllm-openai:v0.8.5` 拉到 Docker 后再次执行 `aima engine pull vllm-ada`，CLI 明确报错：

- 镜像已在 Docker 中
- 但不在 K3S containerd 中
- 需要 root 执行导入

对真实用户来说，这意味着“我已经把版本拉下来了，但 AIMA 仍然说引擎不可用”。

### 4. `aima run qwen3-4b` 被同一个门槛直接拦住

在空模型目录下执行 `aima run qwen3-4b`：

- 成功完成解析
- 成功识别当前选择的是 `vllm` + `k3s`
- 在引擎检查阶段直接失败
- 模型下载和实际部署根本没有开始

## 本轮结论

`linux-1` 上最关键的真实用户问题不是“解析不到版本”，而是：

- K3S 可用时，AIMA 会把引擎可用性绑定到 containerd
- 用户即使已经把目标版本拉进 Docker，也不能顺利继续 `engine pull` 或 `run`
- 一条命令链路因此在第一阶段就断掉
