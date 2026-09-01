<h1 align="center">
  <img src="assets/icon.png" alt="" width="36" height="36" style="vertical-align: middle;">
  Code Relay
</h1>

<p align="center">
  <a href="README.md">英文版</a> · <a href="README.zh-CN.md">中文版</a>
</p>

<h2 align="center">
  构建，验证，接力。
</h2>

> **Code Relay 让Agent在一台机器上开发，在另一台机器上证明结果。**

Relay 是基于 MCP 的跨机器开发验证工具。开发机上的agent负责实现需求，
目标机上的验证环境负责执行真实测试，将结构化回执返回给开发机，
推动下一轮修复。它支持 Codex、Claude Code、Cursor、VS Code 以及其他
支持 MCP 的coding agents。

## 什么时候需要 Relay

- **环境不同**：在 Windows 或 macOS 开发，在 Linux、内网或生产同构环境验证。
- **需要特殊硬件**：把模型代码交给 CUDA/GPU 机器，或在连接摄像头、串口设备、传感器的机器上验证。
- **需要真实依赖**：验证支付回调、队列、数据库、浏览器或本地无法复现的内部服务。
- **需要 AI 持续迭代**：回传具体的预期与实际结果，让agent修复、重新发布并再次验证。

## Relay 提供什么

- 分支级项目绑定和短时加入链接。
- 在隔离工作树中验证精确的源提交。
- 通过 `code-relay-checkpoint` GitHub Actions 自托管运行器执行 Checkpoint。
- 验证命令白名单、输出大小和超时控制。
- 结构化 `receipt.json` 与人类可读回执，覆盖通过、失败和阻塞状态。

## 快速开始

### AI 客户端安装

Agent阅读[AI 客户端安装指南](install.md)：

```text
https://raw.githubusercontent.com/zarcherlot/code-relay/main/install.md
```

### npm 安装

```sh
npx -y code-relay-mcp@latest install --client codex
```

按客户端替换为 `claude-code`、`cursor`、`vscode` 或 `generic`。

## MCP 服务器信息

Code Relay 是通过 **stdio** 通信的本地 MCP 服务器。直接启动：

```sh
npx -y code-relay-mcp@3.1.0
```

服务器在 Go 中直接实现 MCP JSON-RPC（不依赖第三方 MCP SDK），支持
`initialize`、`ping`、`tools/list` 和 `tools/call`。可用工具包括：

`bind_project`、`create_checkpoint_invite`、`join_checkpoint`、
`watcher_status`、`stop_watcher`、`doctor`、`publish_runbook`、`status`、
`fetch_receipt`、`analyze`。

实现与传输循环见 [`internal/relay/mcp.go`](internal/relay/mcp.go)。

## 工作原理

<p align="center">
  <img src="assets/overview.png" alt="开发机通过 Code Relay 向目标机发送 runbook；目标机执行 Checkpoint 并返回带证据的 Receipt" width="836">
</p>

开发机通过 Relay 发送绑定仓库和分支的 runbook；目标机在
真实环境中检查准确的源提交，并返回记录 Checkpoint 结果的 Receipt。

每份 runbook 都绑定仓库、分支和源提交。目标机作为 Checkpoint，
回执记录通过、失败或阻塞结果，供下一轮迭代使用。

## 文档

- [英文版 README](README.md)
- [用户指南](USER_GUIDE.md)
- [AI 客户端安装指南](install.md)
- [本机安装运行手册](deploy/LOCAL-INSTALL-RUNBOOK.md)
- [部署与运行手册](deploy/RUNBOOK.md)
- [安全说明](SECURITY.md)
- [贡献指南](CONTRIBUTING.md)

## 许可证

Code Relay 使用 [MIT 许可证](LICENSE) 发布。
