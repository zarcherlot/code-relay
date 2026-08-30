package relay

import (
	"fmt"
	"sort"
	"strings"
)

var allowedCommands = map[string]bool{
	"cargo":  true,
	"dotnet": true,
	"echo":   true,
	"go":     true,
	"node":   true,
	"npm":    true,
}

var deniedTokens = []string{
	"rm -rf", "rmdir /s", "del /s", "format ", "shutdown", "git push",
	"git reset --hard", "curl | sh", "wget | sh", "invoke-webrequest",
	"npm install",
}

var shellOperators = []string{"&&", "||", "|", ";", "&", ">", "<"}

var deniedCommandArguments = map[string]map[string]bool{
	"node": {"-e": true, "--eval": true, "-p": true, "--print": true},
	"npm":  {"exec": true, "x": true},
}

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

func validateCommandArguments(name string, args []string) error {
	denied := deniedCommandArguments[name]
	for _, argument := range args {
		if denied[strings.ToLower(argument)] {
			return fmt.Errorf("命令参数被安全策略拦截: %s %s", name, argument)
		}
	}
	return nil
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
	deniedArguments := make(map[string][]string, len(deniedCommandArguments))
	for command, values := range deniedCommandArguments {
		arguments := make([]string, 0, len(values))
		for argument := range values {
			arguments = append(arguments, argument)
		}
		sort.Strings(arguments)
		deniedArguments[command] = arguments
	}
	return map[string]any{
		"allowed_commands":         allowed,
		"denied_command_arguments": deniedArguments,
		"deny_tokens":              deniedTokens,
		"shell_operators":          operators,
		"sensitive_env_keys":       sensitive,
		"max_command_length":       maxCommandLength,
		"max_output_length":        maxOutputLength,
		"max_timeout_seconds":      maxTimeout,
		"git_timeout_seconds":      gitTimeout,
	}
}
