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

> **Code Relay 让 coding agent 在一台机器上开发，在另一台机器上证明结果。**

Relay 是基于 MCP 的跨机器开发验证工具。开发机上的 AI 负责实现需求，
目标机上的验证环境负责执行真实测试，结构化回执会返回给 coding agent，
推动下一轮修复。它支持 Codex、Claude Code、Cursor、VS Code 以及其他
支持 MCP 的 coding agent。

## 什么时候需要 Relay

- **环境不同**：在 Windows 或 macOS 开发，在 Linux、内网或生产同构环境验证。
- **需要特殊硬件**：把模型代码交给 CUDA/GPU 机器，或在连接摄像头、串口设备、传感器的机器上验证。
- **需要真实依赖**：验证支付回调、队列、数据库、浏览器或本地无法复现的内部服务。
- **需要 AI 持续迭代**：回传具体的 expected/actual 结果，让 coding agent 修复、重新发布并再次验证。

## Relay 提供什么

- 分支级项目绑定和短时加入链接。
- 在隔离 worktree 中验证精确的 source commit。
- 通过 `code-relay-checkpoint` GitHub Actions self-hosted runner 执行 Checkpoint。
- 验证命令白名单、输出大小和超时控制。
- 结构化 `receipt.json` 与人类可读回执，覆盖通过、失败和阻塞状态。

## 快速开始

### 方式一：让 AI 客户端自动安装

把仓库中的 [AI 客户端安装指南](install.md) 交给支持 MCP 的 coding agent，
让它按指南执行。指南会先预览改动并请求确认，保留已有 MCP 服务，并固定
安装版本。

### 方式二：通过 npm 安装

```sh
npx -y code-relay-mcp@latest install --client codex
```

按客户端替换为 `claude-code`、`cursor`、`vscode` 或 `generic`。如果需要
长期使用全局命令：

```sh
npm install --global code-relay-mcp
code-relay-mcp install --client codex --yes
```

需要 Node.js 18+。首次启动时 launcher 会下载并校验当前平台对应的原生
Release 二进制，运行时不需要 Go。客户端选项和故障恢复见 [install.md](install.md)。

## 工作原理

<p align="center">
  <img src="assets/overview.png" alt="开发机通过 Code Relay 向目标机发送 runbook；目标机执行 Checkpoint 并返回带证据的 Receipt" width="836">
</p>

无障碍文字说明：开发机通过 Relay 发送绑定仓库和分支的 runbook；目标机在
真实环境中检查准确的 source commit，并返回记录 Checkpoint 结果的 Receipt。

每份 runbook 都绑定仓库、分支和 source commit。目标机作为 Checkpoint，
回执记录通过、失败或阻塞结果，供下一轮迭代使用。

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
