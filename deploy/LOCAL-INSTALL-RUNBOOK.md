# Code Relay 本机安装运行手册

本手册用于把本仓库中的 Code Relay 安装到 ChatGPT 桌面版。它只覆盖本机
插件安装，不包含 Target Host、GitHub Actions runner 或远端 MCP Gateway 的
部署。

## 安装目标

安装完成后，ChatGPT 桌面版可以从仓库级 marketplace 发现并安装：

```text
Code Relay (Local) → Code Relay
```

仓库已经提供 `.agents/plugins/marketplace.json`，默认指向
`./dist/plugin`。不要把仓库根目录直接当作最终插件目录：根目录的
`.mcp.json` 是源码开发回退入口，使用 `go run`。

## 前置条件

- 已安装 ChatGPT 桌面版，并且当前账号/工作区允许使用插件。
- 已检出本仓库，并能在终端进入仓库根目录；从零开始时按[源码准备](#源码准备)执行。
- 使用预构建包时，只需要仓库中的 `dist/plugin`；从源码打包时还需要
  Go 1.26+ 和 PowerShell 7（命令名为 `pwsh`）。

## 路径 A：使用预构建包

如果仓库已经包含 `dist/plugin/.codex-plugin/plugin.json` 和
`dist/plugin/bin/code-relay-agent*`，无需安装 Go，也无需重新打包。直接执行
[安装步骤](#安装到-chatgpt-桌面版)。

## 路径 B：从源码打包

### 1. 源码准备

如果本机还没有工程，先安装 Git 并拉取仓库：

```powershell
git clone https://github.com/zarcherlot/code-relay.git .\code-relay
Set-Location .\code-relay
```

然后安装以下工具：

| 工具 | 用途 | 要求 |
|---|---|---|
| Git | 拉取源码 | 当前稳定版 |
| PowerShell | 执行跨平台打包脚本 | PowerShell 7+，命令为 `pwsh` |
| Go | 编译原生 agent | Go 1.26+ |

Windows 可以使用 `winget` 安装（也可以改用官方安装包）：

```powershell
winget install --id Git.Git -e
winget install --id Microsoft.PowerShell -e
winget install --id GoLang.Go -e
```

macOS/Linux 请使用系统包管理器或官方安装包，确保 `git`、`pwsh` 和 `go`
都已加入 `PATH`。安装后重新打开终端并检查版本：

```powershell
git --version
pwsh --version
go version
```

确认 Go 版本至少为 1.26。项目使用 `go.mod` 管理 Go 依赖，不需要单独执行
`go install`；打包时 `go build` 会自动解析并下载所需模块。

### 2. 构建插件包

在仓库根目录执行：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\package-plugin.ps1 `
  -Output .\dist\plugin
```

脚本会根据当前操作系统和 CPU 架构：

1. 编译原生 `code-relay-agent`。
2. 生成指向该二进制的插件 `.mcp.json`。
3. 复制 manifest、skills、schemas、templates 和 assets。
4. 对 agent 版本和生成的 `.mcp.json` 做基本检查。

看到类似下面的输出即表示打包完成：

```text
Plugin package: <repository>\dist\plugin
```

## 安装到 ChatGPT 桌面版

1. 打包完成后完全退出并重新启动 ChatGPT 桌面版。
2. 打开 `Plugins Directory`；部分账号会显示为 `Plugins` 或 `Apps`。
3. 选择 `Code Relay (Local)`，再选择 `Code Relay`。
4. 点击 `Install`。
5. 新建一个聊天，使用 `@` 或 `+ → More`（Codex 任务视图使用
   `Sources → Use plugins`）调用 Code Relay。

## 安装验收

在新聊天中发送一个只读请求，例如：

```text
Show Code Relay verifier status and the latest receipts.
```

验收标准：

- Code Relay 出现在当前聊天可用的插件/来源列表中。
- 请求能够触发 Code Relay，而不是要求手工运行 `go run`。
- 没有安装 Python、Go 或单独 Relay daemon 的提示。

## 更新插件

1. 修改源码后重新执行路径 B 的打包命令；始终使用默认输出目录
   `dist/plugin`。
2. 重启 ChatGPT 桌面版，让本地 marketplace 重新读取插件文件。
3. 如仍显示旧内容，在 Plugins Directory 中先卸载 Code Relay，再重新安装。

如果使用自定义输出目录，必须同步修改
`.agents/plugins/marketplace.json` 的 `source.path`，否则桌面版仍会查找
`./dist/plugin`。

## 卸载

在 Plugins Directory 中打开 Code Relay 的菜单并选择卸载（或禁用）。这只会
移除桌面版中的插件缓存，不会删除仓库、`.agents/plugins/marketplace.json`
或项目中的 `.code-relay` 数据。

## 常见故障

| 现象 | 处理 |
|---|---|
| 看不到 `Code Relay (Local)` | 确认当前打开的是本仓库项目；检查 `.agents/plugins/marketplace.json` 是否存在，然后完全重启桌面版。 |
| marketplace 能看到但插件无法安装 | 检查 `dist/plugin/.codex-plugin/plugin.json` 是否存在，并确认 `source.path` 为 `./dist/plugin`。 |
| `pwsh` 或 `go` 找不到 | 改用已提供的预构建 `dist/plugin`，或安装 PowerShell 7 与 Go 1.26+ 后重试。 |
| 在另一台系统上运行失败 | 不要复用其他系统生成的包；在目标系统重新执行打包命令。 |
| 安装后仍使用旧版本 | 重启桌面版；仍未刷新时卸载后重新安装，并确认文件确实更新在 `dist/plugin`。 |
| 安装按钮不可用 | 检查账号/工作区的插件策略；工作区管理员可能已禁用插件。 |

不要把 GitHub token、runner token、邀请链接或 `CODE_RELAY_INVITE_SECRET`
写入问题报告或日志。安装插件和授权插件是两件事，按需确认每个权限提示。

## 相关文档

- [用户指南](../USER_GUIDE.md)
- [插件发布说明](../PLUGIN_PUBLISHING.md)
- [Target Host 运行手册](RUNBOOK.md)
