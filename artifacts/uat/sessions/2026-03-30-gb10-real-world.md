# gb10 Real-World UAT Session

**机器**：`gb10`  
**日期**：2026-03-30  
**构建**：`v0.2-dev` / commit `77f815a`  
**二进制**：`~/aima-uat`  
**数据目录**：`~/aima-uat-data`  
**硬件摘要**：NVIDIA GB10，`docker` 可用，`k3s` 不可用  

## 覆盖场景

- `UAT-RW-01` 指定版本引擎下载
- `UAT-RW-02` 一条命令从空目录跑起模型
- `UAT-RW-04` 大模型下载不稳定网络恢复

## 结果概览

- `engine info vllm-blackwell`：PASS
- `engine pull vllm-blackwell`：PASS
- `run qwen3-4b`：BLOCKED

## 关键观察

### 1. 版本化引擎下载链路可以工作

`engine info vllm-blackwell` 能正确解析到：

- 引擎资产：`vllm-blackwell`
- 版本：`26.01`
- 镜像：`nvcr.io/nvidia/vllm:26.01-py3`

`aima engine pull vllm-blackwell` 的结果是：

- AIMA 识别到该镜像已经在 Docker 本地
- 命令直接返回成功
- 没有要求用户再做额外导入动作

### 2. `aima run qwen3-4b` 能进入自动模型下载，但没能在可接受时间内完成

这条链路的前半段是通的：

- 自动解析为 `vllm` + `docker`
- 自动识别引擎已就绪
- 自动开始下载 `qwen3-4b`

### 3. Hugging Face 大文件下载不稳定，自动恢复体验不足

运行过程中出现了多次真实网络异常：

- `cas-bridge.xethub.hf.co` read timeout
- SSL EOF
- `ChunkedEncodingError`

随后 AIMA 确实尝试切换到 ModelScope，但真实表现是：

- 重新进入一轮新的多文件下载
- 没有形成“用户可感知的顺滑续传”
- 超过 15 分钟后仍未进入部署阶段
- 期间没有任何容器被真正拉起

### 4. 还有一个次级噪音问题

下载开始时出现 Hugging Face cache 权限告警：

- `Ignored error while writing commit hash ... Permission denied`

它没有立即打断链路，但会让用户对环境状态失去信心。

## 本轮结论

`gb10` 上已经证明：

- 指定版本引擎下载链路可以成立
- `aima run` 前半段自动化已经接上

但模型大文件下载恢复能力还不足，导致整条一键链路没能在本轮 UAT 里真正到达 ready。
