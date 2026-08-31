# Code Relay 用户使用指南

Code Relay 适合这样的工作方式：你只在 A 主机的 Codex 中描述需求，A 负责开发并发布 runbook，B 主机作为 Checkpoint 在目标环境验证，最后把结构化 Receipt 交回 A。

## 1. 你会看到什么

一份 runbook 通常经历以下状态：

```text
开发中 → PR/CI → 已合入 → Checkpoint 验证中 → Receipt 已发布 → 完成/继续修复/阻塞
```

代码、runbook 和 Receipt 都在同一个 GitHub 仓库中：

```text
runbooks/<runbook_id>/runbook.md        # A 发给 Checkpoint 的验证运行手册
receipts/<runbook_id>/receipt.json      # Checkpoint 给程序读取的结构化结果
receipts/<runbook_id>/receipt.md        # Checkpoint 给人和 Codex 阅读的结果
```

你不需要手工维护数据库，也不需要打开 Web 控制台查看状态。

## 2. 一次性准备

普通用户可以直接从 Codex 插件入口安装 **Code Relay**。如果使用本仓库进行
桌面版本地安装，请先完成下面的打包步骤；打包后的插件直接启动原生 Go
`code-relay-agent`，运行时不需要安装 Python、Go 或其他语言运行时。

### 从源码仓库进行本地安装

仓库已包含 `.agents/plugins/marketplace.json`，不需要手工创建 marketplace。
在仓库根目录执行：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\package-plugin.ps1 `
  -Output .\dist\plugin
```

这一步只负责把源码编译成当前系统/架构的插件包；从源码构建需要 Go。若已
获得预构建的 `dist/plugin`，可直接跳过。随后：

1. 重启 ChatGPT 桌面版。
2. 打开 `Plugins Directory`（部分账号显示为 `Plugins` 或 `Apps`）。
3. 选择 `Code Relay (Local)` → `Code Relay` → `Install`。
4. 新建聊天，确认插件可以被调用。

保持默认的 `dist/plugin` 输出路径即可，不需要执行
`codex plugin marketplace add`。更改输出目录时，才需要同步修改
`.agents/plugins/marketplace.json` 的 `source.path`。从零开始准备源码和依赖时，
请参阅[本机安装运行手册](deploy/LOCAL-INSTALL-RUNBOOK.md)。

### A 主机（Relay 端）

安装插件后，在目标工程目录的 Codex 中说：“为当前工程当前分支启用 Code Relay”。插件会读取 Git remote 和当前分支，生成项目配置、workflow、runbook/Receipt 目录，提交并推送。

### B 主机（Checkpoint 端）

B 用户安装同一个 Code Relay 插件，把 A 生成的加入链接粘贴到该工程的 Codex 中，并确认仓库、分支和权限。插件生成 Checkpoint 配置；随后在同一仓库注册带 `codex-b` 标签的 GitHub Actions self-hosted runner。runner 注册是唯一需要在 GitHub/主机层完成的一次性管理操作。

排查主机环境时运行：

```powershell
code-relay-agent doctor --root .
```

生产环境建议使用 workflow，因为它会根据 runbook 里的 `source_commit` 创建隔离 worktree，避免验证到错误版本。

## 3. A 端标准操作

### 第一步：完成开发并合入

在 A 的 Codex 中描述业务目标，例如：

> 完成支付回调幂等改造，提交并合入；合入后让 B 验证，失败时继续修复。

开发、提交、PR 和 CI 完成后，记下合入后的完整 commit SHA。不要使用工作分支的旧 SHA。

### 第二步：自动发布 runbook

不需要手工编辑 `runbook.md`。A 的 Code Relay Skill 会根据当前对话、合入后的 commit SHA、验证命令和预期结果自动生成 runbook，分配 `runbook_id`，提交并推送到已绑定的远端分支。

你只需要说：

> 代码合入后，让 B 在目标环境验证支付回调幂等性，并收集日志和截图。

重复发布同一份 runbook 是幂等的；同一 ID 内容不一致时，插件会停止并提示确认。

### 第三步：查看状态

直接在 Codex 中询问：“查看当前 Relay 状态”或“查看 runbook-001 的验证结果”。插件会调用内部状态工具。

常见状态含义：

- `runbook_published`：runbook 已发布，等待 Checkpoint
- `passed`：B 的所有检查通过
- `failed`：至少一项检查失败
- `blocked`：runbook 缺少可执行命令，或命令被安全策略拦截
- `invalid`：runbook 或 Receipt 格式错误，需要先修复协议问题

插件会自动监听回执。需要时可以在 Codex 中说：“读取 runbook-001 回执并给出下一步”。

`analyze` 有三种结论：

- `done`：验证完成，可以向用户报告结果
- `iterate`：展示失败检查，修复代码后发布下一版 runbook，例如 `runbook-002`
- `blocked`：停止自动修改，先让用户决定如何处理

CLI 会以结构化 JSON 返回 `done`、`iterate` 或 `blocked` 结论，供 Codex 或脚本读取。

## 4. Checkpoint 如何验证

Checkpoint 只执行 runbook 中明确列出的验证命令。Relay 不预设某一种项目语言；命令必须通过 allowlist，危险操作（例如递归删除、强制 reset、push）会被拦截。

无论验证成功、失败、超时还是被拦截，都必须生成：

```text
receipts/<runbook_id>/receipt.json
receipts/<runbook_id>/receipt.md
```

正常情况下，B 用户不需要手工执行 runbook；GitHub Actions runner 会在发现 runbook 变更后调用 `code-relay-agent run-pending`。CLI 仅用于开发者故障排查。

Receipt 会记录每条命令的 expected、actual、status、耗时、环境、风险和后续建议。`runbook_id`、`source_commit` 和 runbook 内容哈希不一致时，A 端会拒绝 Receipt。

## 5. 推荐的失败处理方式

1. 运行 `fetch-receipt` 查看失败项和证据。
2. 在 A 上修复代码并完成本地测试。
3. 创建新的 `runbook_id`，并把上一版 runbook 写入 objective 或后续说明。
4. 使用新的合入 commit SHA 发布，例如 `runbook-002`。
5. 再次等待 Checkpoint 返回 Receipt，直到 `done` 或明确进入 `blocked`。

不要覆盖一份已经产生 Receipt 的 runbook 来表示“下一轮”；使用新的 `runbook_id` 更容易审计和恢复。

## 6. 常见问题

### 找不到回执

确认 Checkpoint runner 在线、workflow 已启用，并检查 `runbooks/<runbook_id>/runbook.md` 是否已经推送到远端。

### 回执显示 `invalid`

优先检查 `runbook_id`、`source_commit` 是否完全一致，以及 receipt JSON 是否包含 `checks` 数组和每项的 `name/expected/actual/status`。

### 回执显示 `blocked`

检查 Validation Plan 是否写了明确命令，而不是只有自然语言描述；同时确认命令没有触发危险命令拦截规则。

### 验证了错误代码版本

检查 `source_commit` 是否为合入后的完整 SHA。GitHub workflow 会按该 SHA 创建隔离 worktree；不要填分支名或未合入的工作树版本。

## 7. 安全建议

- B runner 使用独立、低权限账号和独立 worktree。
- 在 A、B 主机安全配置相同的 `CODE_RELAY_INVITE_SECRET`（至少 32 个字符），让邀请链接启用 HMAC 完整性校验；不要把该值写进 Git、runbook、receipt 或聊天记录。
- GitHub Token 只授予仓库内容读写和 Actions 所需的最小权限。
- 不要把 Token、密码或其他密钥写入 runbook、receipt 或日志。
- 不要把未经审查的任意 shell 脚本放入 Validation Plan；运行时也会拒绝 shell 解释器和内联 eval 模式。
- 保留 GitHub Actions 日志、receipt 和 Git 历史，便于审计与故障恢复。
