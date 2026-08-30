# Code Relay 用户使用指南

Code Relay 适合这样的工作方式：你只在 A 主机的 Codex 中描述需求，A 负责开发和发布任务，B 主机负责在目标环境验证，最后把结构化回执交回 A。

## 1. 你会看到什么

一次任务通常经历以下状态：

```text
开发中 → PR/CI → 已合入 → B 验证中 → 回执已发布 → 完成/继续修复/阻塞
```

代码、任务和回执都在同一个 GitHub 仓库中：

```text
tasks/<task_id>/task.md              # A 发给 B 的任务
receipts/<task_id>/receipt.json      # B 给程序读取的结果
receipts/<task_id>/receipt.md        # B 给人和 Codex 阅读的结果
```

你不需要手工维护数据库，也不需要打开 Web 控制台查看状态。

## 2. 一次性准备

普通用户只需从 Codex 插件入口安装 **Code Relay**。不需要手动执行 `pip install`、`conda install` 或 `relay install`；这些属于插件内部的 runtime 配置动作。

### A 主机（编排端）

安装插件后，在目标工程目录的 Codex 中说：“为当前工程当前分支启用 Code Relay”。插件会读取 Git remote 和当前分支，生成项目配置、workflow、任务/回执目录，提交并推送。

### B 主机（验证端）

B 用户安装同一个 Code Relay 插件，把 A 生成的加入链接粘贴到该工程的 Codex 中，并确认仓库、分支和权限。插件默认使用 `code-relay-agent` Go runtime，生成 verifier 配置并启动 watcher；开发调试时可显式选择 `runtime=python`。准备同一个 GitHub 仓库的 self-hosted runner，并添加标签 `codex-b`；runner 注册是唯一需要在 GitHub/主机层完成的一次性管理操作。

如果暂时没有 GitHub runner，开发者可以在 B 的工作目录用内部调试命令启动 watcher（普通用户无需执行）：

```powershell
python -m code_relay.daemon --root . --role verifier
```

排查主机环境时运行：

```powershell
python -m code_relay.relay --root . doctor --json
code-relay-agent doctor --root .
```

生产环境建议使用 workflow，因为它会根据任务里的 `source_commit` 创建隔离 worktree，避免验证到错误版本。

## 3. A 端标准操作

### 第一步：完成开发并合入

在 A 的 Codex 中描述业务目标，例如：

> 完成支付回调幂等改造，提交并合入；合入后让 B 验证，失败时继续修复。

开发、提交、PR 和 CI 完成后，记下合入后的完整 commit SHA。不要使用工作分支的旧 SHA。

### 第二步：自动发布任务

不需要手工编辑 task.md。A 的 Code Relay Skill 会根据当前对话、合入后的 commit SHA、验证命令和预期结果自动生成任务，分配 task ID，提交并推送到已绑定的远端分支。

你只需要说：

> 代码合入后，让 B 在目标环境验证支付回调幂等性，并收集日志和截图。

重复发送同一个任务是幂等的；同一 ID 内容不一致时，插件会停止并提示确认。

### 第三步：查看状态

直接在 Codex 中询问：“查看当前 Relay 状态”或“查看 task-001 的验证结果”。插件会调用内部状态工具。

常见状态含义：

- `task_published`：任务已发布，等待 B
- `passed`：B 的所有检查通过
- `failed`：至少一项检查失败
- `blocked`：任务缺少可执行命令，或命令被安全策略拦截
- `invalid`：任务或回执格式错误，需要先修复协议问题

插件会自动监听回执。需要时可以在 Codex 中说：“读取 task-001 回执并给出下一步”。

`analyze` 有三种结论：

- `done`：验证完成，可以向用户报告结果
- `iterate`：展示失败检查，修复代码后创建新的子任务，例如 `task-002`
- `blocked`：停止自动修改，先让用户决定如何处理

`done` 返回退出码 0；`iterate` 和 `blocked` 返回退出码 2，便于脚本或 daemon 判断。

## 4. B 端如何验证

B 只执行任务中明确列出的验证命令。默认支持常见的 `python`、`pytest`、`npm`、`node`、`go`、`cargo`、`dotnet` 等命令；危险操作（例如递归删除、强制 reset、push）会被拦截。

无论验证成功、失败、超时还是被拦截，都必须生成：

```text
receipts/<task_id>/receipt.json
receipts/<task_id>/receipt.md
```

正常情况下，B 用户不需要手工执行任务；watcher 会在发现新 task 后自动启动 B Codex。CLI 仅用于开发者故障排查。

回执会记录每条命令的 expected、actual、status、耗时、环境、风险和后续建议。`task_id`、`source_commit` 和任务内容哈希不一致时，A 端会拒绝回执。

## 5. 推荐的失败处理方式

1. 运行 `fetch-receipt` 查看失败项和证据。
2. 在 A 上修复代码并完成本地测试。
3. 创建新的任务 ID，并把父任务写入 objective 或后续说明。
4. 使用新的合入 commit SHA 发布，例如 `task-002`。
5. 再次等待 B 回执，直到 `done` 或明确进入 `blocked`。

不要覆盖一个已经产生回执的任务来表示“下一轮”；使用新的任务 ID 更容易审计和恢复。

## 6. 常见问题

### 找不到回执

确认 B runner 在线、workflow 已启用，并检查 `tasks/<task_id>/task.md` 是否已经推送到远端。A 端 daemon 只会读取当前 checkout 能看到的文件。

### 回执显示 `invalid`

优先检查 `task_id`、`source_commit` 是否完全一致，以及 receipt JSON 是否包含 `checks` 数组和每项的 `name/expected/actual/status`。

### 回执显示 `blocked`

检查 Validation Plan 是否写了明确命令，而不是只有自然语言描述；同时确认命令没有触发危险命令拦截规则。

### 验证了错误代码版本

检查 `source_commit` 是否为合入后的完整 SHA。GitHub workflow 会按该 SHA 创建隔离 worktree；不要填分支名或未合入的工作树版本。

## 7. 安全建议

- B runner 使用独立、低权限账号和独立 worktree。
- 在 A、B 主机安全配置相同的 `CODE_RELAY_INVITE_SECRET`（至少 32 个字符），让邀请链接启用 HMAC 完整性校验；不要把该值写进 Git、task、receipt 或聊天记录。
- GitHub Token 只授予仓库内容读写和 Actions 所需的最小权限。
- 不要把 Token、密码或其他密钥写入 task、receipt 或日志。
- 不要把未经审查的任意 shell 脚本放入 Validation Plan。
- 生产环境保留 `.code-relay/events.jsonl` 和 Git 历史，便于审计与故障恢复。
