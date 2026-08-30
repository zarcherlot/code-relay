# Code Relay 1.0：Go 重构发布计划

## 发布决策

本次发布是一个不兼容的大版本升级，产品与运行时统一为 **Code Relay**。只保留 `code_relay` Python 包、`code-relay` CLI、`code-relay-agent` Go Runtime，以及 `.code-relay/` 项目元数据目录；旧包名、旧 CLI、旧 URI、旧目录和旧环境变量不再读取或生成。

## 目标架构

```text
Codex Plugin / Skills
        ↓
Python MCP facade (code_relay)
        ↓
code-relay-agent (Go)
  ├─ task/receipt protocol
  ├─ Git watcher
  ├─ webhook daemon / queue
  └─ bounded validation runner
```

## 阶段 1：协议与命名冻结

- 固定 `code-relay://join/...` 邀请 URI、`.code-relay/` 元数据目录和 `CODE_RELAY_INVITE_SECRET`。
- 固定 task、receipt、state、events 文件格式，以及 `task_id + source_commit` 幂等规则。
- 删除全部旧命名空间、旧启动器和迁移入口。

验收：仓库源码、配置、文档和测试中不存在旧产品命名；新项目只生成 canonical 名称。

## 阶段 2：Python Runtime 收口

- 将绑定、邀请、watcher、daemon、MCP facade 统一放入 `code_relay/`。
- CLI 只暴露 `code-relay` 与 `code-relay-daemon`。
- Python 测试覆盖绑定、邀请校验、任务发布、回执、watcher 生命周期和安全策略。

验收：`python -m unittest discover -s tests -v` 和 `python -m compileall -q code_relay tests scripts` 通过。

## 阶段 3：Go Agent 协议与存储

- 实现 task.md 解析、receipt 生成、SHA-256 绑定、状态查询和原子文件写入。
- Go Runtime 只使用 `.code-relay/`，并清理敏感环境变量。
- 交付 Linux、macOS（darwin/amd64、darwin/arm64）和 Windows 构建，并在 CI 中覆盖 macOS。
- 提供 `version`、`status`、`validate-task`、`run-task` 命令。

验收：`go test ./...`、`go vet ./...` 通过，Python 与 Go 共享任务/回执格式。

## 阶段 4：跨平台 watcher、daemon 与 runner

- watcher 只 fetch 已绑定的 repository/ref，将新任务写入私有 inbox，不覆盖 active worktree。
- daemon 支持 webhook、HMAC、delivery 去重、持久化队列、优雅关闭和恢复。
- runner 使用 argv 启动、allowlist/denylist、超时、输出上限和 worktree 边界检查。
- 发布 Linux amd64/arm64 与 Windows amd64 构建产物，并提供 systemd/Windows Service 部署文件。

验收：Linux 与 Windows 构建通过；超时、危险命令、错误版本和异常工作目录均生成可审计回执。

## 阶段 5：CI 与发布

- CI 运行 Python 测试、Go 测试、静态检查和旧命名审计。
- 生成跨平台二进制、checksums 和版本信息。
- 发布 `1.0.0`，明确这是 breaking release，不提供旧版兼容或自动迁移。

## 完成标准

Code Relay 1.0 在 Codex 中完成“绑定 → 邀请 → 发布 task → B 验证 → 回执 → 分析”闭环；Python 与 Go Runtime 协议互通；Linux/Windows 均可构建运行；所有失败、阻塞和超时都产生回执；仓库仅保留 `code_relay` 及其 canonical 配置、Skills、脚本和文档。

## 本轮全量落地项

- Python/Go 共享运行时策略、Schema、契约测试和 fuzz 测试。
- 结构化 argv 事件回调，禁止 watcher 使用 shell 执行外部命令。
- 原子文件写入、符号链接拒绝、任务执行锁、邀请 nonce 消费、Webhook JSON 校验和限流。
- Git 超时控制、Go/Python `doctor` 诊断、结构化运行日志和优雅关闭。
- Linux systemd/Windows Service 加固、Dependabot、CI 矩阵、checksums、SBOM 和发布脚本。
- Go Agent 作为生产默认 runtime；Python runtime 只通过显式配置用于开发调试。

## 后续简化与工程加固 Backlog

### P0：发布后优先处理

1. **移除 watcher 事件钩子的 `shell=True`**：改为结构化 argv 配置并限制可执行文件，彻底避免命令注入边界。
2. **统一配置写入层**：Python 与 Go 共用“临时文件 + fsync + 原子替换 + 权限校验”策略，并为 `project.json`、`verifier.json`、`watcher.json` 增加 Schema 校验。
3. **补充 Git 操作超时与重试策略**：所有 fetch、clone、push、worktree 操作都设置超时、有限重试和可诊断错误；禁止把 token 写入命令行、日志或回执。
4. **增加任务并发锁和幂等索引**：以 `task_id + source_commit` 建立唯一索引，防止 watcher、CI 和手工触发并行执行同一任务。
5. **邀请防重放**：服务端/仓库侧记录 nonce 消费状态，加入一次性使用选项；签名邀请继续强制校验过期时间、仓库和 ref。

### P1：稳定性与可运维性

6. **Python-Go 一致性测试**：维护共享 fixtures，覆盖任务解析、回执校验、状态迁移、错误码和边界长度；CI 中同时运行两套实现。
7. **进程与服务加固**：Linux systemd 增加 `NoNewPrivileges`、`PrivateTmp`、`ProtectSystem`、资源限制；Windows 服务使用低权限账号和明确 ACL。
8. **结构化日志与诊断命令**：统一 JSON 日志字段、request/delivery/task correlation ID，并提供 `doctor` 命令检查 Git、目录权限、agent、runtime 和远端连通性。
9. **协议模糊测试与安全回归**：对 invite、task.md、receipt、webhook body 做 fuzz/property tests，固定恶意输入样本。
10. **发布供应链**：锁定 Python/Go 构建依赖，生成 SBOM、SHA-256 checksums、签名和可复现构建信息。

### P2：进一步简化产品面

11. **收敛运行时选择**：Go agent 作为生产默认，Python 仅作为 MCP facade；当 Go agent 缺失时给出明确安装错误，而不是静默切换执行路径。
12. **减少 CLI 表面积**：保留 `init`、`project-bind`、`invite`、`join`、`publish`、`status`、`run-task`、`analyze` 八个稳定命令，其余仅作为内部 MCP 操作。
13. **集中安全策略**：将命令 allowlist、denylist、超时、输出上限和敏感环境变量清单抽成一份协议定义，避免 Python/Go 分叉。
