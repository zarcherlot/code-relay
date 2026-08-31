# Code Relay MVP：产品与技术方案

## 1. 一句话定义

Code Relay 是一个以 Codex 对话为唯一工作界面的双机协作插件：A 主机负责开发和编排，B 主机作为 Checkpoint 负责目标环境验证；GitHub 负责代码、Runbook、状态和 Receipt 的中转。

## 2. MVP 用户体验

用户只在 A 的 Codex 中下达一条业务指令，例如：

> 完成支付回调幂等改造，提交并合入；合入后让 B 按目标环境验证，失败就继续修复，直到验证通过。

A Codex 自动完成：

1. 修改代码并执行本地验证。
2. 提交分支、推送并创建 PR。
3. 等待远端 CI，通过后合入主分支。
4. 根据合入版本生成 `runbooks/runbook-xxx/runbook.md`，写入 Checkpoint 的验证计划和预期结果。
5. 监听 B 的回执。
6. 拉取并分析回执；失败时开始下一轮开发，成功时向用户报告完成。

B Codex 自动完成：

1. 监听 GitHub 中新增的 Runbook。
2. 拉取 Runbook 指定的代码版本和 `runbook.md`。
3. 按验证计划执行测试或业务验证。
4. 对比实际结果和预期结果。
5. 生成回执并推送到 GitHub。

用户在 Codex 中看到的是阶段性状态，而不是 GitHub Actions 细节：

```text
✓ PR #42 已合入
● B 正在执行目标机验证
✗ 验证失败：重复回调产生两条记录
● A 已根据回执启动第二轮修复
✓ Checkpoint 验证通过，Runbook 完成
```

## 3. 产品形态

MVP 不做独立 Web 控制台，形态只有一个 Codex Plugin（Go CLI/MCP 是插件 runtime，GitHub Actions 负责目标机调度）：

```text
Code Relay Plugin
├── orchestrator Skill      # A 的绑定、发布、监听、分析规则
├── checkpoint Skill        # B 的加入、验证、Receipt 规则
├── MCP tools                # bind / invite / join / publish / analyze
└── Go runtime               # Git-backed runbook/receipt、隔离执行器
```

A、B 安装同一个插件，通过当前动作和项目配置区分角色；不要求用户安装语言运行时或执行 relay install：

```yaml
# A 主机
role: orchestrator

# B 主机
role: checkpoint
```

## 4. 最小架构

```text
用户
  ↓ Codex 对话
 A Codex + Code Relay Plugin
  ├─ 本地开发/测试
  ├─ PR、CI、合入
  ├─ 生成 runbook.md
  └─ 监听 receipts/
          ↓ GitHub
      代码仓库
      ├── runbooks/runbook-xxx/runbook.md
      ├── receipts/runbook-xxx/receipt.json
      └── receipts/runbook-xxx/receipt.md
          ↓ GitHub push / Actions
 B Code Relay Plugin + GitHub Actions runner + Go agent
  ├─ 发现 Runbook
  ├─ 执行验证
  └─ 推送回执
```

MVP 使用 GitHub Actions self-hosted runner 触发 B，B 不需要公网入站 Webhook。GitHub 负责排队、路由和重试，Go agent 只负责隔离执行和回执。

### 4.1 工程/分支绑定

一次 Code Relay 绑定由 `repository + refs/heads/<branch>` 唯一确定。A 在工程目录的 Codex 中请求“绑定当前工程当前分支”后，插件读取 Git remote 和当前分支，写入 `.code-relay/project.json`，提交并推送集成文件，并立即生成短时效 B 加入链接。B 粘贴链接后，插件先展示仓库、分支和权限；若当前 Codex 窗口没有仓库，确认后自动克隆该分支到空目录并关联，随后注册带 `codex-b` 标签的 GitHub Actions runner。若已有不同仓库/分支，原目录保持不变。

## 5. GitHub 数据约定

```text
runbooks/
└── runbook-001/
    └── runbook.md
receipts/
└── runbook-001/
    ├── receipt.json
    ├── receipt.md
    └── logs/                # 可选
```

每份 Runbook 都有唯一 `runbook_id`，并绑定合入后的 `source_commit`，避免 Checkpoint 验证错误版本。

### runbook.md 必填内容

```markdown
# Runbook
- runbook_id: runbook-001
- source_commit: abc1234
- target: B
- objective: ...

## Validation Plan
1. ...

## Expected Results
- ...

## Receipt Contract
- 执行状态
- 实际命令和环境
- 每项验证的 expected / actual / status
- 日志或证据位置
- 风险和后续建议
```

### receipt.json 最小结构

```json
{
  "runbook_id": "runbook-001",
  "source_commit": "abc1234",
  "status": "passed",
  "checks": [
    {
      "name": "重复回调验证",
      "expected": "只生成一笔订单",
      "actual": "只生成一笔订单",
      "status": "passed"
    }
  ],
  "risks": [],
  "next_actions": []
}
```

`receipt.md` 用于 Codex 阅读和向用户总结；`receipt.json` 用于程序判断。失败和阻塞也必须生成回执。

## 6. 状态机

```text
developing → pr_open → ci_running → merged
    → runbook_published → verifying → receipt_published
    → analyzing → done
                         ├─ failed → developing（下一轮）
                         └─ blocked → 用户决策
```

状态来源保持简单：PR/check 状态、Runbook 文件、Receipt 文件和 commit SHA，不引入独立数据库。

## 7. MVP 范围

必须有：

- A Codex 的自然语言触发和 Runbook 编排 Skill。
- B Codex 的验证 Skill。
- GitHub Runbook 和 Receipt 目录约定。
- A 开发、PR、CI、合入后的 Runbook 发布。
- B self-hosted runner 执行验证。
- `receipt.json` + `receipt.md` 回传。
- A 监听/拉取回执并自动分析。
- 一轮失败后的人工确认或自动进入下一轮。

明确不做：

- 独立 Web UI。
- 多租户、权限中心和计费。
- 实时逐行日志流。
- 自建消息队列。
- 自动处理任意危险命令。
- 大文件长期存储（大日志可后续接对象存储）。

## 8. 成功标准

从 A 的 Codex 输入一句需求指令开始，能够完成以下闭环：

```text
本地开发 → PR → CI → 合入 → 发布 runbook.md
→ B 自动验证 → 推送 receipt
→ A 自动分析 → 完成或发起下一轮
```

单份 Runbook 不需要用户手工编辑 Markdown、打开 GitHub 页面或登录 Checkpoint 主机。
