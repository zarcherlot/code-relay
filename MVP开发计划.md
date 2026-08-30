# Codex Relay MVP：开发计划

## 目标

在两台主机 A、B 上跑通一个真实任务的完整闭环：A 在 Codex 中开发并合入代码，B 按任务验证并回传回执，A 自动分析并能发起下一轮。

## 交付物

```text
codex-relay/
├── .codex-plugin/plugin.json
├── .mcp.json
├── skills/codex-relay-orchestrator/SKILL.md
├── skills/codex-relay-verifier/SKILL.md
├── scripts/codex_relay_mcp.py
├── codex_relay/                 # 插件公开 Python facade
├── codex_mate/                  # MVP runtime（兼容内部模块名）
├── schemas/task.schema.json
├── schemas/receipt.schema.json
├── templates/task.md
├── templates/receipt.md
└── .github/workflows/verify-on-b.yml
```

## 阶段 0：验证前置条件

时间：半天

- 准备一个 GitHub 仓库和 `main` 分支。
- A、B 都能 clone/push 仓库；Relay 绑定的是某个仓库的某个远端分支（`repository + refs/heads/<branch>`）。
- B 安装 GitHub self-hosted runner，标签为 `codex-b`。
- A、B 安装并能调用 Codex。
- 准备一个可重复执行的示例验证任务。

验收：B runner 在 GitHub 上显示在线，A/B 都能完成一次普通 Git push。

## 阶段 1：定义协议和目录

时间：1 天

- 定义 `task_id`、`source_commit`、状态值和幂等规则。
- 编写 `task.schema.json` 和 `receipt.schema.json`。
- 固定 `tasks/`、`receipts/` 目录结构。
- 提供 task/receipt 模板。

验收：用样例文件通过 JSON Schema 校验；同一任务可以准确关联代码版本和回执。

## 阶段 2：实现 B 验证执行器

时间：1-2 天

- 编写 GitHub Actions Workflow，监听 `tasks/**` 的 push。
- 识别新增任务并读取 `source_commit`。
- checkout 正确代码版本。
- 按 `Validation Plan` 执行验证命令。
- 生成 `receipt.json`、`receipt.md` 和必要日志。
- 回执 commit/push 到 GitHub。
- 保证失败、超时也会生成失败回执。

验收：手工提交一个 `task.md`，B 自动执行并推送回执；重复触发不会重复执行同一任务。

## 阶段 3：实现 Codex Plugin、Skill 和内部 runtime

时间：1-2 天

- `.codex-plugin/plugin.json`：正式插件入口，声明 Skill 和 MCP 工具。
- A Skill：通过自然语言绑定当前工程/当前分支、生成 B 邀请链接、发布任务和分析回执。
- B Skill：粘贴邀请链接后预览绑定；无仓库时在确认后克隆正确分支并自动启动 watcher。
- MCP 工具：提供工程绑定、邀请、加入、watcher 状态和停止动作；底层 CLI 仅作为开发/故障排查接口。

验收：用户在 Codex 中输入一句自然语言，A 能生成任务并发布；回执拉取后能输出结论和下一步。

## 阶段 4：实现 A/B 实时感知和自举

时间：1 天

- 实现 `relay-daemon`，订阅 GitHub Webhook。
- A 监听 `pull_request`、`check_run`、`push`。
- B watcher 按绑定的远端分支轮询并把任务 materialize 到私有 inbox；不切换或覆盖 B 当前工作树。
- 加入动作写入 `verifier.json` 并启动 watcher；可选 `agent_command` 作为宿主 Codex/执行器适配钩子。
- 校验 `X-Hub-Signature-256`。
- 用 `X-GitHub-Delivery` 去重。
- 事件立即入本地队列，异步唤醒 Codex。
- 开发阶段没有公网 HTTPS 时，提供 5-10 秒轮询模式。

验收：PR 合入、B 开始验证、回执提交三个事件都能在 Codex 中显示状态更新。

## 阶段 5：打通 A 的循环决策

时间：1 天

- A 收到回执后自动检查 expected/actual/status。
- 全部通过：向用户报告完成。
- 有失败：展示失败项并询问是否继续修复；用户确认后创建下一轮任务。
- 有阻塞：停止自动修改，请用户决策。
- 保存父子任务关系，例如 `task-001` → `task-002`。

验收：让示例任务先失败一次，再修复成功，完整跑通两轮。

## 阶段 6：安全和可恢复性

时间：1 天

- B runner 使用独立低权限用户和独立 worktree。
- GitHub Token 使用最小权限，禁止把密钥写入 task/receipt。
- 限制可执行仓库、分支和命令范围。
- 设置任务超时、重试上限和并发锁。
- 回执校验 `task_id`、`source_commit` 和文件哈希。
- 对事件、执行、回执保留结构化日志。

验收：错误版本、重复事件、执行失败和进程重启都不会造成错误合入或任务丢失。

## MVP 端到端验收用例

```text
用户在 A Codex 输入：
“完成支付回调幂等改造，合入后让 B 验证，失败就继续修复。”

预期：
1. A 完成代码修改并创建 PR。
2. 远端 CI 通过后 PR 合入 main。
3. A 生成 task-001.md 并推送。
4. B 自动拉取正确 commit 并执行验证。
5. B 推送 receipt-001/receipt.json 和 receipt.md。
6. A 感知回执并展示验证结论。
7. 验证失败时，A 能发起 task-002；通过时任务结束。
```

## MVP 技术取舍

```text
代码/任务/回执：GitHub Git
B 触发：GitHub Actions self-hosted runner
A 实时通知：GitHub Webhook；无公网时轮询降级
状态存储：Git 文件和 GitHub PR/check 状态
结果格式：JSON 判断 + Markdown 阅读 + 可选 ZIP 附件
用户界面：Codex 对话（插件是唯一安装入口）
```

不要在 MVP 引入独立数据库、Redis/NATS、Web 控制台或自建 B Webhook。只有 GitHub 事件和 Git 回执无法满足延迟、并发或控制需求时再升级。

## 分支绑定原则

Relay 实例以一个工程目录下的当前分支为对象。A 和 B 不需要同步本地分支：A 只推送绑定的远端分支和任务，B watcher 只 fetch 该 `repository + ref` 并在隔离目录验证。更换分支时重新绑定并生成新的邀请链接，避免把不同验证目标混在一起。

## 推荐实现顺序

```text
协议/目录 → Codex Plugin/Skill → A 绑定与邀请 → B 加入自举 → watcher/验证器 → 多轮闭环 → 安全加固
```

优先跑通一个任务、一个仓库、一个 B 目标机，再扩展多任务和多目标机。
