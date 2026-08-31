# Code Relay

<div align="center">
  <img src="assets/icon.png" alt="Code Relay Logo" width="96">
  <br>
  <sub>build, verify, and relay</sub>
</div>

![Code Relay workflow](assets/overview.png)

> **Code Relay 是一个让 Codex 在开发机和目标机之间自动接力的跨机器开发验证协作工具。**

## 产品简介

Relay 是面向 Codex 的跨机器开发验证协作工具。开发机上的 AI 负责实现需求，目标机器上的验证环境负责执行真实测试，验证结果以结构化回执返回，并自动推动下一轮修复。

它适合需要跨操作系统、内网、GPU、硬件或生产同构环境验证的开发团队。

Relay 不只是“把命令发到另一台机器执行”，而是把 **开发、发布、真实环境验证、结果回传和失败修复** 串成一个可追踪的闭环。

## 产品逻辑

```text
用户在 Codex 中描述需求
        ↓
Dev Host：AI 开发、提交并合入代码
        ↓
Relay：传递正确的 commit 和 task
        ↓
Target Host：在真实目标环境中执行验证
        ↓
Receipt：回传结构化结果和证据
        ↓
通过 → 完成        失败 → 继续修复        阻塞 → 请求人工决策
```

一次典型任务只需要一句话：

> 完成支付回调幂等改造，合入后让目标机验证；失败时继续修复，直到验证通过。

Codex 负责开发和编排，Relay 负责跨机器传递任务，目标机负责验证。用户看到的是“正在验证 / 验证失败 / 已完成”等阶段性结果，而不是一堆 GitHub Actions 细节。

## 谁会想到使用 Relay

Relay 适合那些经常遇到“本地能写，但必须去另一台机器才能证明”的团队：

- **AI 编程重度用户**：本地没有 GPU，想让 GPU 服务器自动验证模型代码，而不是手动打包、登录和执行。
- **后端开发者**：支付回调在本地无法复现，必须放到有真实数据库、队列和内网依赖的测试环境跑一遍。
- **DevOps / 测试工程师**：每次合并后都要手动通知另一台机器回归测试，容易漏测，也难以追溯。
- **GPU / AI 工程师**：代码在 CPU 上通过了，但最终必须在装好 CUDA 的目标机上验证。
- **硬件、IoT、机器人团队**：只有接着真实设备的机器才能知道串口、摄像头或传感器是否真的可用。
- **内网和企业系统团队**：开发机没有生产同构环境或内网权限，验证必须交给隔离的目标机完成。
- **浏览器自动化团队**：本地浏览器版本与线上不一致，需要在指定环境跑真实用户流程。

## MVP 提供什么

- **分支级绑定**：用 `repository + refs/heads/<branch>` 明确任务属于哪个仓库和分支。
- **自然语言启用**：在 Codex 中请求启用 Relay，自动生成目标机加入链接。
- **短时邀请链接**：`code-relay://join/...` 携带仓库和分支预览，便于安全加入。
- **安全的目标机初始化**：目标机没有绑定项目时，自动克隆获准的仓库；非空目录或其他项目不会被覆盖。
- **GitHub Actions 验证**：将任务路由到带 `codex-b` 标签的 self-hosted runner，由 Go agent 在隔离 worktree 中验证精确 commit。
- **可靠回执**：任务幂等发布、绑定 source commit、校验回执、命令白名单、超时控制，以及失败/阻塞回执。

## 从 Codex 安装

从 Codex 插件界面安装 **Code Relay**。插件直接启动捆绑的 Go `code-relay-agent`，不需要安装 Python 或其他语言运行时。

### Dev Host：绑定当前项目

在 Codex 中打开目标工程并说：

> 为当前工程当前分支启用 Code Relay，并生成 Target Host 加入链接。

插件会展示解析出的远程仓库和分支，写入 `.code-relay/project.json`，添加项目集成文件，提交并推送绑定信息，然后返回一个短时有效的加入链接。

### Target Host：加入并验证

在 Target Host 安装同一个插件，把加入链接粘贴到 Codex 中并确认：

1. 如果工作区为空或尚未绑定，插件会克隆邀请中的仓库和分支。
2. 写入验证端绑定信息，并将目标机注册为带 `codex-b` 标签的 GitHub Actions runner。
3. GitHub Actions 根据 `tasks/**` 变更调度验证任务。
4. Go `code-relay-agent run-pending` 执行验证计划并发布回执。

如果当前目录非空，或属于其他项目，Code Relay 会保持原目录不变，并要求使用单独的目标目录。

完整用户流程见 [USER_GUIDE.md](USER_GUIDE.md)。

## 仓库结构

```text
.codex-plugin/plugin.json       # Codex 插件清单
.mcp.json                       # 源码工作区 MCP 配置（Go 开发回退入口）
skills/                         # 编排端和验证端 Skills
cmd/code-relay-agent/           # Go CLI 与 MCP 入口
internal/relay/                 # Go 协议、执行器、Git 与 MCP 实现
schemas/                        # task 和 receipt JSON Schema
templates/                      # task 和 receipt Markdown 模板
.github/workflows/              # 可选的目标机 self-hosted runner 工作流
internal/relay/*_test.go        # 单元、协议、并发和端到端测试
```

## 开发者本地验证

项目依赖较少，需要 Go 1.26+；CI 与发布构建使用当前稳定的 Go 1.27 工具链：

```powershell
go test ./...
go vet ./...
go build ./cmd/code-relay-agent
./scripts/validate-contracts.ps1
./scripts/smoke-e2e.ps1
```

CI 还会在具备固定 C 工具链的 Linux 环境运行 `go test -race ./...`。

CLI 是开发和 CI 的备用入口，不是普通用户的安装入口：

```powershell
code-relay-agent publish --root . --file examples/task-001.md --no-git
code-relay-agent run-task task-001 --root .
code-relay-agent status --root .
```

Go 运行时支持 Linux、macOS 和 Windows（macOS 同时提供 Intel 与 Apple Silicon 构建）：

```powershell
./scripts/build-agent.ps1
```

要生成包含当前平台 MCP 可执行文件和正确 `.mcp.json` 的插件包，可运行：

```powershell
./scripts/package-plugin.ps1
```

发布验收请参照 `RELEASE_CHECKLIST.md`，验证机部署和故障处理请参照
`deploy/RUNBOOK.md`。

源码工作区的 `.mcp.json` 使用跨平台的 `go run`，方便开发者直接调试；插件包会替换为当前平台的原生二进制，普通用户运行时不需要安装 Go。

在 macOS 或 Linux 上也可以直接运行 `./scripts/build-agent.sh`。版本发布会生成五个 agent 二进制、`release.json`、SBOM 元数据和 `SHA256SUMS`。

在目标机上安装带 `codex-b` 标签的 GitHub Actions self-hosted runner。workflow 会构建匹配平台的 agent 并调用 `run-pending`，不需要预装 Relay daemon 或 Python runtime。

排查主机环境可运行 `code-relay-agent doctor --root .`。

## 安全边界

Code Relay 目前是 MVP。验证命令不经过 shell 执行，shell 解释器与内联 eval 模式会被拒绝，输出大小和执行时间受到限制；危险命令模式会被拦截。用户仍需审阅任务内容和 runner 权限。

默认邀请链接是短时 bearer link；受控部署可以在 Dev Host 和 Target Host 设置相同的 32 位以上 `CODE_RELAY_INVITE_SECRET`，启用 HMAC 完整性校验。用户级授权、撤销和集中式权限管理仍需要托管控制平面。

在暴露 runner 或 webhook 前，请先阅读 [SECURITY.md](SECURITY.md)；提交改动前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)；参与项目请遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

## 项目状态

当前不兼容大版本为 Code Relay 2.0，是 Go-only 的 MVP 参考实现。协议和目录结构保持小而清晰，方便团队检查、复用，或替换其中的运行时组件。

## 许可证

Code Relay 使用 [MIT License](LICENSE) 发布。
