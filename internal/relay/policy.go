package relay

import (
	"sort"
	"strings"
)

var allowedCommands = map[string]bool{
	"python": true, "python3": true, "pytest": true, "py": true, "node": true,
	"npm": true, "go": true, "cargo": true, "dotnet": true, "bash": true,
	"sh": true, "pwsh": true, "powershell": true, "cmd": true, "echo": true,
}

var deniedTokens = []string{
	"rm -rf", "rmdir /s", "del /s", "format ", "shutdown", "git push",
	"git reset --hard", "curl | sh", "wget | sh", "invoke-webrequest",
	"pip install", "npm install",
}

var shellOperators = []string{"&&", "||", "|", ";", "&", ">", "<"}

var sensitiveEnvKeys = map[string]bool{
	"GITHUB_TOKEN": true, "GH_TOKEN": true, "CODE_RELAY_INVITE_SECRET": true,
	"CODE_RELAY_WEBHOOK_SECRET": true, "CODEX_API_KEY": true, "OPENAI_API_KEY": true,
}

const (
	maxCommandLength = 4096
	maxFieldLength   = 8192
	maxOutputLength  = 4000
	maxTimeout       = 3600
	gitTimeout       = 30
)

func containsShellOperator(value string) bool {
	for _, operator := range shellOperators {
		if strings.Contains(value, operator) {
			return true
		}
	}
	return false
}

func policyDocument() map[string]any {
	allowed := make([]string, 0, len(allowedCommands))
	for command := range allowedCommands {
		allowed = append(allowed, command)
	}
	sort.Strings(allowed)
	operators := append([]string(nil), shellOperators...)
	sort.Strings(operators)
	sensitive := make([]string, 0, len(sensitiveEnvKeys))
	for key := range sensitiveEnvKeys {
		sensitive = append(sensitive, key)
	}
	sort.Strings(sensitive)
	return map[string]any{
		"allowed_commands":    allowed,
		"deny_tokens":         deniedTokens,
		"shell_operators":     operators,
		"sensitive_env_keys":  sensitive,
		"max_command_length":  maxCommandLength,
		"max_output_length":   maxOutputLength,
		"max_timeout_seconds": maxTimeout,
		"git_timeout_seconds": gitTimeout,
	}
}
