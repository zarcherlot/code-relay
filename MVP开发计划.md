# Code Relay：命名迁移与 Go Runtime 开发计划

## 目标

将产品统一命名为 **Code Relay**，并把常驻 watcher、daemon 和受限验证 runner 逐步迁移到 Go，支持 Linux amd64/arm64 与 Windows amd64。Codex Plugin、Skills、任务协议和 Python facade 保持兼容。

## 命名与兼容策略

新项目使用：

```text
code-relay                  CLI
code-relay-agent            Go runtime
code-relay://join/...       新邀请链接
.code-relay/                项目元数据
CODE_RELAY_INVITE_SECRET    新密钥变量
code_relay/                 Python facade
```

迁移周期内继续支持 `codex-relay` CLI、`codex-relay://` 邀请链接、`.codex-relay/` 目录、`CODEX_RELAY_INVITE_SECRET` 和 `codex_mate`/`codex_relay` Python 命名空间。规则是“新名称写入、旧名称读取”。`code-relay migrate` 复制旧元数据到新目录，不删除旧目录。

## 目标架构

```text
Codex Plugin / Skills
        ↓
Python MCP facade
        ↓
code-relay-agent
  ├─ protocol / storage
  ├─ Git watcher
  ├─ Webhook daemon / queue
  └─ bounded runner
```

## 阶段 0：基线与协议冻结

- 固定 task、receipt、state、events 文件格式和 `task_id + source_commit` 幂等规则。
- 固定旧 URI、目录和环境变量的兼容行为。
- 收集 Python 端到端测试样例，建立 Python-Go 共享 fixtures。

验收：现有 Python 测试通过；Python 生成的样例可作为 Go 测试输入。

## 阶段 1：Code Relay 命名迁移

- 更新插件 manifest、README、用户指南、安全文档和示例。
- 增加 `code_relay` Python facade、`code-relay` 和 `code-relay-daemon` 命令。
- 保留旧命令和旧 Python 包导出。
- 新邀请使用 `code-relay://join/...`，解析时兼容旧 URI。
- 新项目默认使用 `.code-relay/`。

验收：新项目不生成旧名称；旧项目无需手工修改即可运行。

## 阶段 2：Python 迁移层

- 实现旧目录到新目录的安全复制。
- 优先读取 `CODE_RELAY_INVITE_SECRET`，旧变量作为 fallback。
- verifier 配置增加 `runtime: python|go`。
- 增加 Go agent 自动发现和 Python fallback。

验收：迁移可重复执行；异常不会破坏旧目录；无 Go 二进制时行为不变。

## 阶段 3：Go agent 协议与存储

- 实现 `cmd/code-relay-agent` 和 `internal/relay`。
- 实现 task.md 最小协议解析、receipt 结构、SHA-256 绑定。
- 实现状态、事件队列和原子文件写入。
- 支持新旧元数据目录读取。
- 提供 `version`、`status`、`validate-task` 命令。

验收：Go/Python 互读任务、回执和状态；`go test ./...` 通过。

## 阶段 4：Go watcher 与 Git 同步

- 只 fetch 已绑定的 `repository + ref`。
- 将新任务写入私有 inbox，不切换或覆盖 active worktree。
- 支持轮询、停止、重启恢复和任务去重。
- 增加 Linux systemd 与 Windows Service 适配层。

验收：远端任务能稳定 materialize；watcher 重启不重复处理；工作区不被修改。

## 阶段 5：Go daemon、Webhook 和队列

- 实现 HTTP Webhook、HMAC 签名校验、请求大小限制和 delivery ID 去重。
- 持久化事件队列并限制容量。
- 支持优雅关闭、崩溃恢复和状态查询。

验收：重复事件只处理一次；重启后事件不丢失；Linux/Windows 均可运行。

## 阶段 6：Go bounded runner

- argv 启动，禁止 shell。
- allowlist、denylist、超时和输出上限。
- 清理 GitHub/OpenAI/Relay 密钥环境变量。
- 校验 worktree 必须位于绑定项目内。
- 生成 passed、failed、blocked 三类 receipt。
- 增加容器或受限 runner 执行接口。

验收：不安全命令、超时、错误版本和异常工作目录均被正确处理；Linux/Windows 进程终止行为通过测试。

## 阶段 7：MCP 接入和灰度切换

- MCP 工具继续使用 `bind_project`、`join_verifier`、`watcher_status`、`stop_watcher`。
- Python facade 根据 runtime 调用 Go agent。
- Go agent 不可用时自动回退 Python。
- 开发环境、Linux verifier、CI 分阶段灰度。

验收：Codex 中的自然语言操作不变；Go/Python 可共享项目状态。

## 阶段 8：CI、打包和发布

- CI 覆盖 Ubuntu 和 Windows。
- 构建 Linux amd64、Linux arm64、Windows amd64。
- 运行 `go test -race ./...`。
- 生成 checksums 和签名文件。
- 使用 `scripts/build-agent.ps1` 构建发行包。
- 文档说明安装、升级、回退和迁移。

## 里程碑

```text
M1  Code Relay 命名和兼容层完成
M2  Python 迁移命令完成
M3  Go agent 协议/存储完成
M4  Linux watcher/daemon 完成
M5  Windows watcher/daemon 完成
M6  Go runner 安全测试完成
M7  MCP 接入和 fallback 完成
M8  跨平台构建与灰度发布完成
```

## 完成标准

新项目默认使用 Code Relay 名称；旧项目、旧链接和旧命令仍可用；Linux/Windows agent 可构建并运行；Python 与 Go 协议互通；watcher 不覆盖用户工作树；失败、阻塞和超时都生成可审计回执；完整 Python/Go 测试与跨平台 CI 通过。
