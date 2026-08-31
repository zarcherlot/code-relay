package relay

import (
	"fmt"
	"os"
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

// These variables can redirect Git to another repository, config, transport,
// or credential source.  They are intentionally removed from child Git
// processes; the working tree and normal user SSH configuration remain usable.
var restrictedGitEnvKeys = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CONFIG": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true,
	"GIT_CONFIG_COUNT": true, "GIT_SSH_COMMAND": true, "GIT_ASKPASS": true,
	"GIT_EXEC_PATH": true, "GIT_CEILING_DIRECTORIES": true,
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

func filteredEnvironment(blocked map[string]bool) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if blocked[key] {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func gitEnvironment() []string {
	blocked := make(map[string]bool, len(sensitiveEnvKeys)+len(restrictedGitEnvKeys))
	for key := range sensitiveEnvKeys {
		blocked[key] = true
	}
	for key := range restrictedGitEnvKeys {
		blocked[key] = true
	}
	env := filteredEnvironment(blocked)
	// Git must never wait for an interactive credential prompt in Actions or
	// a checkpoint service.  Explicit credentials configured by the caller still
	// work through the normal Git credential helper or SSH agent.
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	return env
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
