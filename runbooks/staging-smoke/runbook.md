# Runbook
- runbook_id: staging-smoke
- source_commit: 1f2b5c6130f4949635abf76ec8ca035aac0c59b6
- target: checkpoint
- objective: 验证 staging checkpoint runner 的真实 GitHub Actions 闭环

## Validation Plan
1. go version
2. go test ./...

## Expected Results
- Go 版本命令退出码为 0
- 全量 Go 测试退出码为 0

## Receipt Contract
- 记录每条命令的退出状态、输出和执行环境
