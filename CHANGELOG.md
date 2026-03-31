# Changelog

All notable changes to AIMA are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/). Versioning follows [SemVer](https://semver.org/).

## [Unreleased] — develop

55 commits, 217 files changed, +20,848/-6,216 since v0.2.0.

### Added

- **OpenClaw stdio MCP control plane** — bidirectional MCP integration via stdin/stdout transport, replacing HTTP-only flow
- **Scenario startup ordering** — `startup_order` field with `wait_for` (health_check/port_open) and per-step timeouts; `scenario.show` tool for dry-run inspection
- **Engine Profile system** — `catalog/engine-profiles/` for YAML deduplication; engines reference profiles instead of repeating common config
- **Engine download progress + auto-pull** — `engine.pull` streams download progress; `deploy.apply` auto-pulls missing engines
- **`aima run` command** — one-shot deploy+run shortcut for quick model serving
- **Profile-based MCP tool filtering** — reduces agent token overhead by exposing only relevant tools per hardware profile
- **Native engine scanner** — auto-discovers pre-installed native engines and ONNX/MNN model formats on device
- **Windows schtasks GPU deploys** — native runtime support for Windows via scheduled tasks
- **AIBook M1000 SoC knowledge base** — hardware profile, engine configs, and benchmark data for Moore Threads AIBook
- **FunASR ONNX engine** — speech recognition via ONNX runtime with port detection across all runtimes
- **Settings modal redesign** — 4-tab structure (General, LLM, Engine, Advanced) with validation for extras and patrol gaps
- **Support first-level UI page** — dedicated support page with auto-open browser on double-click launch
- **AIMA logo + branding** — app logo in topbar and agent avatar; cross-platform app icons (macOS/Windows/Linux)
- **Catalog validate tool** — `catalog.validate` MCP tool for engine YAML schema integrity checks

### Changed

- **ZeroClaw (L3b) removed** — ~3,400 lines deleted; agent intelligence consolidated into Go Agent (L3a) with simplified status semantics
- **Deployment port allocation** — refactored around startup specs with edge case coverage for port conflicts
- **Native process identity** — preserved across restarts with improved failure detail reporting
- **Support feed isolation** — separated from agent chat to prevent cross-contamination
- **Fleet device ordering** — stabilized for consistent UI display
- **Engine YAML schema normalized** — aligned across all vendors with catalog validate enforcement

### Fixed

- **Scenario readiness checks** — hardened apply flow on partial assets; proper health check and port_open wait strategies
- **Deploy lifecycle status visibility** — correct phase reporting during startup/running/failed transitions
- **GPU-count-aware variant selection** — `gpu_count_min` enforced during model variant resolution
- **Runtime delivery flow** — restored knowledge-driven engine and model delivery; fixed local model reuse and runtime readiness
- **Native deploy for AIBook MUSA engines** — enabled pre-installed engine detection with `work_dir` support
- **Engine profile overlay staleness** — tracked and rebuilt engine assets after profile overlay changes
- **OpenClaw managed ownership** — hardened ownership flow to prevent orphaned processes
- **UI validation** — settings extras, patrol idle gaps, default serve entry, support feedback stability
- **TTS TARGET_DEVICE=cpu** — fixed CPU fallback for TTS engine on non-GPU devices

### Infrastructure

- **Code quality**: `cmd/aima/main.go` split from 6,172 → 3,698 lines (8 domain files); `internal/sqlite.go` split from 2,202 → 332 lines (7 domain files)
- 80 MCP tools, 3 runtimes (K3S, Docker, Native)
- 10 hardware profiles, 24+ engine YAMLs, 18+ model YAMLs, 2 deployment scenarios
- Supported platforms: darwin-arm64, linux-arm64, linux-amd64, windows-amd64

## [v0.2.0] - 2026-03-25 — "Connect the Dots"

36 commits, 108 files changed, 22468 insertions, 1047 deletions since v0.0.1.

### Added

- **Support Service Integration** — `internal/support/` standalone component with self-register, polling, task lifecycle, prompt/notify callbacks, and recovery code flow
- **askforhelp CLI** — interactive terminal UX with invite/worker/recovery code prompts, budget display (USD + task count), referral codes, and foreground wait mode
- **askforhelp MCP tool** — `support.askforhelp` wired via `ToolDeps.SupportAskForHelp`
- **Web UI redesign** — Apple-aesthetic embedded SPA with light/dark mode toggle
- **OpenClaw provider plugin** — LLM/ASR/TTS/image_gen backend integration with reverse proxy discovery
- **Embedded AIMA skills** — multimodal agent tool definitions for OpenClaw
- **Deployment scenarios** — `catalog/scenarios/` asset kind for multi-model deployment recipes (e.g. `openclaw-multi`)
- **Blackwell CUDA TTS engine** — GPU-accelerated TTS for GB10/Blackwell
- **Z-Image model + diffusers engine** — text-to-image support via diffusers backend
- **qwen3.5-9b model asset** — 9B dense model with native multimodal support
- **Hardware ID candidates** — robust device dedup using board serial, product serial, IOPlatformSerialNumber, MAC address
- **In-memory message log** — fixes lost notifications in UI polling

### Changed

- **Support endpoint** — migrated from `http://121.37.119.185/platform` to `https://aimaserver.com/platform`
- **Support wire format** — aligned with latest server API: budget USD fields, bound status, referral count, display language, hardware_id_candidates
- **Support wiring simplified** — 13-line closure in main.go replaced by single `supportSvc.AskForHelpJSON` call
- **Model path resolution** — fixed mismatch between root systemd service and regular user paths

### Fixed

- TTS format mismatch and image understanding config in OpenClaw
- Missing `http://` scheme in backend addresses for reverse proxy
- Agent pipeline: 4 bugs found during live GLM-4.7-Flash validation
- Orphaned explore runs and null-slice JSON responses
- Data races in proxy server and native runtime
- 4 data-integrity issues in knowledge sync/import/export and hardware identity
- Exact engine `metadata.name` preference when resolving variants

### Infrastructure

- 80 MCP tools (unchanged count, improved wiring)
- 3 runtimes: K3S, Docker, Native
- 9 hardware profiles, 22+ engine YAMLs, 16+ model YAMLs, 1 deployment scenario
- Supported platforms: darwin-arm64, linux-arm64, linux-amd64, windows-amd64

## [v0.0.1] - 2026-03-06

Initial tagged release. Foundation layer with hardware detection (8 GPU vendors), multi-runtime deployment, knowledge-driven config resolution, 80 MCP tools, central knowledge server, TUI dashboard, benchmark runner, and exploration runner.

[Unreleased]: https://github.com/Approaching-AI/AIMA/compare/v0.2.0...develop
[v0.2.0]: https://github.com/Approaching-AI/AIMA/compare/v0.0.1...v0.2.0
[v0.0.1]: https://github.com/Approaching-AI/AIMA/releases/tag/v0.0.1
