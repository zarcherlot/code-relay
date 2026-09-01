<h1 align="center">
  <img src="assets/icon.png" alt="" width="36" height="36" style="vertical-align: middle;">
  Code Relay
</h1>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">中文版</a>
</p>

<h2 align="center">
  Build. Prove. Relay.<br>
  构建，验证，接力。
</h2>

<p align="center">
  <img src="assets/overview.png" alt="Code Relay 工作流" width="836">
</p>

> **Code Relay 让 coding agent 在一台机器上开发，在另一台机器上证明结果。**

Relay 是基于 MCP 的跨机器开发验证工具，优先适配 ChatGPT/Codex 插件入口，但并不绑定某个 coding agent；Claude Code、Cursor、VS Code 以及其他支持 MCP 的 coding agent 都可以接入。开发机上的 AI 负责实现需求，目标机器上的验证环境负责执行真实测试，验证结果以结构化回执返回，并自动推动下一轮修复。

它适合需要跨操作系统、内网、GPU、硬件或生产同构环境验证的开发团队。

## 产品逻辑

```text
在 coding agent 中描述需求
        ↓
开发机 → Relay runbook → 目标机
        ↓                 ↓
      开发          在真实环境设卡验证
        ←────── Receipt / 证据 ──────
```

Relay 将每份 runbook 绑定到仓库、分支和 source commit。目标机作为 Checkpoint 验证准备交付的准确版本，并返回可审计的 Receipt；通过、失败和阻塞结果都会保留在 Git 中。

## 什么时候需要 Relay

- **环境不同**：在 Windows 或 macOS 开发，在 Linux、内网或生产同构环境验证。
- **需要特殊硬件**：把模型代码交给 CUDA/GPU 机器，或在连接摄像头、串口设备、传感器的机器上验证。
- **需要真实依赖**：验证支付回调、队列、数据库、浏览器或本地无法复现的内部服务。
- **需要 AI 持续迭代**：回传具体的 expected/actual 结果，让 coding agent 修复、重新发布并再次验证。

## 快速开始

1. 从 ChatGPT/Codex 插件界面安装 **Code Relay**，或在其他 coding agent 中配置 MCP。
2. 在开发机打开项目并说：

   > 为当前工程当前分支启用 Code Relay，并生成 Target Host 加入链接。

   也可以在当前项目上下文中使用 `/relay` 或 `/relay bind`；插件会优先读取
   当前仓库和分支并自动完成绑定。

3. 在目标机安装同一个插件或 MCP 服务，把加入链接粘贴到 coding agent 中。
4. 目标机作为 Checkpoint 加入获准的仓库和分支，通过带 `code-relay-checkpoint` 标签的 GitHub Actions runner 执行 runbook 并发布 Receipt。

打包后的插件不需要用户安装 Python、Go 或单独的 Relay runtime。完整流程和故障恢复见 [USER_GUIDE.md](USER_GUIDE.md)。

### 通过 npm 安装（任意 MCP 客户端）

Code Relay 同时以 `code-relay-mcp` 发布。任何支持 MCP 的 coding agent 都可以遵循
[install.md](install.md)，也可以先预览、再执行客户端专用安装器：

```sh
npx -y code-relay-mcp@latest install --client codex
npx -y code-relay-mcp@latest install --client codex --yes
```

需要保留全局命令时，也可以先安装 npm 包：

```sh
npm install --global code-relay-mcp
code-relay-mcp install --client codex --yes
```

可将 `codex` 替换为 `claude-code`、`cursor`、`vscode` 或 `generic`。需要
Node.js 18+；首次启动时，npm launcher 会下载当前平台对应的原生 Release
二进制，使用 `SHA256SUMS` 校验后写入本机缓存，不需要 Go。npm 发行包提供
MCP 工具；ChatGPT/Codex 插件还会额外提供 `$code-relay:relay` 与
`$code-relay:checkpoint` Skill，其他客户端可直接使用相同的 MCP 工具。

### 从本仓库安装到桌面版（本地安装）

仓库已经包含仓库级 marketplace：`.agents/plugins/marketplace.json`。从源码
检出时，在仓库根目录执行一次打包：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\package-plugin.ps1 `
  -Output .\dist\plugin
```

这会在 `dist/plugin` 中生成当前系统的原生 agent 和插件 `.mcp.json`。只有从
源码重新打包时需要 Go，安装后的插件运行时不需要 Go；如果已经拿到预构建的
`dist/plugin`，可以跳过命令。

重启 ChatGPT 桌面版，打开 `Plugins Directory`，选择
`Code Relay (Local)` → `Code Relay` → `Install`，然后新建聊天测试即可。请保持
默认的 `dist/plugin` 输出路径，这样仓库内的 marketplace 配置无需修改；不需要
手工编辑 marketplace JSON，也不需要执行 `codex plugin marketplace add`。

如果本机还没有源码工程，请先按[本机安装运行手册](deploy/LOCAL-INSTALL-RUNBOOK.md)
拉取仓库并安装 Git、PowerShell 7 和 Go 1.26+。

## 核心能力

- 分支级项目绑定和短时加入链接。
- 在隔离 worktree 中验证精确的 source commit。
- 通过 `code-relay-checkpoint` self-hosted runner 建立 Checkpoint。
- 验证命令白名单、输出大小和超时控制。
- 结构化 `receipt.json` 与人类可读回执，覆盖通过、失败和阻塞状态。

## 开发

项目目标 Go 1.26+：

```powershell
go test ./...
go vet ./...
go build ./cmd/code-relay-agent
npm ci
npm test
npm run verify
./scripts/validate-contracts.ps1
./scripts/smoke-e2e.ps1
```

使用 `./scripts/build-agent.ps1` 构建当前平台的 agent。CLI 是开发和 CI 的备用入口，普通用户应通过自己的 coding agent 的 MCP/插件机制安装。

## 文档

- [English README](README.md)
- [用户指南](USER_GUIDE.md)
- [AI 客户端安装指南](install.md)
- [本机安装运行手册](deploy/LOCAL-INSTALL-RUNBOOK.md)
- [部署与运行手册](deploy/RUNBOOK.md)
- [安全说明](SECURITY.md)
- [贡献指南](CONTRIBUTING.md)

## 许可证

Code Relay 使用 [MIT License](LICENSE) 发布。
