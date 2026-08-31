# Code Relay 2.0：Go + GitHub Actions 发布计划

## 发布决策

Code Relay 采用 Go-only runtime。Codex Plugin 通过 `code-relay-agent mcp-stdio` 调用 Go MCP；GitHub Actions 负责调度目标机 self-hosted runner；Go agent 负责协议、隔离执行和回执。Relay 不再依赖 Python、pip 或自研 B 端轮询服务。

## 目标架构

```text
Codex Plugin
    ↓ MCP stdio
code-relay-agent (Go)
    ├─ binding / invite / join
    ├─ task / receipt protocol
    ├─ publish / status / analyze
    └─ bounded runner
             ↓ GitHub Actions
       codex-b self-hosted runner
```

## 阶段 1：Go 协议和 MCP

- 保留 `tasks/`、`receipts/`、`.code-relay/` 和现有 Schema。
- 实现 `mcp-stdio`、`bind_project`、`create_verifier_invite`、`join_verifier`。
- 统一 task/receipt 解析、SHA-256 绑定、原子写入和安全策略。

验收：Go 能读取已有 fixture，MCP 通过 initialize、tools/list、tools/call 契约测试。

## 阶段 2：Go 编排和验证

- 实现 `publish`、`fetch-receipt`、`analyze`。
- 实现 `run-pending` 和 `publish-receipts`。
- 每个任务使用 source commit 隔离 worktree、任务锁和幂等检查。
- 验证命令使用 argv，不使用 shell；限制超时、输出和敏感环境变量。

验收：成功、失败、超时、阻塞和重复任务均生成可审计回执。

## 阶段 3：GitHub Actions 集成

- B 主机使用带 `codex-b` 标签的 self-hosted runner。
- workflow 由 Go `run-pending` 执行任务，不包含 Python 脚本。
- 使用 `GITHUB_TOKEN` 的最小权限提交回执。
- 生产环境优先使用一次性或可清理 runner；固定硬件主机必须限制并发并清理 worktree。

验收：push task 后 workflow 自动运行，receipt 可被 A 端读取和分析。

## 阶段 4：发布和供应链

- CI 运行 `gofmt`、`go test -race ./...` 和 `go vet ./...`。
- 构建 Linux、macOS、Windows 的 amd64/arm64 二进制。
- 生成 SHA-256、SBOM 和版本元数据。
- 插件 MCP 配置直接启动 Go agent。

## 阶段 5：发布验收和运行维护

- 使用 `scripts/validate-contracts.ps1` 校验 Schema、fixture、插件 manifest 和 MCP 配置。
- 使用 `scripts/smoke-e2e.ps1` 验证本地通过/失败回执链路。
- 使用 `scripts/verify-release.ps1` 校验五平台产物、发布 manifest 和 SHA-256。
- 按 `RELEASE_CHECKLIST.md` 创建 tag、核验 provenance，并保留回滚记录。
- 按 `deploy/RUNBOOK.md` 完成 runner 初始化、故障排查和数据保留。

验收：`v2.0.0` release workflow 成功，Go runtime CI 通过，工作区不残留构建缓存或中间产物。

## 删除项

- `code_relay/`
- `pyproject.toml`
- Python 测试和 Python runtime workflow
- `scripts/code_relay_mcp.py`
- Relay 自研 watcher/daemon 的默认启动路径

`watcher` 和 `daemon` 命令可以暂时保留为兼容入口，但不再由插件自动启动；默认调度路径是 GitHub Actions runner。

## 完成标准

从 A Codex 输入任务开始，可以完成：

```text
开发 → 合入 → publish task → GitHub Actions 调度 B
→ Go agent 隔离验证 → publish receipt → A analyze
```

仓库源码、构建、CI 和文档中不再需要 Python。
