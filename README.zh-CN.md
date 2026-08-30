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
- **后台监听**：只监听已订阅的远端分支，把新任务放进私有收件箱，不覆盖当前工作树。
- **可靠回执**：任务幂等发布、绑定 source commit、校验回执、命令白名单、超时控制，以及失败/阻塞回执。
- **GitHub Actions 支持**：可使用带 `codex-b` 标签的 self-hosted runner 执行目标环境验证。

## 从 Codex 安装

从 Codex 插件界面安装 **Code Relay**。普通用户不需要执行 `pip install`、`conda install` 或单独的 relay 安装命令。

### Dev Host：绑定当前项目

在 Codex 中打开目标工程并说：

> 为当前工程当前分支启用 Code Relay，并生成 Target Host 加入链接。

插件会展示解析出的远程仓库和分支，写入 `.code-relay/project.json`，添加项目集成文件，提交并推送绑定信息，然后返回一个短时有效的加入链接。

### Target Host：加入并验证

在 Target Host 安装同一个插件，把加入链接粘贴到 Codex 中并确认：

1. 如果工作区为空或尚未绑定，插件会克隆邀请中的仓库和分支。
2. 写入验证端绑定信息并自动启动 watcher。
3. 从绑定的远程分支拉取新任务，放入私有收件箱。
4. 验证运行时或配置好的 host adapter 执行任务中的验证计划，并发布回执。

如果当前目录非空，或属于其他项目，Code Relay 会保持原目录不变，并要求使用单独的目标目录。

完整用户流程见 [USER_GUIDE.md](USER_GUIDE.md)。

## 仓库结构

```text
.codex-plugin/plugin.json       # Codex 插件清单
.mcp.json                       # 插件本地 MCP 服务
skills/                         # 编排端和验证端 Skills
code_relay/                     # Python 运行时与门面层
schemas/                        # task 和 receipt JSON Schema
templates/                      # task 和 receipt Markdown 模板
.github/workflows/              # 可选的目标机 self-hosted runner 工作流
tests/                          # 本地 MVP 与端到端测试
```

## 开发者本地验证

项目依赖较少，目标 Python 版本为 3.10+：

```powershell
python -m unittest discover -s tests -v
python -m compileall -q code_relay tests scripts
```

CLI 是开发和 CI 的备用入口，不是普通用户的安装入口：

```powershell
python -m code_relay.relay --root . publish --file examples/task-001.md --no-git
python -m code_relay.relay --root . run-task task-001
python -m code_relay.relay --root . status --json
```

Go 运行时支持 Linux 和 Windows：

```powershell
./scripts/build-agent.ps1
```

在目标机上使用 `code-relay-agent watcher --root .` 或 `code-relay-agent daemon --root . --role verifier`。验证端默认使用 Go runtime；开发调试时才显式设置 `CODE_RELAY_RUNTIME=python`。

排查主机环境可运行 `code-relay-agent doctor --root .` 或 `python -m code_relay.relay --root . doctor --json`。

## 安全边界

Code Relay 目前是 MVP。验证命令不经过 shell 执行，必须通过白名单，并受到输出大小和超时限制；危险命令模式会被拦截。用户仍需审阅任务内容和 runner 权限。

默认邀请链接是短时 bearer link；受控部署可以在 Dev Host 和 Target Host 设置相同的 32 位以上 `CODE_RELAY_INVITE_SECRET`，启用 HMAC 完整性校验。用户级授权、撤销和集中式权限管理仍需要托管控制平面。

在暴露 runner 或 webhook 前，请先阅读 [SECURITY.md](SECURITY.md)；提交改动前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)；参与项目请遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

## 项目状态

当前版本是 MVP 参考实现。协议和目录结构保持小而清晰，方便团队检查、复用，或替换其中的运行时组件。

## 许可证

Code Relay 使用 [MIT License](LICENSE) 发布。
