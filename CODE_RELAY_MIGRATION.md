# Code Relay 命名与 Go Runtime 迁移

本次版本将产品名称统一为 **Code Relay**，并逐步将常驻 runtime 迁移到 Go。

## 兼容规则

- 新项目使用 `code-relay`、`.code-relay/`、`code-relay://join/...` 和 `CODE_RELAY_INVITE_SECRET`。
- 旧的 `codex-relay` CLI、`codex-relay://` 邀请链接、`.codex-relay/` 目录和 `CODEX_RELAY_INVITE_SECRET` 在过渡周期内继续可用。
- 运行 `code-relay migrate --root <project>` 可将旧元数据复制到新目录；迁移不会删除旧目录。
- `codex_mate` 和 `codex_relay` Python 命名空间保留为兼容层，新的代码使用 `code_relay`。

## Go runtime

`code-relay-agent` 提供协议校验、状态查询、watcher、Webhook daemon 和受限验证执行。MCP 默认仍通过 Python facade 调用；设置 `runtime: go` 或 `CODE_RELAY_RUNTIME=go` 后启用 Go watcher，找不到二进制时回退 Python。

支持的构建目标为 Linux amd64、Linux arm64 和 Windows amd64。使用 `scripts/build-agent.ps1` 构建本地发行包。
